package report

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/xuri/excelize/v2"

	"freightflow/internal/facility"
)

// facilityAttribution — источники записей реестра (атрибуция в каждой выгрузке).
const facilityAttribution = "© участники OpenStreetMap (ODbL 1.0); адреса — Адресный реестр объектов недвижимости, " +
	"портал открытых данных Москвы data.mos.ru (CC BY 4.0); время до МКАД — расчёт FreightFlow по графу сети"

// rowWriter пишет строки реестра в формат выгрузки.
type rowWriter interface {
	Write(it facility.Item) error
	Close() error
}

func newWriter(format string, w io.Writer) (rowWriter, error) {
	switch format {
	case FormatCSV:
		return newCSVWriter(w)
	case FormatXLSX:
		return newXLSXWriter(w)
	case FormatGeoJSON:
		return newGeoJSONWriter(w)
	}
	return nil, fmt.Errorf("unknown export format %q", format)
}

// Колонки табличных форматов.
var facilityHeader = []string{
	"Идентификатор", "Название", "Тип", "Адрес", "Округ", "Район", "Пояс", "Площадь застройки, м²",
	"Общая площадь (оценка), м²", "Площадь территории, м²", "Зданий", "Достоверность", "До МКАД (грузовик 20 т), мин",
	"Разрешение на ввод", "Широта", "Долгота", "В реестре с", "Источники",
}

var ringNames = map[string]string{
	"sk": "внутри Садового кольца", "ttk": "Садовое — ТТК", "mkad": "ТТК — МКАД", "ckad": "МКАД — ЦКАД", "outside": "за ЦКАД",
}

func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// cells — значения строки: числа — числами (для XLSX), пустые — nil.
func cells(it facility.Item) []any {
	num := func(v *float64) any {
		if v == nil {
			return nil
		}
		return *v
	}
	permit := "нет"
	if it.HasPermit {
		permit = "есть"
	}
	return []any{
		it.ID.String(), str(it.Name), it.TypeName, str(it.Address), str(it.Okrug), str(it.District), ringNames[str(it.Ring)],
		num(it.BuildingM2), num(it.FloorM2), num(it.TerritoryM2), it.Buildings, it.Confidence, num(it.MkadMin),
		permit, it.Lat, it.Lon, it.FirstSeen.Format("2006-01-02"), facilityAttribution,
	}
}

// utf8BOM — метка порядка байтов UTF-8.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// csvWriter — UTF-8 с BOM (Excel распознаёт кодировку), разделитель — запятая, дробная часть — точка.
type csvWriter struct {
	buf *bufio.Writer
	w   *csv.Writer
}

func newCSVWriter(w io.Writer) (*csvWriter, error) {
	buf := bufio.NewWriter(w)
	if _, err := buf.Write(utf8BOM); err != nil {
		return nil, err
	}
	c := &csvWriter{buf: buf, w: csv.NewWriter(buf)}
	return c, c.w.Write(facilityHeader)
}

func (c *csvWriter) Write(it facility.Item) error {
	vals := cells(it)
	rec := make([]string, len(vals))
	for i, v := range vals {
		switch x := v.(type) {
		case nil:
		case string:
			rec[i] = x
		case float64:
			rec[i] = strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			rec[i] = strconv.Itoa(x)
		default:
			rec[i] = fmt.Sprint(x)
		}
	}
	return c.w.Write(rec)
}

func (c *csvWriter) Close() error {
	c.w.Flush()
	if err := c.w.Error(); err != nil {
		return err
	}
	return c.buf.Flush()
}

// xlsxWriter — лист «Объекты» потоковой записью и лист «Источники» с атрибуцией.
type xlsxWriter struct {
	out io.Writer
	f   *excelize.File
	sw  *excelize.StreamWriter
	row int
}

const xlsxSheet = "Объекты"

func newXLSXWriter(w io.Writer) (*xlsxWriter, error) {
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", xlsxSheet); err != nil {
		return nil, err
	}
	sw, err := f.NewStreamWriter(xlsxSheet)
	if err != nil {
		return nil, err
	}
	widths := []float64{38, 40, 20, 50, 12, 24, 22, 14, 14, 14, 8, 12, 14, 12, 11, 11, 12, 30}
	for i, wd := range widths {
		if err := sw.SetColWidth(i+1, i+1, wd); err != nil {
			return nil, err
		}
	}
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return nil, err
	}
	head := make([]any, len(facilityHeader))
	for i, h := range facilityHeader {
		head[i] = excelize.Cell{Value: h, StyleID: bold}
	}
	if err := sw.SetRow("A1", head, excelize.RowOpts{Height: 30}); err != nil {
		return nil, err
	}
	return &xlsxWriter{out: w, f: f, sw: sw, row: 1}, nil
}

func (x *xlsxWriter) Write(it facility.Item) error {
	x.row++
	cell, err := excelize.CoordinatesToCellName(1, x.row)
	if err != nil {
		return err
	}
	return x.sw.SetRow(cell, cells(it))
}

func (x *xlsxWriter) Close() error {
	if err := x.sw.Flush(); err != nil {
		return err
	}
	if _, err := x.f.NewSheet("Источники"); err != nil {
		return err
	}
	if err := x.f.SetCellValue("Источники", "A1", facilityAttribution); err != nil {
		return err
	}
	if _, err := x.f.WriteTo(x.out); err != nil {
		return err
	}
	return x.f.Close()
}

// geoJSONWriter — FeatureCollection точек объектов; атрибуция — в поле attribution.
type geoJSONWriter struct {
	buf   *bufio.Writer
	first bool
}

func newGeoJSONWriter(w io.Writer) (*geoJSONWriter, error) {
	buf := bufio.NewWriter(w)
	attr, err := json.Marshal(facilityAttribution)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(buf, `{"type":"FeatureCollection","attribution":%s,"features":[`, attr); err != nil {
		return nil, err
	}
	return &geoJSONWriter{buf: buf, first: true}, nil
}

func (g *geoJSONWriter) Write(it facility.Item) error {
	feature := map[string]any{
		"type": "Feature",
		"id":   it.ID.String(),
		"geometry": map[string]any{
			"type": "Point", "coordinates": []float64{it.Lon, it.Lat},
		},
		"properties": map[string]any{
			"name": it.Name, "type": it.Type, "type_name": it.TypeName, "address": it.Address, "okrug": it.Okrug,
			"district": it.District, "ring": it.Ring, "building_area_m2": it.BuildingM2, "floor_area_m2": it.FloorM2,
			"territory_area_m2": it.TerritoryM2, "buildings": it.Buildings, "confidence": it.Confidence,
			"mkad_min_truck_20": it.MkadMin, "has_permit": it.HasPermit, "first_seen": it.FirstSeen.Format("2006-01-02"),
		},
	}
	b, err := json.Marshal(feature)
	if err != nil {
		return err
	}
	if !g.first {
		if err := g.buf.WriteByte(','); err != nil {
			return err
		}
	}
	g.first = false
	_, err = g.buf.Write(b)
	return err
}

func (g *geoJSONWriter) Close() error {
	if _, err := g.buf.WriteString("]}\n"); err != nil {
		return err
	}
	return g.buf.Flush()
}
