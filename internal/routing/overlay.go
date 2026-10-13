// Package routing — маршрутизация грузовиков: граф Valhalla с ограничениями из дорожных знаков,
// профили ТС, политика зон, маршруты, изохроны, матрицы, доступность объектов.
package routing

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// minOverlayConfidence — в граф попадают знаки, привязанные однозначно (неоднозначная привязка —
// 0,4).
const minOverlayConfidence = 0.6

// overlayTag — тег OSM, которым ограничение из знака переносится в граф. Только однозначные
// случаи: без условий по времени (8.5.x) и без неизвестного порога массы (8.11), значения
// габаритов в реестре знаков нет — такие ограничения остаются из OSM.
func overlayTag(kind string, condition *string) (key, value string, ok bool) {
	cond := ""
	if condition != nil {
		cond = *condition
	}
	switch {
	case kind == "hgv_ban" && cond == "":
		return "hgv", "no", true
	case kind == "hgv_ban" && cond == "въезд":
		return "hgv", "destination", true
	case kind == "motor_vehicle_ban" && cond == "":
		return "motor_vehicle", "no", true
	case kind == "hazmat_ban" && cond == "":
		return "hazmat", "no", true
	}
	return "", "", false
}

// Overlay — теги, добавляемые линиям OSM: идентификатор линии → ключ → значение.
type Overlay map[int64]map[string]string

// SignOverlay выбирает ограничения из знаков, которых нет в разметке OSM того же участка.
func SignOverlay(ctx context.Context, pool *pgxpool.Pool) (Overlay, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.osm_id, r.kind, r.condition
		FROM network.restriction r
		JOIN network.road_segment s ON s.id = r.segment_id
		WHERE r.origin = 'sign' AND r.confidence >= $1
		  AND NOT EXISTS (SELECT 1 FROM network.restriction o
		                  WHERE o.segment_id = r.segment_id AND o.kind = r.kind AND o.origin IN ('osm', 'manual'))
		ORDER BY s.osm_id, r.kind`, minOverlayConfidence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := Overlay{}
	for rows.Next() {
		var id int64
		var kind string
		var cond *string
		if err := rows.Scan(&id, &kind, &cond); err != nil {
			return nil, err
		}
		k, v, ok := overlayTag(kind, cond)
		if !ok {
			continue
		}
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		// Запрет сильнее «только для подъезда».
		if prev, ok := out[id][k]; ok && prev == "no" {
			continue
		}
		out[id][k] = v
	}
	return out, rows.Err()
}

// osmWay — линия в OSM XML (выдача osmium getid).
type osmWay struct {
	ID      int64   `xml:"id,attr"`
	Version int     `xml:"version,attr"`
	Nodes   []nd    `xml:"nd"`
	Tags    []tagKV `xml:"tag"`
}

type nd struct {
	Ref int64 `xml:"ref,attr"`
}

type tagKV struct {
	K string `xml:"k,attr"`
	V string `xml:"v,attr"`
}

// WriteChange читает линии OSM XML и пишет osmChange: линии из overlay с добавленными тегами и
// версией на единицу больше (apply-changes оставляет более новую версию). Возвращает число
// изменённых линий.
func WriteChange(src io.Reader, overlay Overlay, w io.Writer, now time.Time) (int, error) {
	dec := xml.NewDecoder(src)
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<osmChange version="0.6" generator="freightflow">` + "\n<modify>\n"); err != nil {
		return 0, err
	}
	ts := now.UTC().Format(time.RFC3339)
	n := 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("osm xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "way" {
			continue
		}
		var way osmWay
		if err := dec.DecodeElement(&way, &se); err != nil {
			return n, fmt.Errorf("osm way: %w", err)
		}
		add, ok := overlay[way.ID]
		if !ok {
			continue
		}
		tags := map[string]string{}
		for _, t := range way.Tags {
			tags[t.K] = t.V
		}
		for k, v := range add {
			tags[k] = v
		}
		if err := writeWay(bw, way, tags, ts); err != nil {
			return n, err
		}
		n++
	}
	if _, err := bw.WriteString("</modify>\n</osmChange>\n"); err != nil {
		return n, err
	}
	return n, bw.Flush()
}

// attrEscaper экранирует значение атрибута XML.
var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
	"\n", "&#xA;", "\r", "&#xD;", "\t", "&#x9;")

func writeWay(w io.Writer, way osmWay, tags map[string]string, ts string) error {
	var b strings.Builder
	fmt.Fprintf(&b, `  <way id="%d" version="%d" timestamp="%s">`+"\n", way.ID, way.Version+1, ts)
	for _, n := range way.Nodes {
		fmt.Fprintf(&b, `    <nd ref="%d"/>`+"\n", n.Ref)
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, `    <tag k="%s" v="%s"/>`+"\n", attrEscaper.Replace(k), attrEscaper.Replace(tags[k]))
	}
	b.WriteString("  </way>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// PrepareResult — итог подготовки выгрузки для графа.
type PrepareResult struct {
	Ways   int
	Output string
}

// Prepare накладывает ограничения из знаков на выгрузку маршрутизации: osmium getid выбирает
// линии, WriteChange добавляет теги, osmium apply-changes пишет новую выгрузку out.
func Prepare(ctx context.Context, pool *pgxpool.Pool, pbf, dir, out string) (PrepareResult, error) {
	overlay, err := SignOverlay(ctx, pool)
	if err != nil {
		return PrepareResult{}, fmt.Errorf("sign overlay: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return PrepareResult{}, err
	}
	ids := make([]string, 0, len(overlay))
	for id := range overlay {
		ids = append(ids, "w"+strconv.FormatInt(id, 10))
	}
	sort.Strings(ids)
	idFile := filepath.Join(dir, "ways.txt")
	if err := os.WriteFile(idFile, []byte(strings.Join(ids, "\n")+"\n"), 0o600); err != nil {
		return PrepareResult{}, err
	}
	waysXML := filepath.Join(dir, "ways.osm")
	if err := run(ctx, "osmium", "getid", "--overwrite", "-i", idFile, "-o", waysXML, pbf); err != nil {
		return PrepareResult{}, err
	}
	src, err := os.Open(waysXML)
	if err != nil {
		return PrepareResult{}, err
	}
	defer src.Close()
	changeFile := filepath.Join(dir, "signs.osc")
	dst, err := os.Create(changeFile)
	if err != nil {
		return PrepareResult{}, err
	}
	n, err := WriteChange(src, overlay, dst, time.Now())
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return PrepareResult{}, err
	}
	if err := run(ctx, "osmium", "apply-changes", "--overwrite", "-o", out, pbf, changeFile); err != nil {
		return PrepareResult{}, err
	}
	return PrepareResult{Ways: n, Output: out}, nil
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, args[0], err, strings.TrimSpace(string(b)))
	}
	return nil
}
