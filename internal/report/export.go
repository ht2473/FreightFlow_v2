// Package report — отчёты и выгрузки данных. Выгрузка — задание: запрос сохраняется,
// файл формирует фоновая задача, результат хранится в хранилище файлов неделю.
package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/facility"
	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/jobs"
)

// ErrNotFound — выгрузки нет или она чужая.
var ErrNotFound = errors.New("report: export not found")

// ErrNotReady — файл выгрузки ещё не готов или формирование не удалось.
var ErrNotReady = errors.New("report: export not ready")

// Форматы выгрузки.
const (
	FormatCSV     = "csv"
	FormatXLSX    = "xlsx"
	FormatGeoJSON = "geojson"
)

// Export — задание выгрузки.
type Export struct {
	ID         uuid.UUID
	Kind       string
	Format     string
	Status     string // queued, running, done, failed
	Rows       *int
	FileSize   *int64
	Error      *string
	CreatedAt  time.Time
	FinishedAt *time.Time
	ExpiresAt  time.Time
}

// FacilityParams — выборка реестра для выгрузки.
type FacilityParams struct {
	Filter facility.Filter `json:"filter"`
	Sort   string          `json:"sort"`
}

// Service — выгрузки данных.
type Service struct {
	pool       *pgxpool.Pool
	jobs       *jobs.Client
	blobs      *blob.Store
	facilities *facility.Service
}

// New создаёт сервис.
func New(pool *pgxpool.Pool, client *jobs.Client, blobs *blob.Store, facilities *facility.Service) *Service {
	return &Service{pool: pool, jobs: client, blobs: blobs, facilities: facilities}
}

const exportColumns = `id, kind, format, status, rows, file_size, error, created_at, finished_at, expires_at`

func scanExport(r pgx.Row) (*Export, error) {
	var e Export
	err := r.Scan(&e.ID, &e.Kind, &e.Format, &e.Status, &e.Rows, &e.FileSize, &e.Error, &e.CreatedAt, &e.FinishedAt, &e.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &e, err
}

// CreateFacilities ставит выгрузку выборки реестра.
func (s *Service) CreateFacilities(ctx context.Context, userID uuid.UUID, format string, p FacilityParams) (*Export, error) {
	params, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var e *Export
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO report.export (id, user_id, kind, format, params)
			VALUES ($1, $2, 'facilities', $3, $4) RETURNING `+exportColumns, ids.New(), userID, format, params)
		var err error
		if e, err = scanExport(row); err != nil {
			return err
		}
		_, err = s.jobs.InsertTx(ctx, tx, ExportArgs{ID: e.ID}, &river.InsertOpts{Queue: jobs.QueueReports})
		return err
	})
	return e, err
}

// Get возвращает выгрузку пользователя.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*Export, error) {
	return scanExport(s.pool.QueryRow(ctx, `SELECT `+exportColumns+` FROM report.export WHERE id = $1 AND user_id = $2`, id, userID))
}

// List — последние выгрузки пользователя.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit int) ([]Export, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+exportColumns+` FROM report.export
		WHERE user_id = $1 AND expires_at > now() ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Export, error) {
		e, err := scanExport(r)
		if err != nil {
			return Export{}, err
		}
		return *e, nil
	})
}

// File — готовый файл выгрузки пользователя: содержимое, имя для сохранения, тип, размер.
func (s *Service) File(ctx context.Context, userID, id uuid.UUID) (io.ReadCloser, string, string, int64, error) {
	var path *string
	var format, kind, status string
	var created time.Time
	var size *int64
	err := s.pool.QueryRow(ctx, `SELECT file_path, format, kind, status, created_at, file_size FROM report.export
		WHERE id = $1 AND user_id = $2 AND expires_at > now()`, id, userID).Scan(&path, &format, &kind, &status, &created, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", 0, ErrNotFound
	}
	if err != nil {
		return nil, "", "", 0, err
	}
	if status != "done" || path == nil || size == nil {
		return nil, "", "", 0, ErrNotReady
	}
	f, err := s.blobs.Open(*path)
	if err != nil {
		return nil, "", "", 0, err
	}
	name := fmt.Sprintf("%s-%s.%s", kindFile[kind], created.Format("2006-01-02-1504"), format)
	return f, name, contentTypes[format], *size, nil
}

var kindFile = map[string]string{"facilities": "objects"}

var contentTypes = map[string]string{
	FormatCSV:     "text/csv; charset=utf-8",
	FormatXLSX:    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	FormatGeoJSON: "application/geo+json",
}

// run формирует файл выгрузки: выборка → поток в нужном формате → хранилище.
func (s *Service) run(ctx context.Context, id uuid.UUID) error {
	var kind, format string
	var params []byte
	tag, err := s.pool.Exec(ctx, `UPDATE report.export SET status = 'running' WHERE id = $1 AND status IN ('queued', 'running')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := s.pool.QueryRow(ctx, `SELECT kind, format, params FROM report.export WHERE id = $1`, id).
		Scan(&kind, &format, &params); err != nil {
		return err
	}
	var p FacilityParams
	if err := json.Unmarshal(params, &p); err != nil {
		return s.fail(ctx, id, fmt.Errorf("params: %w", err))
	}
	pr, pw := io.Pipe()
	rows := 0
	go func() {
		w, err := newWriter(format, pw)
		if err == nil {
			err = s.facilities.Each(ctx, p.Filter, p.Sort, func(it facility.Item) error {
				rows++
				return w.Write(it)
			})
		}
		if err == nil {
			err = w.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	obj, err := s.blobs.Put(ctx, "exports", format, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return s.fail(ctx, id, err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE report.export SET status = 'done', rows = $2, file_path = $3, file_size = $4,
		finished_at = now() WHERE id = $1`, id, rows, obj.Path, obj.Size)
	return err
}

func (s *Service) fail(ctx context.Context, id uuid.UUID, cause error) error {
	_, err := s.pool.Exec(ctx, `UPDATE report.export SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`,
		id, cause.Error())
	return errors.Join(cause, err)
}

// cleanup удаляет просроченные выгрузки и их файлы (файл с тем же содержимым может принадлежать
// другой выгрузке — тогда он остаётся).
func (s *Service) cleanup(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		WITH gone AS (DELETE FROM report.export WHERE expires_at <= now() RETURNING file_path)
		SELECT DISTINCT g.file_path FROM gone g
		WHERE g.file_path IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM report.export e WHERE e.file_path = g.file_path AND e.expires_at > now())`)
	if err != nil {
		return 0, err
	}
	paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, p := range paths {
		if err := s.blobs.Remove(p); err != nil {
			return 0, err
		}
	}
	return len(paths), nil
}
