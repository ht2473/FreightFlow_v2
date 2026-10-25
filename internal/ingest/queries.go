package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/ingest/sqlc"
	"freightflow/internal/platform/outbound"
)

// Run — запуск загрузки.
type Run struct {
	ID         uuid.UUID
	Source     string
	Status     string
	Trigger    string
	Forced     bool
	StartedBy  *uuid.UUID
	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	SnapshotID *uuid.UUID
	Stats      LoadStats
	Error      string
	Log        []LogEntry
}

// LogEntry — строка журнала запуска.
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// CheckResult — сохранённый результат проверки качества.
type CheckResult struct {
	Check
	CheckedAt time.Time
}

// SourceStatus — источник с последним запуском и свежестью данных.
type SourceStatus struct {
	Descriptor
	Enabled       bool
	LastRun       *Run
	LastSuccessAt *time.Time
	RecordCount   *int64
}

// ListSources возвращает зарегистрированные источники с последним запуском.
func (s *Service) ListSources(ctx context.Context) ([]SourceStatus, error) {
	rows, err := s.q.ListSourcesWithLastRun(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	out := make([]SourceStatus, 0, len(rows))
	for _, r := range rows {
		src, ok := s.sources[r.Code]
		if !ok {
			continue
		}
		st := SourceStatus{Descriptor: src.Descriptor(), Enabled: r.Enabled, LastSuccessAt: r.LastSuccessAt, RecordCount: r.RecordCount}
		if len(r.LastRun) > 0 && string(r.LastRun) != "null" {
			var lr struct {
				ID         uuid.UUID  `json:"id"`
				Status     string     `json:"status"`
				Trigger    string     `json:"trigger"`
				QueuedAt   time.Time  `json:"queued_at"`
				StartedAt  *time.Time `json:"started_at"`
				FinishedAt *time.Time `json:"finished_at"`
				Error      *string    `json:"error"`
			}
			if err := json.Unmarshal(r.LastRun, &lr); err != nil {
				return nil, fmt.Errorf("decode last run: %w", err)
			}
			run := &Run{
				ID: lr.ID, Source: r.Code, Status: lr.Status, Trigger: lr.Trigger, QueuedAt: lr.QueuedAt,
				StartedAt: lr.StartedAt, FinishedAt: lr.FinishedAt,
			}
			if lr.Error != nil {
				run.Error = *lr.Error
			}
			st.LastRun = run
		}
		out = append(out, st)
	}
	return out, nil
}

// Source возвращает источник с последним запуском.
func (s *Service) Source(ctx context.Context, code string) (SourceStatus, error) {
	all, err := s.ListSources(ctx)
	if err != nil {
		return SourceStatus{}, err
	}
	for _, st := range all {
		if st.Code == code {
			return st, nil
		}
	}
	return SourceStatus{}, ErrUnknownSource
}

// SetEnabled включает или выключает автоматическую загрузку источника.
func (s *Service) SetEnabled(ctx context.Context, code string, enabled bool) (SourceStatus, error) {
	if _, ok := s.sources[code]; !ok {
		return SourceStatus{}, ErrUnknownSource
	}
	if _, err := s.q.SetSourceEnabled(ctx, sqlc.SetSourceEnabledParams{Code: code, Enabled: enabled}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SourceStatus{}, ErrUnknownSource
		}
		return SourceStatus{}, fmt.Errorf("set source enabled: %w", err)
	}
	return s.Source(ctx, code)
}

// ListRuns возвращает запуски источника от новых к старым.
func (s *Service) ListRuns(ctx context.Context, code string, before *time.Time, limit int) ([]Run, error) {
	rows, err := s.q.ListRuns(ctx, sqlc.ListRunsParams{SourceCode: code, Before: before, Lim: int32(limit)}) //nolint:gosec // limit ограничен вызывающим
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = toRun(r)
	}
	return out, nil
}

// GetRun возвращает запуск.
func (s *Service) GetRun(ctx context.Context, id uuid.UUID) (Run, error) {
	r, err := s.q.GetRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	return toRun(r), nil
}

// Checks возвращает проверки качества запуска.
func (s *Service) Checks(ctx context.Context, runID uuid.UUID) ([]CheckResult, error) {
	rows, err := s.q.ListChecks(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}
	out := make([]CheckResult, len(rows))
	for i, r := range rows {
		c := CheckResult{CheckedAt: r.CheckedAt, Check: Check{
			Code: r.Code, Title: r.Title, Severity: Severity(r.Severity), Passed: r.Passed,
		}}
		if r.Message != nil {
			c.Message = *r.Message
		}
		if len(r.Observed) > 0 {
			c.Observed = json.RawMessage(r.Observed)
		}
		if len(r.Expected) > 0 {
			c.Expected = json.RawMessage(r.Expected)
		}
		out[i] = c
	}
	return out, nil
}

// DataVersions возвращает версии опубликованных данных по доменам.
func (s *Service) DataVersions(ctx context.Context) (map[string]int64, error) {
	rows, err := s.q.ListDataVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list data versions: %w", err)
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Domain] = r.Version
	}
	return out, nil
}

func toRun(r sqlc.IngestRun) Run {
	run := Run{
		ID: r.ID, Source: r.SourceCode, Status: r.Status, Trigger: r.Trigger, Forced: r.Forced,
		StartedBy: r.StartedBy, QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		SnapshotID: r.SnapshotID, Stats: LoadStats{},
	}
	if r.Error != nil {
		run.Error = *r.Error
	}
	_ = json.Unmarshal(r.Stats, &run.Stats)
	_ = json.Unmarshal(r.Log, &run.Log)
	return run
}

func toSnapshot(r sqlc.IngestSnapshot) Snapshot {
	s := Snapshot{
		ID: r.ID, Path: r.Path, SHA256: r.Sha256, Size: r.SizeBytes, Format: r.Format,
		RecordCount: r.RecordCount, FetchedAt: r.FetchedAt,
	}
	if r.SourceVersion != nil {
		s.Version = *r.SourceVersion
	}
	var v outbound.Validators
	if json.Unmarshal(r.Validators, &v) == nil {
		s.Validators = v
	}
	return s
}

// Update — опубликованное обновление данных: источник, время, число записей, домены.
type Update struct {
	Source     string
	Title      string
	Publisher  string
	FinishedAt time.Time
	Records    *int64
	Domains    []string
}

// RecentUpdates — последние успешные загрузки (лента обновлений на дашборде).
func (s *Service) RecentUpdates(ctx context.Context, limit int) ([]Update, error) {
	rows, err := s.d.Pool.Query(ctx, `
		SELECT r.source_code, s.title, s.publisher, r.finished_at, (r.stats ->> 'records')::bigint, s.domains
		FROM ingest.run r JOIN ingest.source s ON s.code = r.source_code
		WHERE r.status = 'succeeded' AND r.finished_at IS NOT NULL
		ORDER BY r.finished_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent updates: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Update, error) {
		var u Update
		return u, r.Scan(&u.Source, &u.Title, &u.Publisher, &u.FinishedAt, &u.Records, &u.Domains)
	})
}
