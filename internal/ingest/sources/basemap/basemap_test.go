package basemap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/blob"
)

type nopLog struct{}

func (nopLog) Info(context.Context, string) {}
func (nopLog) Warn(context.Context, string) {}

// Фрагмент z12–14 вокруг Кремля не проходит проверку масштабов: подложка остаётся прежней.
func TestLoadRejectsIncompleteArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blobs, err := blob.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()
	f, err := os.Open(filepath.Join("..", "..", "..", "platform", "pmtiles", "testdata", "kremlin.pmtiles"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	obj, err := blobs.Put(t.Context(), "raw/basemap/test", "pmtiles", f)
	if err != nil {
		t.Fatal(err)
	}

	var checks []ingest.Check
	env := ingest.LoadEnv{
		Blobs:    blobs,
		Snapshot: ingest.Snapshot{Path: obj.Path, SHA256: obj.SHA256, Size: obj.Size, Version: "test.pmtiles"},
		Log:      nopLog{},
		Check:    func(_ context.Context, c ingest.Check) { checks = append(checks, c) },
	}
	_, err = New(Config{DataDir: dir}).Load(t.Context(), env)
	if !errors.Is(err, ingest.ErrQualityFailed) {
		t.Fatalf("want quality failure, got %v", err)
	}
	passed := map[string]bool{}
	for _, c := range checks {
		passed[c.Code] = c.Passed
	}
	if !passed["tile_type"] || !passed["center"] || passed["zoom"] {
		t.Fatalf("checks: %+v", passed)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(PointerFile))); !os.IsNotExist(err) {
		t.Fatalf("pointer must not be written: %v", err)
	}
}

func TestWritePointerReplacesFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "basemap", "current.json")
	for _, v := range []string{"a", "b"} {
		if err := writePointer(path, Ref{Path: "raw/" + v, Version: v}); err != nil {
			t.Fatal(err)
		}
	}
	var ref Ref
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &ref); err != nil || ref.Version != "b" {
		t.Fatalf("pointer %s: %v", b, err)
	}
}
