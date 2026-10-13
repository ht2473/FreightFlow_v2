package mosdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"freightflow/internal/platform/outbound"
)

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	hc, err := outbound.New(t.Context(), outbound.Config{Hosts: []string{u.Hostname()}, Direct: true, AllowPrivate: true, Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(hc, "test-key")
	c.base = srv.URL + "/v1"
	c.Pause = 0
	c.retryPauses = []time.Duration{time.Millisecond, time.Millisecond}
	return c
}

// featurePage — страница выдачи /features из записей набора 62101 (testdata) с номерами ids.
func featurePage(t *testing.T, recs []Record, ids []int64) []byte {
	t.Helper()
	type props struct {
		DatasetID  int             `json:"datasetId"`
		RowID      int64           `json:"rowId"`
		Attributes json.RawMessage `json:"attributes"`
	}
	type feat struct {
		Geometry   json.RawMessage `json:"geometry"`
		Properties props           `json:"properties"`
		Type       string          `json:"type"`
	}
	fc := struct {
		Features []feat `json:"features"`
		Type     string `json:"type"`
	}{Type: "FeatureCollection"}
	for _, id := range ids {
		r := recs[int(id)%len(recs)]
		fc.Features = append(fc.Features, feat{
			Geometry: r.Geometry, Type: "Feature",
			Properties: props{DatasetID: 62101, RowID: id, Attributes: setCells(t, r.Cells, map[string]any{"global_id": id})},
		})
	}
	b, err := json.Marshal(fc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEachFeaturePageUsesTotal(t *testing.T) {
	t.Parallel()
	const total = 2300
	recs := readRecords(t, "testdata/works.jsonl.gz")
	pages := map[int][]byte{}
	for skip := 0; skip < total; skip += 1000 {
		var ids []int64
		for i := skip; i < min(skip+1000, total); i++ {
			// Запись без геометрии /features не отдаёт: первая страница неполная.
			if i == 500 {
				continue
			}
			ids = append(ids, int64(i+1))
		}
		pages[skip] = featurePage(t, recs, ids)
	}
	var calls, failed atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/v1/datasets/62101/features" || q.Get("api_key") != "test-key" || q.Get("$top") != "1000" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		skip, _ := strconv.Atoi(q.Get("$skip"))
		// Вторая страница сначала отвечает 500, затем 502 — портал так делает на больших наборах.
		if skip == 1000 {
			switch failed.Add(1) {
			case 1:
				w.WriteHeader(http.StatusInternalServerError)
				return
			case 2:
				w.WriteHeader(http.StatusBadGateway)
				return
			}
		}
		_, _ = w.Write(pages[skip])
	}))
	defer srv.Close()

	var got []Record
	var nexts []int
	err := testClient(t, srv).EachFeaturePage(t.Context(), 62101, total, 0, func(recs []Record, next int) error {
		got = append(got, recs...)
		nexts = append(nexts, next)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Вторая страница: 500 — повтор страницы, 502 — повтор самого клиента, затем ответ.
	if len(got) != total-1 || calls.Load() != 5 {
		t.Fatalf("records %d, calls %d; want %d records, 5 calls", len(got), calls.Load(), total-1)
	}
	if len(nexts) != 3 || nexts[2] != 3000 {
		t.Errorf("next offsets: %v", nexts)
	}
	if got[0].ID != 1 || len(got[0].Geometry) == 0 {
		t.Errorf("first record: id %d, geometry %d bytes", got[0].ID, len(got[0].Geometry))
	}
	var cells map[string]any
	if err := json.Unmarshal(got[0].Cells, &cells); err != nil || cells["WorksType"] == nil {
		t.Errorf("cells: %v %v", cells, err)
	}
}

func TestRowsFieldsProjection(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields []string
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || json.Unmarshal(body, &fields) != nil || len(fields) != 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		cells := map[string]any{}
		for _, f := range fields {
			cells[f] = "v"
		}
		b, _ := json.Marshal([]map[string]any{{"global_id": 7, "Number": 1, "Cells": cells}})
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	var got []Record
	err := testClient(t, srv).EachRowPage(t.Context(), 3247, []string{"Zone", "BeginDate"}, 0, 0, func(recs []Record, _ int) error {
		got = append(got, recs...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 7 || string(got[0].Cells) != `{"BeginDate":"v","Zone":"v"}` {
		t.Fatalf("rows: %+v", got)
	}
}

func TestPageErrorNotRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	err := testClient(t, srv).EachFeaturePage(t.Context(), 62101, 10, 0, func([]Record, int) error { return nil })
	if outbound.StatusCode(err) != http.StatusBadRequest || calls.Load() != 1 {
		t.Fatalf("err %v, calls %d", err, calls.Load())
	}
}

type nopLog struct{}

func (nopLog) Info(context.Context, string) {}
func (nopLog) Warn(context.Context, string) {}

func TestDownloadResumesAfterFailure(t *testing.T) {
	t.Parallel()
	const total = 2500
	recs := readRecords(t, "testdata/works.jsonl.gz")
	pages := map[int][]byte{}
	for skip := 0; skip < total; skip += 1000 {
		var ids []int64
		for i := skip; i < min(skip+1000, total); i++ {
			ids = append(ids, int64(i+1))
		}
		pages[skip] = featurePage(t, recs, ids)
	}
	var mu sync.Mutex
	asked := []int{}
	broken := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skip, _ := strconv.Atoi(r.URL.Query().Get("$skip"))
		mu.Lock()
		asked = append(asked, skip)
		fail := broken && skip == 2000
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(pages[skip])
	}))
	defer srv.Close()

	s := New(Dataset{ID: 62101, Geo: true})
	c := testClient(t, srv)
	dir := t.TempDir()
	if _, _, err := s.download(t.Context(), c, dir, "1.2", total, nopLog{}); err == nil {
		t.Fatal("download did not fail")
	}
	mu.Lock()
	broken = false
	mu.Unlock()
	path, n, err := s.download(t.Context(), c, dir, "1.2", total, nopLog{})
	if err != nil {
		t.Fatal(err)
	}
	// Вторая попытка начинается с третьей страницы: первые две уже сохранены.
	if n != total || fmt.Sprint(asked) != "[0 1000 2000 2000]" {
		t.Fatalf("records %d, requested pages %v", n, asked)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if seen[r.ID] {
			t.Fatalf("record %d twice", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != total {
		t.Fatalf("file has %d records", len(seen))
	}

	// Другая версия набора начинается заново, а старые файлы удаляются.
	if _, _, err := s.download(t.Context(), c, dir, "1.3", total, nopLog{}); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "62101-1.2*")); len(files) != 0 {
		t.Errorf("stale files: %v", files)
	}
}
