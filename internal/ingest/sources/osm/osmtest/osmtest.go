// Package osmtest загружает в тестовую базу фрагмент OSM Нижегородского района (testdata
// источника osm) и переносит его в домен: объекты реестра, сеть, границы, точки.
package osmtest

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/etl"
	"freightflow/internal/platform/db"
)

// LoadDistrict выполняет стадию фрагмента и transform.sql источника osm.
func LoadDistrict(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(file), "..", "testdata", "stage.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(stage)); err != nil {
		t.Fatal(err)
	}
	script, err := etl.FS.ReadFile("osm/transform.sql")
	if err != nil {
		t.Fatal(err)
	}
	err = db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), `SELECT set_config('ff.source', 'osm', true), set_config('ff.snapshot', $1, true)`,
			uuid.NewString()); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), string(script))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
