package ingest_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"freightflow/internal/ingest"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/outbound"
)

// feed — тестовый источник: строки текста, по одной записи на строку.
type feed struct {
	url     string
	host    string
	secrets []string
	loads   atomic.Int32
}

func (f *feed) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code: "test_feed", Title: "Тестовый источник", Publisher: "тест", URL: f.url, License: "CC0",
		Attribution: "тест", Domains: []string{"test", "test_extra"}, Hosts: []string{f.host}, Secrets: f.secrets,
	}
}

func (f *feed) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	var v outbound.Validators
	if env.Previous != nil {
		v = env.Previous.Validators
	}
	resp, err := env.HTTP.Get(ctx, f.url+"/data", v)
	if errors.Is(err, outbound.ErrNotModified) {
		return ingest.Fetched{NotModified: true}, nil
	}
	if err != nil {
		return ingest.Fetched{}, err
	}
	defer resp.Body.Close()
	obj, err := env.Store(ctx, "txt", resp.Body)
	if err != nil {
		return ingest.Fetched{}, err
	}
	return ingest.Fetched{
		Object: obj, Format: "txt", Validators: resp.Validators,
		Schema: []byte(`{"fields":["` + resp.Header.Get("X-Schema") + `"]}`),
	}, nil
}

func (f *feed) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	f.loads.Add(1)
	r, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	lines := strings.Count(string(b), "\n")
	bad := strings.Contains(string(b), "bad")
	env.Check(ctx, ingest.Check{
		Code: "no_bad_rows", Title: "Нет испорченных строк", Severity: ingest.SeverityCritical,
		Passed: !bad, Observed: map[string]bool{"bad": bad},
	})
	if bad {
		return nil, ingest.ErrQualityFailed
	}
	return ingest.LoadStats{"records": int64(lines)}, nil
}

type server struct {
	mu     sync.Mutex
	body   string
	etag   string
	schema string
}

func (s *server) set(body, etag, schema string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.etag, s.schema = body, etag, schema
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.etag != "" && r.Header.Get("If-None-Match") == s.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if s.etag != "" {
		w.Header().Set("ETag", s.etag)
	}
	w.Header().Set("X-Schema", s.schema)
	_, _ = io.WriteString(w, s.body)
}

type env struct {
	pool *pgxpool.Pool
	svc  *ingest.Service
	src  *feed
	data *server
}

func setup(t *testing.T, client *jobs.Client, secrets map[string]string, required ...string) *env {
	t.Helper()
	return newEnv(t, dbtest.New(t), client, secrets, required...)
}

func newEnv(t *testing.T, pool *pgxpool.Pool, client *jobs.Client, secrets map[string]string, required ...string) *env {
	t.Helper()
	blobs, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	data := &server{body: "a\nb\n", schema: "name"}
	srv := httptest.NewServer(data)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	src := &feed{url: srv.URL, host: u.Hostname(), secrets: required}
	svc, err := ingest.NewService(ingest.Deps{
		Pool: pool, Blobs: blobs, Jobs: client, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Secrets: secrets, AllowPrivateNetwork: true,
	}, src)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, svc: svc, src: src, data: data}
}

func (e *env) version(t *testing.T, domain string) int64 {
	t.Helper()
	v, err := e.svc.DataVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v[domain]
}

func TestRunLifecycle(t *testing.T) {
	t.Parallel()
	e := setup(t, nil, nil)
	ctx := context.Background()

	run, err := e.svc.RunNow(ctx, "test_feed", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != ingest.StatusSucceeded || run.Stats["records"] != 2 || run.SnapshotID == nil {
		t.Fatalf("first run: %+v", run)
	}
	if e.version(t, "test") != 1 || e.version(t, "test_extra") != 1 {
		t.Fatal("data versions must be published")
	}

	// То же содержимое — без загрузки.
	run, err = e.svc.RunNow(ctx, "test_feed", false)
	if err != nil || run.Status != ingest.StatusUnchanged || e.src.loads.Load() != 1 {
		t.Fatalf("unchanged run: %+v loads=%d err=%v", run, e.src.loads.Load(), err)
	}

	// Принудительный запуск загружает тот же снимок заново.
	run, err = e.svc.RunNow(ctx, "test_feed", true)
	if err != nil || run.Status != ingest.StatusSucceeded || e.version(t, "test") != 2 {
		t.Fatalf("forced run: %+v err=%v", run, err)
	}

	// Источник отвечает 304 по ETag.
	e.data.set("a\nb\nc\n", `"v2"`, "name")
	if run, _ = e.svc.RunNow(ctx, "test_feed", false); run.Status != ingest.StatusSucceeded {
		t.Fatalf("new data: %+v", run)
	}
	if run, _ = e.svc.RunNow(ctx, "test_feed", false); run.Status != ingest.StatusUnchanged {
		t.Fatalf("not modified: %+v", run)
	}

	st, err := e.svc.Source(ctx, "test_feed")
	if err != nil || st.LastRun == nil || st.LastRun.Status != ingest.StatusUnchanged || st.LastSuccessAt == nil ||
		st.RecordCount == nil || *st.RecordCount != 3 {
		t.Fatalf("source status: %+v err=%v", st, err)
	}
	runs, err := e.svc.ListRuns(ctx, "test_feed", nil, 10)
	if err != nil || len(runs) != 5 {
		t.Fatalf("runs: %d err=%v", len(runs), err)
	}
	if len(runs[0].Log) == 0 {
		t.Fatal("run log is empty")
	}
}

func TestCriticalCheckFailsRun(t *testing.T) {
	t.Parallel()
	e := setup(t, nil, nil)
	ctx := context.Background()
	e.data.set("ok\nbad\n", "", "name")

	run, err := e.svc.RunNow(ctx, "test_feed", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != ingest.StatusFailed || !strings.Contains(run.Error, "critical quality check") {
		t.Fatalf("run: %+v", run)
	}
	if e.version(t, "test") != 0 {
		t.Fatal("failed run must not publish")
	}
	checks, err := e.svc.Checks(ctx, run.ID)
	if err != nil || len(checks) == 0 || checks[len(checks)-1].Passed {
		t.Fatalf("checks: %+v err=%v", checks, err)
	}
}

func TestSchemaChangeIsReported(t *testing.T) {
	t.Parallel()
	e := setup(t, nil, nil)
	ctx := context.Background()
	if _, err := e.svc.RunNow(ctx, "test_feed", false); err != nil {
		t.Fatal(err)
	}
	e.data.set("x\n", "", "name,area")
	run, err := e.svc.RunNow(ctx, "test_feed", false)
	if err != nil {
		t.Fatal(err)
	}
	checks, _ := e.svc.Checks(ctx, run.ID)
	found := false
	for _, c := range checks {
		if c.Code == "schema_changed" && !c.Passed && c.Severity == ingest.SeverityWarning {
			found = true
		}
	}
	if !found || run.Status != ingest.StatusSucceeded {
		t.Fatalf("schema change: status=%s checks=%+v", run.Status, checks)
	}
}

func TestMissingSecretFailsRun(t *testing.T) {
	t.Parallel()
	e := setup(t, nil, map[string]string{}, "FF_TEST_KEY")
	run, err := e.svc.RunNow(context.Background(), "test_feed", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != ingest.StatusFailed || !strings.Contains(run.Error, "FF_TEST_KEY") {
		t.Fatalf("run: %+v", run)
	}
}

func TestQueuedRunIsExecutedByWorker(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	inserter, err := jobs.NewInserter(pool, log)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, pool, inserter, nil)

	ctx := context.Background()
	run, err := e.svc.Enqueue(ctx, "test_feed", ingest.TriggerManual, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Enqueue(ctx, "test_feed", ingest.TriggerManual, false, nil); !errors.Is(err, ingest.ErrRunActive) {
		t.Fatalf("second enqueue: %v", err)
	}

	reg := jobs.NewRegistry()
	ingest.Register(reg, e.svc)
	worker, err := jobs.NewWorker(pool, log, reg, jobs.Observability{Meters: noop.NewMeterProvider(), Tracers: tracenoop.NewTracerProvider()})
	if err != nil {
		t.Fatal(err)
	}
	done, cancel := worker.Subscribe(river.EventKindJobCompleted)
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = worker.Stop(sctx)
	})
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev := <-done:
			if ev.Job.Kind != "ingest.run" {
				continue
			}
			got, err := e.svc.GetRun(ctx, run.ID)
			if err != nil || got.Status != ingest.StatusSucceeded {
				t.Fatalf("run after worker: %+v err=%v", got, err)
			}
			return
		case <-deadline:
			t.Fatal("run was not executed")
		}
	}
}
