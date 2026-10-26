package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/routing"
)

// Routing — расчёты маршрутизации для сценариев (модуль routing).
type Routing interface {
	ProfileByCode(ctx context.Context, code string) (routing.Profile, error)
	Rules(ctx context.Context) ([]routing.ZoneRule, error)
	ZoneRing(ctx context.Context, zone string) ([]routing.Ring, error)
	BlockedPoints(ctx context.Context, pts []routing.Point, ds []routing.ZoneDecision) ([]routing.BlockedPoint, error)
	Nearest(ctx context.Context, in routing.NearestInput) (minutes, km []float64, err error)
	IsochroneWith(ctx context.Context, origin routing.Point, p routing.Profile, minutes []int, depart time.Time,
		ex routing.Exclusions) (json.RawMessage, error)
	SegmentExclusions(ctx context.Context, profileCode string, segments []uuid.UUID) ([]routing.ExcludePoint, error)
	Element(ctx context.Context, id int) (routing.CriticalElementRef, error)
	TopElements(ctx context.Context, top int) ([]routing.CriticalElementRef, error)
	Targets(ctx context.Context) (map[string][]routing.Point, error)
	GraphBuiltAt(ctx context.Context) (time.Time, error)
}

// Service — сценарии, устойчивость и оценка локации.
type Service struct {
	pool    *pgxpool.Pool
	jobs    *jobs.Client
	routing Routing
	// workers — параллельных запросов матриц к службе маршрутизации в одном расчёте.
	workers int
}

// matrixWorkers — параллельных запросов матриц: служба обслуживает 4 потока (compose), больше —
// очередь внутри службы без выигрыша.
const matrixWorkers = 4

// New создаёт сервис; client может быть nil в процессе без постановки задач.
func New(pool *pgxpool.Pool, client *jobs.Client, r Routing) *Service {
	return &Service{pool: pool, jobs: client, routing: r, workers: matrixWorkers}
}

// ErrNotFound — сценария нет или он чужой.
var ErrNotFound = errors.New("scenario: not found")

// ErrBusy — сценарий ещё считается.
var ErrBusy = errors.New("scenario: computing")

const scenarioColumns = `id, user_id, name, description, params, status, progress, summary, error, graph_built_at,
	created_at, started_at, finished_at`

func scanScenario(r pgx.Row) (*Scenario, error) {
	var s Scenario
	var params, progress, summary []byte
	err := r.Scan(&s.ID, &s.UserID, &s.Name, &s.Description, &params, &s.Status, &progress, &summary, &s.Error,
		&s.GraphBuiltAt, &s.CreatedAt, &s.StartedAt, &s.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(params, &s.Params); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(progress, &s.Progress); err != nil {
		return nil, err
	}
	if summary != nil {
		s.Summary = &Summary{}
		if err := json.Unmarshal(summary, s.Summary); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// RunArgs — расчёт сценария.
type RunArgs struct {
	ID uuid.UUID `json:"id"`
}

// Kind — вид задачи.
func (RunArgs) Kind() string { return "scenario.run" }

// Create проверяет параметры, сохраняет сценарий и ставит его расчёт.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name, description string, p Params) (*Scenario, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, invalid("не задано название")
	}
	if _, err := s.routing.ProfileByCode(ctx, p.Profile); err != nil {
		return nil, invalid("неизвестный профиль ТС %q", p.Profile)
	}
	params, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out *Scenario
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO scenario.scenario (id, user_id, name, description, params)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+scenarioColumns, ids.New(), userID, name, description, params)
		var err error
		if out, err = scanScenario(row); err != nil {
			return err
		}
		return s.enqueue(ctx, tx, out.ID)
	})
	return out, err
}

func (s *Service) enqueue(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := s.jobs.InsertTx(ctx, tx, RunArgs{ID: id}, &river.InsertOpts{Queue: jobs.QueueScenario, MaxAttempts: 1})
	return err
}

// Rerun пересчитывает готовый или неудавшийся сценарий (например, после обновления графа).
func (s *Service) Rerun(ctx context.Context, userID, id uuid.UUID) (*Scenario, error) {
	var out *Scenario
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `UPDATE scenario.scenario
			SET status = 'queued', progress = '{}', error = NULL, started_at = NULL, finished_at = NULL
			WHERE id = $1 AND user_id = $2 AND status IN ('done', 'failed') RETURNING `+scenarioColumns, id, userID)
		var err error
		out, err = scanScenario(row)
		if errors.Is(err, ErrNotFound) {
			if _, gerr := s.Get(ctx, userID, id); gerr == nil {
				return ErrBusy
			}
		}
		if err != nil {
			return err
		}
		return s.enqueue(ctx, tx, id)
	})
	return out, err
}

// Get — сценарий пользователя.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*Scenario, error) {
	return scanScenario(s.pool.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenario.scenario
		WHERE id = $1 AND user_id = $2`, id, userID))
}

// List — сценарии пользователя, новые первыми.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit int) ([]Scenario, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+scenarioColumns+` FROM scenario.scenario
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Scenario, error) {
		sc, err := scanScenario(r)
		if err != nil {
			return Scenario{}, err
		}
		return *sc, nil
	})
}

// Delete удаляет сценарий пользователя с результатами; считающийся удалить нельзя.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM scenario.scenario WHERE id = $1 AND user_id = $2 AND status <> 'running'`,
		id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, userID, id); err == nil {
			return ErrBusy
		}
		return ErrNotFound
	}
	return nil
}

// FacilityRow — изменение времени объекта до цели.
type FacilityRow struct {
	Entity         string
	Name           string
	District       string
	Lon, Lat       float64
	BaseMin        *float64
	ScenarioMin    *float64
	BaseStatus     string
	ScenarioStatus string
}

// Facilities — объекты с изменившимся временем до цели kind (все — при all), по убыванию прироста.
func (s *Service) Facilities(ctx context.Context, userID, id uuid.UUID, kind string, all bool, limit int) ([]FacilityRow, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT d.entity, coalesce(f.name, ft.name, sc.params->'newFacility'->>'name', ''), coalesce(a.name, ''),
		       coalesce(ST_X(f.point), (sc.params->'newFacility'->>'lon')::float8),
		       coalesce(ST_Y(f.point), (sc.params->'newFacility'->>'lat')::float8),
		       d.base_min::float8, d.scenario_min::float8, d.base_status, d.scenario_status
		FROM scenario.facility_delta d
		JOIN scenario.scenario sc ON sc.id = d.scenario_id
		LEFT JOIN infra.facility f ON f.id = nullif(d.entity, 'new')::uuid
		LEFT JOIN ref.facility_type ft ON ft.code = f.type_code
		LEFT JOIN territory.admin_area a ON a.id = f.district_id
		WHERE d.scenario_id = $1 AND d.target_kind = $2
		  AND ($3 OR d.base_status <> d.scenario_status OR d.base_min IS DISTINCT FROM d.scenario_min)
		ORDER BY (d.base_status = 'ok' AND d.scenario_status <> 'ok') DESC,
		         coalesce(d.scenario_min - d.base_min, 0) DESC, d.entity
		LIMIT $4`, id, kind, all, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FacilityRow, error) {
		var f FacilityRow
		err := r.Scan(&f.Entity, &f.Name, &f.District, &f.Lon, &f.Lat, &f.BaseMin, &f.ScenarioMin, &f.BaseStatus,
			&f.ScenarioStatus)
		return f, err
	})
}

// DistrictRow — изменения по району: затронутые объекты, прирост времени, потеря доступа.
type DistrictRow struct {
	District   string
	Facilities int
	Affected   int
	Lost       int
	DelayMin   float64
	MaxDelay   float64
}

// Districts — изменения доступности цели kind по районам (объекты реестра).
func (s *Service) Districts(ctx context.Context, userID, id uuid.UUID, kind string) ([]DistrictRow, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT coalesce(a.name, 'Вне районов Москвы'), count(*)::int,
		       count(*) FILTER (WHERE d.scenario_min > d.base_min)::int,
		       count(*) FILTER (WHERE d.base_status = 'ok' AND d.scenario_status <> 'ok')::int,
		       coalesce(sum(d.scenario_min - d.base_min) FILTER (WHERE d.scenario_min > d.base_min), 0)::float8,
		       coalesce(max(d.scenario_min - d.base_min) FILTER (WHERE d.scenario_min > d.base_min), 0)::float8
		FROM scenario.facility_delta d
		JOIN infra.facility f ON f.id = nullif(d.entity, 'new')::uuid
		LEFT JOIN territory.admin_area a ON a.id = f.district_id
		WHERE d.scenario_id = $1 AND d.target_kind = $2
		GROUP BY a.name
		HAVING count(*) FILTER (WHERE d.scenario_min > d.base_min OR d.base_status <> d.scenario_status) > 0
		ORDER BY 5 DESC, 4 DESC, 1`, id, kind)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DistrictRow, error) {
		var d DistrictRow
		err := r.Scan(&d.District, &d.Facilities, &d.Affected, &d.Lost, &d.DelayMin, &d.MaxDelay)
		return d, err
	})
}

// Isochrones — изохроны базы и сценария (GeoJSON FeatureCollection; свойства origin, name,
// minutes, variant, areaKm2).
func (s *Service) Isochrones(ctx context.Context, userID, id uuid.UUID) (json.RawMessage, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	var out json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT jsonb_build_object('type', 'FeatureCollection', 'features', coalesce(jsonb_agg(jsonb_build_object(
		           'type', 'Feature', 'geometry', ST_AsGeoJSON(i.geom, 6)::jsonb,
		           'properties', jsonb_build_object('origin', i.origin, 'name',
		               coalesce(f.name, sc.params->'newFacility'->>'name', ''), 'minutes', i.minutes,
		               'variant', i.variant, 'areaKm2', round(i.area_km2::numeric, 2)))
		       ORDER BY i.origin, i.minutes DESC, i.variant), '[]'::jsonb))
		FROM scenario.isochrone i
		JOIN scenario.scenario sc ON sc.id = i.scenario_id
		LEFT JOIN infra.facility f ON f.id = nullif(i.origin, 'new')::uuid
		WHERE i.scenario_id = $1`, id).Scan(&out)
	return out, err
}
