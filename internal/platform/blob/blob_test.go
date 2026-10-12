package blob

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPutDeduplicatesByContent(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	a, err := s.Put(ctx, "raw/mosdata/2026-10-12", "json", strings.NewReader(`{"rows":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Put(ctx, "raw/mosdata/2026-10-12", "json", strings.NewReader(`{"rows":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a.Size != 10 || !strings.HasSuffix(a.Path, a.SHA256+".json") {
		t.Fatalf("objects differ: %+v %+v", a, b)
	}

	f, err := s.Open(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	_ = f.Close()
	if string(data) != `{"rows":1}` {
		t.Fatalf("content: %s", data)
	}

	local, err := s.LocalPath(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatalf("local path: %v", err)
	}
	tmp, _ := os.ReadDir(filepath.Join(s.dir, tmpDir))
	if len(tmp) != 0 {
		t.Fatalf("temp files left: %d", len(tmp))
	}
}

func TestRejectsPathsOutsideStore(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, dir := range []string{"../x", "/abs", "a/../../b", "", "tmp"} {
		if _, err := s.Put(context.Background(), dir, "bin", strings.NewReader("x")); err == nil {
			t.Errorf("dir %q accepted", dir)
		}
	}
	if _, err := s.Put(context.Background(), "raw", "../exe", strings.NewReader("x")); err == nil {
		t.Error("bad extension accepted")
	}
}

func TestPutStopsOnCanceledContext(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Put(ctx, "raw", "bin", strings.NewReader("data")); err == nil {
		t.Fatal("canceled put succeeded")
	}
}
