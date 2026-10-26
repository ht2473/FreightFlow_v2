package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/routing"
)

// Устойчивость сети: пакет сценариев выпадения верхних элементов рейтинга критичности (Б4). Рейтинг
// считает только время до МКАД по графу анализа; здесь каждый элемент перекрывается в Valhalla и
// пересчитывается время объектов до нескольких видов целей.
const (
	// resilienceTop — элементов рейтинга критичности в пакете (с ущербом больше нуля).
	resilienceTop = 40
	// resilienceRadiusKm — объекты в этом радиусе от элемента пересчитываются; дальние
	// закрытие одного моста или участка практически не затрагивает (объезд короче пути до цели).
	resilienceRadiusKm = 10
	// resilienceProfile — профиль ТС, как в сетевом анализе.
	resilienceProfile = "truck_12"
)

// resilienceTargets — виды целей пакета: выезд за МКАД, к ТТК и к ж/д грузовым пунктам.
var resilienceTargets = []string{"mkad", "ttk", "rail_freight"}

// ResilienceArgs — пакет устойчивости; Force — пересчитать, даже если граф и анализ не менялись.
type ResilienceArgs struct {
	Force bool `json:"force"`
}

// Kind — вид задачи.
func (ResilienceArgs) Kind() string { return "scenario.resilience" }

// ResilienceWorker считает пакет устойчивости.
type ResilienceWorker struct {
	river.WorkerDefaults[ResilienceArgs]
	Service *Service
	Log     *slog.Logger
}

// Timeout — сорок элементов по три вида целей.
func (w *ResilienceWorker) Timeout(*river.Job[ResilienceArgs]) time.Duration { return 2 * time.Hour }

// Work считает пакет, если сменились граф маршрутизации или сетевой анализ (или Force).
func (w *ResilienceWorker) Work(ctx context.Context, job *river.Job[ResilienceArgs]) error {
	st, err := w.Service.RunResilience(ctx, job.Args.Force)
	switch {
	case errors.Is(err, routing.ErrUnavailable), errors.Is(err, ErrNoAnalysis):
		// Служба маршрутизации не запущена или анализа ещё нет — следующая попытка по расписанию.
		return nil
	case err != nil:
		return err
	case !st.Skipped:
		w.Log.InfoContext(ctx, "resilience computed", "run", st.RunID, "elements", st.Elements, "requests", st.Requests,
			"duration", st.Duration)
	}
	return nil
}

// ErrNoAnalysis — сетевой анализ (рейтинг критичности) ещё не выполнен.
var ErrNoAnalysis = errors.New("scenario: no network analysis")

// ResilienceStats — итог пакета.
type ResilienceStats struct {
	RunID    int
	Skipped  bool
	Elements int
	Requests int
	Duration time.Duration
}

// TargetDamage — последствия выпадения элемента для одного вида целей.
type TargetDamage struct {
	Kind     string  `json:"kind"`
	Affected int     `json:"affected"`
	Lost     int     `json:"lost"`
	DelayMin float64 `json:"delayMin"`
	MaxDelay float64 `json:"maxDelay"`
	Damage   float64 `json:"damage"`
}

// lostMinutes — объект, потерявший доступ к цели, в оценке ущерба: как час объезда (как в Б4).
const lostMinutes = 60

// RunResilience считает пакет выпадений и публикует слой. Без изменений графа и анализа (и без
// force) пакет не пересчитывается.
func (s *Service) RunResilience(ctx context.Context, force bool) (ResilienceStats, error) {
	start := time.Now()
	var st ResilienceStats
	els, err := s.routing.TopElements(ctx, resilienceTop*3)
	if err != nil {
		return st, err
	}
	var top []routing.CriticalElementRef
	for _, e := range els {
		if e.DamageMin > 0 && len(e.Segments) > 0 && len(top) < resilienceTop {
			top = append(top, e)
		}
	}
	if len(top) == 0 {
		return st, ErrNoAnalysis
	}
	built, err := s.routing.GraphBuiltAt(ctx)
	if err != nil {
		return st, err
	}
	version := top[0].VersionID
	if !force {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM scenario.resilience_run
			WHERE graph_version = $1 AND graph_built_at = $2 AND status IN ('done', 'running'))`, version, built).
			Scan(&exists); err != nil {
			return st, err
		}
		if exists {
			st.Skipped = true
			return st, nil
		}
	}
	prof, err := s.routing.ProfileByCode(ctx, resilienceProfile)
	if err != nil {
		return st, err
	}
	targets, err := s.routing.Targets(ctx)
	if err != nil {
		return st, err
	}
	if err := s.pool.QueryRow(ctx, `INSERT INTO scenario.resilience_run (graph_version, graph_built_at, profile_code, targets, radius_km)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, version, built, prof.Code, resilienceTargets, resilienceRadiusKm).
		Scan(&st.RunID); err != nil {
		return st, err
	}
	fail := func(err error) (ResilienceStats, error) {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(wctx, `UPDATE scenario.resilience_run SET status = 'failed', error = $2, finished_at = now()
			WHERE id = $1`, st.RunID, UserMessage(err))
		return st, err
	}

	base := newBaseCache()
	if err := base.load(ctx, s, prof.Code, built); err != nil {
		return fail(err)
	}
	for _, el := range top {
		cands, err := s.candidates(ctx, el.Segments)
		if err != nil {
			return fail(err)
		}
		ex, err := s.routing.SegmentExclusions(ctx, prof.Code, el.Segments)
		if err != nil {
			return fail(err)
		}
		row := struct {
			affected, lost   int
			delay, maxDelay  float64
			damage           float64
			byTarget         []TargetDamage
			affectedEntities map[string]bool
		}{affectedEntities: map[string]bool{}}
		for _, kind := range resilienceTargets {
			ts := targets[kind]
			if len(ts) == 0 || len(cands) == 0 {
				continue
			}
			before, n, err := base.times(ctx, s, prof, kind, ts, cands)
			if err != nil {
				return fail(err)
			}
			st.Requests += n
			after, err := s.nearestPoints(ctx, cands, ts, prof, routing.Exclusions{Locations: ex})
			if err != nil {
				return fail(err)
			}
			st.Requests += batchCount(len(cands), len(ts))
			d := TargetDamage{Kind: kind}
			for i := range cands {
				switch {
				case before[i] < 0:
				case after[i] < 0:
					d.Lost++
					row.affectedEntities[cands[i].key] = true
				case after[i]-before[i] > eps:
					delay := after[i] - before[i]
					d.Affected++
					d.DelayMin += delay
					d.MaxDelay = max(d.MaxDelay, delay)
					row.affectedEntities[cands[i].key] = true
				}
			}
			d.DelayMin = math.Round(d.DelayMin*10) / 10
			d.MaxDelay = math.Round(d.MaxDelay*10) / 10
			d.Damage = d.DelayMin + lostMinutes*float64(d.Lost)
			row.byTarget = append(row.byTarget, d)
			row.lost += d.Lost
			row.delay += d.DelayMin
			row.maxDelay = max(row.maxDelay, d.MaxDelay)
			row.damage += d.Damage
		}
		row.affected = len(row.affectedEntities)
		by, _ := json.Marshal(row.byTarget)
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO scenario.resilience (run_id, element_id, kind, name, criticality_rank, segment_ids, candidates,
			                                 affected, lost, delay_min, max_delay_min, damage, rank, by_target, geom)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 0, $13, ST_Multi(ST_LineMerge(ST_Collect(geom)))
			FROM network.road_segment WHERE id = ANY ($6)
			HAVING count(*) > 0`,
			st.RunID, el.ID, el.Kind, el.Name, el.Rank, el.Segments, len(cands), row.affected, row.lost,
			row.delay, row.maxDelay, row.damage, by); err != nil {
			return fail(fmt.Errorf("store resilience: %w", err))
		}
		st.Elements++
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE scenario.resilience r SET rank = o.rank
			FROM (SELECT element_id, row_number() OVER (ORDER BY damage DESC, criticality_rank) AS rank
			      FROM scenario.resilience WHERE run_id = $1) o
			WHERE r.run_id = $1 AND r.element_id = o.element_id`, st.RunID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE scenario.resilience_run
			SET status = 'done', elements = $2, requests = $3, finished_at = now() WHERE id = $1`,
			st.RunID, st.Elements, st.Requests); err != nil {
			return err
		}
		// Прежние пакеты не нужны: слой и раздел показывают последний.
		if _, err := tx.Exec(ctx, `DELETE FROM scenario.resilience_run WHERE id < $1`, st.RunID); err != nil {
			return err
		}
		var v int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO ingest.data_version (domain, version, published_at) VALUES ('resilience', 1, now())
			ON CONFLICT (domain) DO UPDATE SET version = ingest.data_version.version + 1, published_at = now()
			RETURNING version`).Scan(&v); err != nil {
			return err
		}
		return events.Publish(ctx, tx, events.DataPublished, map[string]any{"versions": map[string]int64{"resilience": v}})
	})
	if err != nil {
		return fail(err)
	}
	st.Duration = time.Since(start)
	return st, nil
}

// candidates — действующие объекты реестра в resilienceRadiusKm от участков элемента.
func (s *Service) candidates(ctx context.Context, segments []uuid.UUID) ([]entity, error) {
	rows, err := s.pool.Query(ctx, `
		WITH e AS (SELECT ST_Collect(geom) AS g FROM network.road_segment WHERE id = ANY ($1))
		SELECT f.id::text, ST_X(f.point), ST_Y(f.point)
		FROM infra.facility f, e
		WHERE f.status = 'active' AND f.point && ST_Expand(e.g, $2 / 60.0)
		  AND ST_DWithin(f.point::geography, e.g::geography, $2 * 1000)
		ORDER BY f.id`, segments, float64(resilienceRadiusKm))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (entity, error) {
		var e entity
		return e, r.Scan(&e.key, &e.point.Lon, &e.point.Lat)
	})
}

// nearestPoints — время объектов до ближайшей цели с исключениями.
func (s *Service) nearestPoints(ctx context.Context, ents []entity, targets []routing.Point, prof routing.Profile,
	ex routing.Exclusions,
) ([]float64, error) {
	src := make([]routing.Point, len(ents))
	for i, e := range ents {
		src[i] = e.point
	}
	minutes, _, err := s.routing.Nearest(ctx, routing.NearestInput{
		Sources: src, Targets: targets, Profile: prof, Exclude: ex, Workers: s.workers,
	})
	return minutes, err
}

// baseCache — время объектов до целей без перекрытий: из показателей доступности текущего графа,
// недостающее — расчётом (один раз на объект и вид цели за пакет).
type baseCache struct {
	m map[string]map[string]float64
}

func newBaseCache() *baseCache { return &baseCache{m: map[string]map[string]float64{}} }

func (b *baseCache) load(ctx context.Context, s *Service, profile string, built time.Time) error {
	rows, err := s.pool.Query(ctx, `
		SELECT target_kind, facility_id::text, coalesce(minutes, -1)::float8 FROM analytics.accessibility
		WHERE profile_code = $1 AND graph_built_at = $2 AND target_kind = ANY ($3)`, profile, built, resilienceTargets)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var m float64
		if err := rows.Scan(&kind, &id, &m); err != nil {
			return err
		}
		if b.m[kind] == nil {
			b.m[kind] = map[string]float64{}
		}
		b.m[kind][id] = m
	}
	return rows.Err()
}

// times — базовое время объектов до цели kind; n — запросов матриц для недостающих.
func (b *baseCache) times(ctx context.Context, s *Service, prof routing.Profile, kind string, targets []routing.Point,
	ents []entity,
) ([]float64, int, error) {
	known := b.m[kind]
	if known == nil {
		known = map[string]float64{}
		b.m[kind] = known
	}
	var missing []entity
	for _, e := range ents {
		if _, ok := known[e.key]; !ok {
			missing = append(missing, e)
		}
	}
	n := 0
	if len(missing) > 0 {
		v, err := s.nearestPoints(ctx, missing, targets, prof, routing.Exclusions{})
		if err != nil {
			return nil, 0, err
		}
		n = batchCount(len(missing), len(targets))
		for i, e := range missing {
			known[e.key] = v[i]
		}
	}
	out := make([]float64, len(ents))
	for i, e := range ents {
		out[i] = known[e.key]
	}
	return out, n, nil
}

// ResilienceRun — пакет устойчивости.
type ResilienceRun struct {
	ID           int
	GraphBuiltAt time.Time
	Profile      string
	Targets      []string
	RadiusKm     float64
	Status       string
	Elements     int
	Error        *string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

// ResilienceRow — элемент пакета.
type ResilienceRow struct {
	ElementID       int
	Kind, Name      string
	CriticalityRank int
	Rank            int
	Candidates      int
	Affected, Lost  int
	DelayMin        float64
	MaxDelayMin     float64
	Damage          float64
	ByTarget        []TargetDamage
	Lon, Lat        float64
}

// Resilience — последний пакет (завершённый, а если идёт новый — и он) и рейтинг ущерба.
func (s *Service) Resilience(ctx context.Context) (last, running *ResilienceRun, rows []ResilienceRow, err error) {
	runs, err := s.pool.Query(ctx, `
		SELECT id, graph_built_at, profile_code, targets, radius_km::float8, status, elements, error, started_at, finished_at
		FROM scenario.resilience_run ORDER BY id DESC LIMIT 5`)
	if err != nil {
		return nil, nil, nil, err
	}
	list, err := pgx.CollectRows(runs, func(r pgx.CollectableRow) (ResilienceRun, error) {
		var x ResilienceRun
		err := r.Scan(&x.ID, &x.GraphBuiltAt, &x.Profile, &x.Targets, &x.RadiusKm, &x.Status, &x.Elements, &x.Error,
			&x.StartedAt, &x.FinishedAt)
		return x, err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range list {
		switch {
		case list[i].Status == "running" && running == nil && last == nil:
			running = &list[i]
		case list[i].Status != "running" && last == nil:
			last = &list[i]
		}
	}
	if last == nil || last.Status != "done" {
		return last, running, nil, nil
	}
	q, err := s.pool.Query(ctx, `
		SELECT element_id, kind, name, criticality_rank, rank, candidates, affected, lost, delay_min::float8,
		       max_delay_min::float8, damage::float8, by_target,
		       ST_X(ST_LineInterpolatePoint(ST_GeometryN(geom, 1), 0.5)), ST_Y(ST_LineInterpolatePoint(ST_GeometryN(geom, 1), 0.5))
		FROM scenario.resilience WHERE run_id = $1 ORDER BY rank`, last.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err = pgx.CollectRows(q, func(r pgx.CollectableRow) (ResilienceRow, error) {
		var x ResilienceRow
		var by []byte
		if err := r.Scan(&x.ElementID, &x.Kind, &x.Name, &x.CriticalityRank, &x.Rank, &x.Candidates, &x.Affected,
			&x.Lost, &x.DelayMin, &x.MaxDelayMin, &x.Damage, &by, &x.Lon, &x.Lat); err != nil {
			return x, err
		}
		return x, json.Unmarshal(by, &x.ByTarget)
	})
	return last, running, rows, err
}

// activeStates — пакет уже стоит в очереди или считается: повторная постановка не нужна.
var activeStates = []rivertype.JobState{
	rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
	rivertype.JobStateRunning, rivertype.JobStateScheduled,
}

// resilienceOpts — очередь сценариев; одновременно не больше одного пакета.
func resilienceOpts() *river.InsertOpts {
	return &river.InsertOpts{Queue: jobs.QueueScenario, UniqueOpts: river.UniqueOpts{ByArgs: true, ByState: activeStates}}
}

// EnqueueResilience ставит пересчёт пакета.
func (s *Service) EnqueueResilience(ctx context.Context) error {
	_, err := s.jobs.Insert(ctx, ResilienceArgs{Force: true}, resilienceOpts())
	return err
}
