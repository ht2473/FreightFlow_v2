package search

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// reverseRadiusM — радиус обратного геокодирования.
const reverseRadiusM = 50

// Geocode — прямое геокодирование: адреса реестра Москвы и улицы по словам запроса;
// номер дома, корпус и строение сверяются с заголовком.
func (s *Service) Geocode(ctx context.Context, req Request, limit int) ([]Hit, error) {
	req.Kinds = []string{"address", "street"}
	req.PerKind = limit
	hits, _, err := s.documents(ctx, req, req.Q)
	if err != nil {
		return nil, err
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// Reverse — обратное геокодирование: ближайший адрес в 50 м и район (вне районов — округ), где
// лежит точка.
func (s *Service) Reverse(ctx context.Context, p Point, req Request) ([]Hit, error) {
	var out []Hit
	addr, err := s.collect(ctx, `
		SELECT d.kind, d.ref, d.title, d.subtitle, ST_X(d.point), ST_Y(d.point),
		       ST_Distance(d.point::geography, g.pt::geography) AS score
		FROM search.document d, (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326) AS pt) g
		WHERE d.kind = 'address' AND d.point && ST_Expand(g.pt, 0.001)
		  AND ST_DWithin(d.point::geography, g.pt::geography, $3)
		ORDER BY d.point <-> g.pt LIMIT 1`, p.Lon, p.Lat, reverseRadiusM)
	if err != nil {
		return nil, err
	}
	out = append(out, addr...)
	var t Hit
	var sub *string
	err = s.pool.QueryRow(ctx, `
		SELECT d.ref, d.title, d.subtitle FROM territory.admin_area a
		JOIN search.document d ON d.kind = 'territory' AND d.ref = a.id::text
		WHERE a.kind IN ('district', 'okrug') AND ST_Intersects(a.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326))
		ORDER BY a.kind = 'district' DESC LIMIT 1`, p.Lon, p.Lat).Scan(&t.Ref, &t.Title, &sub)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		t.Kind = "territory"
		if sub != nil {
			t.Subtitle = *sub
		}
		out = append(out, t)
	}
	return out, nil
}
