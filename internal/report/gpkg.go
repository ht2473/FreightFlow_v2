package report

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"freightflow/internal/facility"
)

// FormatGPKG — GeoPackage (SQLite с геометрией): слой objects и таблица sources.
const FormatGPKG = "gpkg"

// writeGPKG пишет выборку в GeoPackage через ogr2ogr (GDAL): точки объектов слоем objects,
// атрибуция и дата данных — таблицей sources. Возвращает путь файла во временном каталоге dir.
func (s *Service) writeGPKG(ctx context.Context, dir string, p FacilityParams, attr string, dataDate time.Time) (string, int, error) {
	in := filepath.Join(dir, "objects.geojson")
	f, err := os.Create(in)
	if err != nil {
		return "", 0, err
	}
	rows := 0
	w, err := newGeoJSONWriter(f, attr)
	if err == nil {
		err = s.facilities.Each(ctx, p.Filter, p.Sort, func(it facility.Item) error {
			rows++
			return w.Write(it)
		})
	}
	if err == nil {
		err = w.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	out := filepath.Join(dir, "objects.gpkg")
	if err := ogr2ogr(ctx, "-f", "GPKG", out, in, "-nln", "objects", "-a_srs", "EPSG:4326",
		"-lco", "SPATIAL_INDEX=YES", "-lco", "IDENTIFIER=Объекты логистики", "-lco", "DESCRIPTION="+attr); err != nil {
		return "", 0, err
	}
	src := filepath.Join(dir, "sources.csv")
	sf, err := os.Create(src)
	if err != nil {
		return "", 0, err
	}
	cw := csv.NewWriter(sf)
	date := ""
	if !dataDate.IsZero() {
		date = dataDate.Format("2006-01-02")
	}
	_ = cw.Write([]string{"attribution", "data_date", "exported_at"})
	_ = cw.Write([]string{attr, date, time.Now().UTC().Format(time.RFC3339)})
	cw.Flush()
	if err := cw.Error(); err != nil {
		_ = sf.Close()
		return "", 0, err
	}
	if err := sf.Close(); err != nil {
		return "", 0, err
	}
	if err := ogr2ogr(ctx, "-update", "-f", "GPKG", out, src, "-nln", "sources"); err != nil {
		return "", 0, err
	}
	return out, rows, nil
}

func ogr2ogr(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "ogr2ogr", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ogr2ogr: %w: %s", err, b)
	}
	return nil
}
