package facility

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Point — действующий объект для кластеров карты: координаты и свойства отбора.
type Point struct {
	ID         uuid.UUID
	Type       string
	Name       *string
	Lon, Lat   float64
	Area       *float64
	Confidence float64
	MkadMin    *float64
}

// Points — все действующие объекты; на мелких масштабах карта собирает их в кластеры сама,
// отбор по фильтру реестра тоже на её стороне.
func (s *Service) Points(ctx context.Context) ([]Point, error) {
	rows, err := s.pool.Query(ctx, `SELECT f.id, f.type_code, f.name,
			round(ST_X(f.point::geometry)::numeric, 6)::float8, round(ST_Y(f.point::geometry)::numeric, 6)::float8,
			f.building_area_m2::float8, round(f.confidence::numeric, 2)::float8, round(acc.minutes::numeric)::float8`+fromClause+`
		WHERE f.status = 'active' ORDER BY f.building_area_m2 DESC NULLS LAST`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Point, error) {
		var p Point
		return p, r.Scan(&p.ID, &p.Type, &p.Name, &p.Lon, &p.Lat, &p.Area, &p.Confidence, &p.MkadMin)
	})
}
