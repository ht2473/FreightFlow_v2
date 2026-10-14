package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"freightflow/internal/facility"
	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/jobs"
)

func TestFacilityExports(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'export@example.com', 'Выгрузка')`, user); err != nil {
		t.Fatal(err)
	}
	client, err := jobs.NewInserter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	blobs, err := blob.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	svc := New(pool, client, blobs, facility.New(pool))

	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM infra.facility WHERE status = 'active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	read := func(format string) []byte {
		t.Helper()
		e, err := svc.CreateFacilities(ctx, user, format, FacilityParams{Sort: "name"})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := svc.File(ctx, user, e.ID); !errors.Is(err, ErrNotReady) {
			t.Fatalf("%s before run: %v", format, err)
		}
		if err := svc.run(ctx, e.ID); err != nil {
			t.Fatal(err)
		}
		done, err := svc.Get(ctx, user, e.ID)
		if err != nil || done.Status != "done" || done.Rows == nil || *done.Rows != active {
			t.Fatalf("%s export %+v, %v", format, done, err)
		}
		f, name, _, size, err := svc.File(ctx, user, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil || int64(len(b)) != size || filepath.Ext(name) != "."+format {
			t.Fatalf("%s file %s: %d bytes of %d, %v", format, name, len(b), size, err)
		}
		return b
	}

	// CSV: метка UTF-8, заголовок, строка на объект, колонка источников.
	b := read(FormatCSV)
	if !bytes.HasPrefix(b, utf8BOM) {
		t.Error("csv without BOM")
	}
	recs, err := csv.NewReader(bytes.NewReader(b[len(utf8BOM):])).ReadAll()
	if err != nil || len(recs) != active+1 || recs[0][1] != "Название" || recs[1][len(recs[1])-1] != facilityAttribution {
		t.Fatalf("csv: %d records, %v", len(recs), err)
	}

	// XLSX: лист объектов и лист источников.
	x, err := excelize.OpenReader(bytes.NewReader(read(FormatXLSX)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := x.GetRows(xlsxSheet)
	if err != nil || len(rows) != active+1 {
		t.Fatalf("xlsx: %d rows, %v", len(rows), err)
	}
	if v, _ := x.GetCellValue("Источники", "A1"); v != facilityAttribution {
		t.Errorf("xlsx attribution %q", v)
	}

	// GeoJSON: точки и атрибуция.
	var fc struct {
		Attribution string
		Features    []struct {
			Geometry struct {
				Type        string
				Coordinates []float64
			}
		}
	}
	if err := json.Unmarshal(read(FormatGeoJSON), &fc); err != nil || len(fc.Features) != active ||
		fc.Attribution != facilityAttribution || fc.Features[0].Geometry.Type != "Point" {
		t.Fatalf("geojson: %d features, %v", len(fc.Features), err)
	}

	// Чужая выгрузка не видна; просроченные удаляются с файлами.
	list, err := svc.List(ctx, user, 10)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %d, %v", len(list), err)
	}
	if _, err := svc.Get(ctx, uuid.New(), list[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("other user: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE report.export SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	n, err := svc.cleanup(ctx)
	if err != nil || n != 3 {
		t.Fatalf("cleanup: %d, %v", n, err)
	}
	files, _ := os.ReadDir(filepath.Join(dir, "exports"))
	if len(files) != 0 {
		t.Errorf("files left: %d", len(files))
	}
}
