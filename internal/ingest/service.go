package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/ingest/sqlc"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/outbound"
)

const (
	defaultTimeout = 2 * time.Hour
	// maxInterval — самый редкий плановый запуск (секунды помещаются в integer таблицы).
	maxInterval = 366 * 24 * time.Hour
)

// Deps — зависимости сервиса загрузок.
type Deps struct {
	Pool  *pgxpool.Pool
	Blobs *blob.Store
	// Jobs ставит запуски в очередь; nil — только синхронные запуски (CLI).
	Jobs *jobs.Client
	Log  *slog.Logger
	// Secrets — значения обязательных настроек источников по именам переменных.
	Secrets map[string]string
	// WorkDir — каталог постоянных рабочих файлов источников; пусто — только временные.
	WorkDir string
	// RuCAFile — сертификат Минцифры для российских государственных источников.
	RuCAFile  string
	UserAgent string
	// AllowPrivateNetwork разрешает загрузчикам адреса локальной сети (только тесты).
	AllowPrivateNetwork bool
}

// Service управляет источниками и запусками.
type Service struct {
	d       Deps
	q       *sqlc.Queries
	sources map[string]Source
	order   []string
}

// NewService проверяет описания источников и создаёт сервис.
func NewService(d Deps, sources ...Source) (*Service, error) {
	s := &Service{d: d, q: sqlc.New(d.Pool), sources: map[string]Source{}}
	for _, src := range sources {
		desc := src.Descriptor()
		switch {
		case !codeRe.MatchString(desc.Code):
			return nil, fmt.Errorf("ingest: invalid source code %q", desc.Code)
		case s.sources[desc.Code] != nil:
			return nil, fmt.Errorf("ingest: duplicate source %q", desc.Code)
		case len(desc.Domains) == 0:
			return nil, fmt.Errorf("ingest: source %q publishes no domains", desc.Code)
		case desc.Interval != 0 && (desc.Interval < time.Minute || desc.Interval > maxInterval):
			return nil, fmt.Errorf("ingest: source %q interval is out of range", desc.Code)
		}
		s.sources[desc.Code] = src
		s.order = append(s.order, desc.Code)
	}
	slices.Sort(s.order)
	return s, nil
}

// Descriptors возвращает описания источников в порядке кодов.
func (s *Service) Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(s.order))
	for _, c := range s.order {
		out = append(out, s.sources[c].Descriptor())
	}
	return out
}

// Sync переносит описания источников из кода в реестр.
func (s *Service) Sync(ctx context.Context) error {
	return db.InTx(ctx, s.d.Pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		for _, d := range s.Descriptors() {
			var interval *int32
			if d.Interval > 0 {
				v := int32(d.Interval / time.Second) //nolint:gosec // ограничено maxInterval в NewService
				interval = &v
			}
			if err := q.UpsertSource(ctx, sqlc.UpsertSourceParams{
				Code: d.Code, Title: d.Title, Publisher: d.Publisher, Url: d.URL, License: d.License,
				Attribution: d.Attribution, Domains: d.Domains, IntervalS: interval, RuNetwork: d.RuNetwork,
			}); err != nil {
				return fmt.Errorf("sync source %s: %w", d.Code, err)
			}
		}
		if err := q.MarkUnregisteredSources(ctx, s.order); err != nil {
			return fmt.Errorf("mark unregistered sources: %w", err)
		}
		return nil
	})
}

// Enqueue создаёт запуск и ставит его в очередь в одной транзакции.
func (s *Service) Enqueue(ctx context.Context, code string, trigger Trigger, force bool, by *uuid.UUID) (Run, error) {
	src, ok := s.sources[code]
	if !ok {
		return Run{}, ErrUnknownSource
	}
	if s.d.Jobs == nil {
		return Run{}, errors.New("ingest: job queue is not configured")
	}
	var run sqlc.IngestRun
	err := db.InTx(ctx, s.d.Pool, func(tx pgx.Tx) error {
		r, err := s.createRun(ctx, s.q.WithTx(tx), code, trigger, force, by)
		if err != nil {
			return err
		}
		run = r
		queue := src.Descriptor().Queue
		if queue == "" {
			queue = jobs.QueueIngest
		}
		if _, err := s.d.Jobs.InsertTx(ctx, tx, RunArgs{RunID: r.ID}, &river.InsertOpts{Queue: queue}); err != nil {
			return fmt.Errorf("enqueue run: %w", err)
		}
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return toRun(run), nil
}

// RunNow выполняет загрузку в текущем процессе (командная строка).
func (s *Service) RunNow(ctx context.Context, code string, force bool) (Run, error) {
	if _, ok := s.sources[code]; !ok {
		return Run{}, ErrUnknownSource
	}
	r, err := s.createRun(ctx, s.q, code, TriggerCLI, force, nil)
	if err != nil {
		return Run{}, err
	}
	if err := s.Execute(ctx, r.ID); err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, r.ID)
}

func (s *Service) createRun(ctx context.Context, q *sqlc.Queries, code string, trigger Trigger, force bool, by *uuid.UUID) (sqlc.IngestRun, error) {
	r, err := q.CreateRun(ctx, sqlc.CreateRunParams{
		ID: ids.New(), SourceCode: code, Trigger: string(trigger), Forced: force, StartedBy: by,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.IngestRun{}, ErrRunActive
	}
	if err != nil {
		return sqlc.IngestRun{}, fmt.Errorf("create run: %w", err)
	}
	return r, nil
}

// Execute выполняет запуск. Ошибки загрузки записываются в запуск и не возвращаются:
// повтор задачи их не исправит. Возвращаются только ошибки записи состояния.
func (s *Service) Execute(ctx context.Context, runID uuid.UUID) (err error) {
	run, err := s.q.GetRun(ctx, runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRunNotFound
	}
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if run.Status != StatusQueued && run.Status != StatusRunning {
		return nil
	}
	rl := &runLog{q: s.q, id: run.ID, log: s.d.Log.With("source", run.SourceCode, "run", run.ID)}
	src, ok := s.sources[run.SourceCode]
	if !ok {
		return s.finish(ctx, run, StatusFailed, nil, nil, "источник не описан в текущей версии приложения")
	}
	desc := src.Descriptor()
	if err := s.q.StartRun(ctx, run.ID); err != nil {
		return fmt.Errorf("start run: %w", err)
	}

	timeout := desc.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if p := recover(); p != nil {
			err = s.finish(context.WithoutCancel(ctx), run, StatusFailed, nil, nil, fmt.Sprintf("внутренняя ошибка загрузчика: %v", p))
		}
	}()

	status, stats, snapID, ferr := s.execute(rctx, run, src, desc, rl)
	if ferr != nil {
		rl.Warn(ctx, ferr.Error())
		return s.finish(context.WithoutCancel(ctx), run, StatusFailed, stats, snapID, ferr.Error())
	}
	return s.finish(context.WithoutCancel(ctx), run, status, stats, snapID, "")
}

func (s *Service) execute(ctx context.Context, run sqlc.IngestRun, src Source, desc Descriptor, rl *runLog) (string, LoadStats, *uuid.UUID, error) {
	for _, name := range desc.Secrets {
		if s.d.Secrets[name] == "" {
			return "", nil, nil, fmt.Errorf("%w: %s", ErrMissingSecret, name)
		}
	}
	for _, tool := range desc.Tools {
		if _, err := exec.LookPath(tool); err != nil {
			return "", nil, nil, fmt.Errorf("%w: %s", ErrMissingTool, tool)
		}
	}
	tmp, err := os.MkdirTemp("", "ffv2-ingest-"+desc.Code+"-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	work := tmp
	if s.d.WorkDir != "" {
		work = filepath.Join(s.d.WorkDir, desc.Code)
		if err := os.MkdirAll(work, 0o750); err != nil {
			return "", nil, nil, fmt.Errorf("create work dir: %w", err)
		}
	}

	var client *outbound.Client
	if len(desc.Hosts) > 0 {
		cfg := outbound.Config{
			Hosts: desc.Hosts, Direct: desc.RuNetwork, UserAgent: s.d.UserAgent, AllowPrivate: s.d.AllowPrivateNetwork,
			Timeout: desc.RequestTimeout,
		}
		if desc.RuNetwork {
			cfg.RootCAFile = s.d.RuCAFile
		}
		if client, err = outbound.New(ctx, cfg); err != nil {
			return "", nil, nil, err
		}
	}

	prev, err := s.lastSnapshot(ctx, desc.Code)
	if err != nil {
		return "", nil, nil, err
	}
	dir := path.Join("raw", desc.Code, time.Now().Format("2006-01-02"))
	rl.Info(ctx, "получение данных")
	fetched, err := src.Fetch(ctx, FetchEnv{
		HTTP:     client,
		Previous: prev,
		Stored: func(ctx context.Context, version string) (*Snapshot, error) {
			return s.storedSnapshot(ctx, desc.Code, version)
		},
		Secret: func(name string) string { return s.d.Secrets[name] },
		Store: func(ctx context.Context, ext string, r io.Reader) (blob.Object, error) {
			return s.d.Blobs.Put(ctx, dir, ext, r)
		},
		TempDir: tmp,
		WorkDir: work,
		Log:     rl,
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("получение данных: %w", err)
	}
	if fetched.NotModified {
		rl.Info(ctx, "источник сообщает, что данные не изменились")
		return StatusUnchanged, nil, nil, nil
	}
	if prev != nil && fetched.Object.SHA256 == prev.SHA256 && !run.Forced {
		rl.Info(ctx, "снимок совпадает с последним загруженным")
		return StatusUnchanged, nil, &prev.ID, nil
	}

	validators, _ := json.Marshal(fetched.Validators)
	snap, err := s.q.SaveSnapshot(ctx, sqlc.SaveSnapshotParams{
		ID: ids.New(), SourceCode: desc.Code, Path: fetched.Object.Path, Sha256: fetched.Object.SHA256,
		SizeBytes: fetched.Object.Size, Format: fetched.Format, SourceVersion: nonEmpty(fetched.Version),
		RecordCount: fetched.RecordCount, Validators: validators,
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("save snapshot: %w", err)
	}
	rl.Info(ctx, fmt.Sprintf("снимок сохранён: %s, %d байт", snap.Sha256[:12], snap.SizeBytes))

	check := func(ctx context.Context, c Check) { s.saveCheck(ctx, run.ID, c, rl) }
	if fetched.Schema != nil {
		if err := s.trackSchema(ctx, desc.Code, snap.ID, fetched.Schema, check); err != nil {
			return "", nil, &snap.ID, err
		}
	}

	snapshot := toSnapshot(snap)
	rl.Info(ctx, "загрузка в базу")
	failed := false
	stats, err := src.Load(ctx, LoadEnv{
		Pool: s.d.Pool, Blobs: s.d.Blobs, Snapshot: snapshot, RunID: run.ID, TempDir: tmp, Log: rl,
		Check: func(ctx context.Context, c Check) {
			if !c.Passed && c.Severity == SeverityCritical {
				failed = true
			}
			check(ctx, c)
		},
	})
	if err != nil {
		return "", stats, &snap.ID, fmt.Errorf("загрузка: %w", err)
	}
	if failed {
		return "", stats, &snap.ID, ErrQualityFailed
	}

	if err := s.publish(ctx, run, desc, snap.ID, stats); err != nil {
		return "", stats, &snap.ID, err
	}
	rl.Info(ctx, "данные опубликованы: "+strings.Join(desc.Domains, ", "))
	return StatusSucceeded, stats, &snap.ID, nil
}

// publish отмечает снимок загруженным, повышает версии доменов и сообщает об этом.
func (s *Service) publish(ctx context.Context, run sqlc.IngestRun, desc Descriptor, snapID uuid.UUID, stats LoadStats) error {
	return db.InTx(ctx, s.d.Pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var records *int64
		if n, ok := stats["records"]; ok {
			records = &n
		}
		if err := q.MarkSnapshotLoaded(ctx, sqlc.MarkSnapshotLoadedParams{ID: snapID, RecordCount: records}); err != nil {
			return fmt.Errorf("mark snapshot loaded: %w", err)
		}
		versions := map[string]int64{}
		for _, d := range desc.Domains {
			v, err := q.BumpDataVersion(ctx, sqlc.BumpDataVersionParams{Domain: d, SourceCode: &desc.Code, RunID: &run.ID})
			if err != nil {
				return fmt.Errorf("bump data version %s: %w", d, err)
			}
			versions[d] = v
		}
		return events.Publish(ctx, tx, events.DataPublished, map[string]any{
			"source": desc.Code, "run": run.ID, "versions": versions,
		})
	})
}

func (s *Service) finish(ctx context.Context, run sqlc.IngestRun, status string, stats LoadStats, snapID *uuid.UUID, msg string) error {
	if stats == nil {
		stats = LoadStats{}
	}
	b, _ := json.Marshal(stats)
	var errText *string
	if msg != "" {
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		errText = &msg
	}
	if err := s.q.FinishRun(ctx, sqlc.FinishRunParams{ID: run.ID, Status: status, Stats: b, Error: errText, SnapshotID: snapID}); err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	if err := events.Publish(ctx, s.d.Pool, events.IngestRunFinished, map[string]any{
		"source": run.SourceCode, "run": run.ID, "status": status,
	}); err != nil {
		return err
	}
	s.d.Log.InfoContext(ctx, "ingest run finished", "source", run.SourceCode, "run", run.ID, "status", status, "error", msg)
	return nil
}

func (s *Service) lastSnapshot(ctx context.Context, code string) (*Snapshot, error) {
	snap, err := s.q.LastLoadedSnapshot(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("last snapshot: %w", err)
	}
	out := toSnapshot(snap)
	return &out, nil
}

func (s *Service) storedSnapshot(ctx context.Context, code, version string) (*Snapshot, error) {
	snap, err := s.q.FindSnapshotByVersion(ctx, sqlc.FindSnapshotByVersionParams{SourceCode: code, SourceVersion: &version})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find snapshot: %w", err)
	}
	p, err := s.d.Blobs.LocalPath(snap.Path)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(p); err != nil || st.Size() != snap.SizeBytes {
		return nil, nil //nolint:nilerr // файла нет или он неполный — снимок получается заново
	}
	out := toSnapshot(snap)
	return &out, nil
}

// trackSchema сохраняет схему набора и отмечает её изменение предупреждением.
func (s *Service) trackSchema(ctx context.Context, code string, snapID uuid.UUID, schema json.RawMessage, check func(context.Context, Check)) error {
	sum := sha256.Sum256(schema)
	hash := hex.EncodeToString(sum[:])
	last, err := s.q.LastSchema(ctx, code)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("last schema: %w", err)
	case last.Hash == hash:
		check(ctx, Check{Code: "schema_unchanged", Title: "Схема набора не изменилась", Severity: SeverityInfo, Passed: true})
		return nil
	default:
		check(ctx, Check{
			Code: "schema_changed", Title: "Схема набора изменилась", Severity: SeverityWarning, Passed: false,
			Message: "Проверьте загрузчик: поля набора отличаются от предыдущего снимка",
		})
	}
	if err := s.q.SaveSchema(ctx, sqlc.SaveSchemaParams{ID: ids.New(), SourceCode: code, SnapshotID: &snapID, Schema: schema, Hash: hash}); err != nil {
		return fmt.Errorf("save schema: %w", err)
	}
	return nil
}

func (s *Service) saveCheck(ctx context.Context, runID uuid.UUID, c Check, rl *runLog) {
	observed, _ := marshalOptional(c.Observed)
	expected, _ := marshalOptional(c.Expected)
	err := s.q.SaveCheck(ctx, sqlc.SaveCheckParams{
		ID: ids.New(), RunID: runID, Code: c.Code, Title: c.Title, Severity: string(c.Severity),
		Passed: c.Passed, Observed: observed, Expected: expected, Message: nonEmpty(c.Message),
	})
	if err != nil {
		s.d.Log.WarnContext(ctx, "save quality check", "run", runID, "check", c.Code, "error", err)
	}
	if !c.Passed {
		rl.Warn(ctx, "проверка не пройдена: "+c.Title)
	}
}

// FailStaleRuns завершает запуски, оставшиеся «в работе» после остановки процесса.
func (s *Service) FailStaleRuns(ctx context.Context) (int64, error) {
	maxTimeout := defaultTimeout
	for _, d := range s.Descriptors() {
		maxTimeout = max(maxTimeout, d.Timeout)
	}
	return s.q.FailStaleRuns(ctx, (maxTimeout + time.Hour).Seconds())
}

// runLog пишет шаги запуска в ingest.run.log и в журнал процесса.
type runLog struct {
	q   *sqlc.Queries
	id  uuid.UUID
	log *slog.Logger
}

func (l *runLog) Info(ctx context.Context, msg string) { l.write(ctx, "info", msg) }
func (l *runLog) Warn(ctx context.Context, msg string) { l.write(ctx, "warn", msg) }

func (l *runLog) write(ctx context.Context, level, msg string) {
	if level == "warn" {
		l.log.WarnContext(ctx, msg)
	} else {
		l.log.InfoContext(ctx, msg)
	}
	if err := l.q.AppendRunLog(context.WithoutCancel(ctx), sqlc.AppendRunLogParams{ID: l.id, Level: level, Message: msg}); err != nil {
		l.log.WarnContext(ctx, "append run log", "error", err)
	}
}

func marshalOptional(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
