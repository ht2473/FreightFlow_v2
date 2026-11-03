package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
)

// Actor — кто выполняет действие: владелец правил или оператор с правом на все алерты.
type Actor struct {
	UserID uuid.UUID
	Manage bool
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// bounds — охват данных (Москва и ближняя область): области вне его не принимаются.
const bounds = `ST_MakeEnvelope(35.1, 54.9, 38.9, 56.5, 4326)`

// area вычисляет область правила (GeoJSON мультиполигона) или пустую строку, если она не нужна.
func area(ctx context.Context, q querier, s Scope) (string, error) {
	var out *string
	var bad string
	var err error
	switch s.Type {
	case ScopeCorridor:
		err = q.QueryRow(ctx, `
			WITH g AS (SELECT ST_GeomFromGeoJSON($1) AS g)
			SELECT CASE
				WHEN GeometryType(g) <> 'LINESTRING' THEN 'коридор задаётся линией'
				WHEN ST_NPoints(g) > $3 THEN 'в линии коридора слишком много точек'
				WHEN NOT ST_Within(g, `+bounds+`) THEN 'линия коридора вне охвата данных'
				WHEN ST_Length(g::geography) > $4 * 1000 THEN 'коридор длиннее ' || $4 || ' км'
				ELSE '' END,
				ST_AsGeoJSON(ST_Multi(ST_Buffer(g::geography, $2)::geometry), 6)
			FROM g`, string(s.Line), s.BufferM, maxVertices, maxCorridorKm).Scan(&bad, &out)
	case ScopeFacilities:
		var found int
		err = q.QueryRow(ctx, `
			SELECT count(*), ST_AsGeoJSON(ST_Multi(ST_Union(ST_Buffer(point::geography, $2)::geometry)), 6)
			FROM infra.facility WHERE id = ANY($1)`, s.FacilityIDs, s.RadiusM).Scan(&found, &out)
		if err == nil && found != len(s.FacilityIDs) {
			bad = "часть объектов не найдена в реестре"
		}
	case ScopeArea:
		err = q.QueryRow(ctx, `
			WITH g AS (SELECT ST_CollectionExtract(ST_MakeValid(ST_GeomFromGeoJSON($1)), 3) AS g)
			SELECT CASE
				WHEN ST_IsEmpty(g) THEN 'область задаётся многоугольником'
				WHEN ST_NPoints(g) > $2 THEN 'в контуре области слишком много точек'
				WHEN NOT ST_Within(g, `+bounds+`) THEN 'область вне охвата данных'
				WHEN ST_Area(g::geography) > $3 * 1e6 THEN 'область больше ' || $3 || ' км²'
				ELSE '' END,
				ST_AsGeoJSON(ST_Multi(g), 6)
			FROM g`, string(s.Polygon), maxVertices, maxAreaKm2).Scan(&bad, &out)
	default:
		return "", nil
	}
	// Разбор GeoJSON в PostGIS сообщает об ошибке как о внутренней (XX000) или о неверном значении.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "XX000" || pgErr.Code == "22023") {
		return "", invalid("неверная геометрия области")
	}
	if err != nil {
		return "", fmt.Errorf("rule area: %w", err)
	}
	if bad != "" {
		return "", invalid("%s", bad)
	}
	if out == nil {
		return "", invalid("область пуста")
	}
	return *out, nil
}

// checkRefs проверяет ссылки параметров на справочники базы: профиль ТС, источники.
func checkRefs(ctx context.Context, q querier, in *RuleInput) error {
	switch in.Kind {
	case KindAccessibility:
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ref.vehicle_profile WHERE code = $1)`, in.Params.Profile).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalid("неизвестный профиль ТС %q", in.Params.Profile)
		}
	case KindSources:
		if len(in.Params.Sources) == 0 {
			return nil
		}
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM ingest.source WHERE code = ANY($1)`, in.Params.Sources).Scan(&n); err != nil {
			return err
		}
		if n != len(in.Params.Sources) {
			return invalid("часть источников не найдена")
		}
	}
	return nil
}

const ruleColumns = `r.id, r.owner_id, u.name, r.name, r.kind, r.scope, r.params, r.severity, r.channels, r.enabled,
	ST_AsGeoJSON(r.area, 6), r.checked_at, r.check_error, r.created_at, r.updated_at,
	(SELECT count(*) FROM alerting.alert a WHERE a.rule_id = r.id AND a.status <> 'closed')`

const ruleFrom = ` FROM alerting.rule r JOIN core.app_user u ON u.id = r.owner_id`

func scanRule(row pgx.Row) (*Rule, error) {
	var r Rule
	var scope, params []byte
	var areaJSON *string
	err := row.Scan(&r.ID, &r.OwnerID, &r.OwnerName, &r.Name, &r.Kind, &scope, &params, &r.Severity, &r.Channels,
		&r.Enabled, &areaJSON, &r.CheckedAt, &r.CheckError, &r.CreatedAt, &r.UpdatedAt, &r.OpenAlerts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(scope, &r.Scope); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(params, &r.Params); err != nil {
		return nil, err
	}
	if areaJSON != nil {
		r.Area = json.RawMessage(*areaJSON)
	}
	return &r, nil
}

// ListRules — правила пользователя; оператору с all — все правила.
func (s *Service) ListRules(ctx context.Context, a Actor, all bool) ([]Rule, error) {
	where := ` WHERE r.owner_id = $1`
	if all && a.Manage {
		where = ` WHERE $1::uuid IS NOT NULL`
	}
	rows, err := s.pool.Query(ctx, `SELECT `+ruleColumns+ruleFrom+where+` ORDER BY r.created_at DESC`, a.UserID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// Rule — правило; чужое видит только оператор.
func (s *Service) Rule(ctx context.Context, a Actor, id uuid.UUID) (*Rule, error) {
	r, err := scanRule(s.pool.QueryRow(ctx, `SELECT `+ruleColumns+ruleFrom+` WHERE r.id = $1`, id))
	if err != nil {
		return nil, err
	}
	if r.OwnerID != a.UserID && !a.Manage {
		return nil, ErrNotFound
	}
	return r, nil
}

// CreateRule сохраняет правило пользователя. Первая проверка запоминает текущее состояние
// данных: о том, что было до создания правила, оно не сообщает.
func (s *Service) CreateRule(ctx context.Context, a Actor, in RuleInput) (*Rule, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	id := ids.New()
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		geo, err := area(ctx, tx, in.Scope)
		if err != nil {
			return err
		}
		if err := checkRefs(ctx, tx, &in); err != nil {
			return err
		}
		scope, params, err := marshalInput(in)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO alerting.rule (id, owner_id, name, kind, scope, area, params, severity, channels, enabled)
			VALUES ($1, $2, $3, $4, $5, ST_GeomFromGeoJSON(NULLIF($6, '')), $7, $8, $9, $10)`,
			id, a.UserID, in.Name, in.Kind, scope, geo, params, in.Severity, in.Channels, in.Enabled)
		if err != nil {
			return fmt.Errorf("insert rule: %w", err)
		}
		return s.enqueueCheck(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	return s.Rule(ctx, a, id)
}

// UpdateRule меняет своё правило. Смена области или параметров сбрасывает запомненное
// состояние: сравнение начинается заново.
func (s *Service) UpdateRule(ctx context.Context, a Actor, id uuid.UUID, in RuleInput) (*Rule, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var owner uuid.UUID
		var kind string
		err := tx.QueryRow(ctx, `SELECT owner_id, kind FROM alerting.rule WHERE id = $1 FOR UPDATE`, id).Scan(&owner, &kind)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && owner != a.UserID {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if kind != in.Kind {
			return invalid("вид правила менять нельзя — создайте новое правило")
		}
		geo, err := area(ctx, tx, in.Scope)
		if err != nil {
			return err
		}
		if err := checkRefs(ctx, tx, &in); err != nil {
			return err
		}
		scope, params, err := marshalInput(in)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE alerting.rule SET name = $2, scope = $3, area = ST_GeomFromGeoJSON(NULLIF($4, '')), params = $5,
				severity = $6, channels = $7, enabled = $8, updated_at = now(),
				state = CASE WHEN scope = $3 AND params = $5 THEN state END,
				check_error = NULL
			WHERE id = $1`, id, in.Name, scope, geo, params, in.Severity, in.Channels, in.Enabled)
		if err != nil {
			return fmt.Errorf("update rule: %w", err)
		}
		return s.enqueueCheck(ctx, tx)
	})
	if err != nil {
		return nil, err
	}
	return s.Rule(ctx, a, id)
}

// DeleteRule удаляет своё правило вместе с его алертами.
func (s *Service) DeleteRule(ctx context.Context, a Actor, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alerting.rule WHERE id = $1 AND owner_id = $2`, id, a.UserID)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func marshalInput(in RuleInput) (scope, params []byte, err error) {
	if scope, err = json.Marshal(in.Scope); err != nil {
		return nil, nil, err
	}
	params, err = json.Marshal(in.Params)
	return scope, params, err
}

// RuleArea — область правила (GeoJSON) для карты алерта; пусто, если области нет.
func (s *Service) RuleArea(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	var geo *string
	if err := s.pool.QueryRow(ctx, `SELECT ST_AsGeoJSON(area, 6) FROM alerting.rule WHERE id = $1`, id).Scan(&geo); err != nil {
		return nil, fmt.Errorf("rule area: %w", err)
	}
	if geo == nil {
		return nil, nil
	}
	return json.RawMessage(*geo), nil
}
