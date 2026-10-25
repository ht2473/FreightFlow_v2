package api

import (
	"context"
	"time"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/search"
)

// --- Поиск ---

// searchRequest — запрос от имени пользователя: виды документов — по правам (наборы данных и
// источники — у управляющих данными).
func searchRequest(ctx context.Context, q string, lon, lat gen.OptFloat64) search.Request {
	pr := principal(ctx)
	kinds := []string{"facility", "address", "street", "territory", "legal_act", "help", "road_work", "saved_view", "report"}
	if pr.Has(identity.PermDataManage) {
		kinds = append(kinds, "dataset", "source")
	}
	req := search.Request{Q: q, UserID: pr.UserID, Roles: pr.Roles, Kinds: kinds}
	if x, ok := lon.Get(); ok {
		if y, ok := lat.Get(); ok {
			req.Center = &search.Point{Lon: x, Lat: y}
		}
	}
	return req
}

func searchHit(h search.Hit) gen.SearchHit {
	out := gen.SearchHit{Kind: gen.SearchHitKind(h.Kind), Ref: h.Ref, Title: h.Title, Score: h.Score}
	if h.Subtitle != "" {
		out.Subtitle = gen.NewOptString(h.Subtitle)
	}
	if h.Point != nil {
		out.Point = gen.NewOptLngLat(gen.LngLat{Lon: h.Point.Lon, Lat: h.Point.Lat})
	}
	return out
}

func searchHits(hits []search.Hit) []gen.SearchHit {
	out := make([]gen.SearchHit, len(hits))
	for i, h := range hits {
		out[i] = searchHit(h)
	}
	return out
}

func parsedQuery(p search.Parsed) gen.ParsedQuery {
	out := gen.ParsedQuery{
		Text: p.Text, Facility: p.Facility(), Types: nonNil(p.Types), Okrugs: nonNil(p.Okrugs),
		Districts: nonNil(p.Districts), Chips: make([]gen.SearchChip, len(p.Chips)),
		MinArea: optNum(p.MinArea), MaxArea: optNum(p.MaxArea), MaxMkadMin: optNum(p.MaxMkadMin),
		MinConfidence: optNum(p.MinConfidence),
	}
	for i, c := range p.Chips {
		out.Chips[i] = gen.SearchChip{Field: gen.SearchChipField(c.Field), Label: c.Label}
	}
	for _, r := range p.Rings {
		out.Rings = append(out.Rings, gen.ParsedQueryRingsItem(r))
	}
	if out.Rings == nil {
		out.Rings = []gen.ParsedQueryRingsItem{}
	}
	if p.NearRestrictions != nil {
		out.NearRestrictions = gen.NewOptBool(*p.NearRestrictions)
	}
	if p.HasPermit != nil {
		out.HasPermit = gen.NewOptBool(*p.HasPermit)
	}
	if p.NearText != "" {
		out.NearText = gen.NewOptString(p.NearText)
		out.NearKm = gen.NewOptFloat64(p.NearKm)
	}
	if p.Coordinates != nil {
		out.Coordinates = gen.NewOptLngLat(gen.LngLat{Lon: p.Coordinates.Lon, Lat: p.Coordinates.Lat})
	}
	return out
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Server) Search(ctx context.Context, p gen.SearchParams) (*gen.SearchResult, error) {
	res, err := s.search.Search(ctx, searchRequest(ctx, p.Q, p.Lon, p.Lat))
	if err != nil {
		return nil, err
	}
	out := &gen.SearchResult{
		Query: p.Q, Parsed: parsedQuery(res.Parsed), Hits: searchHits(res.Hits), Fuzzy: res.Fuzzy,
		TookMs: int(res.Took / time.Millisecond),
	}
	if res.Near != nil {
		out.Near = gen.NewOptSearchHit(searchHit(*res.Near))
	}
	if res.Facilities != nil {
		f := gen.SearchResultFacilities{Total: res.Facilities.Total, Items: make([]gen.FacilityItem, len(res.Facilities.Items))}
		for i, it := range res.Facilities.Items {
			f.Items[i] = facilityItem(it)
		}
		out.Facilities = gen.NewOptSearchResultFacilities(f)
	}
	return out, nil
}

func (s *Server) SearchSuggest(ctx context.Context, p gen.SearchSuggestParams) ([]gen.SearchHit, error) {
	hits, err := s.search.Suggest(ctx, searchRequest(ctx, p.Q, p.Lon, p.Lat), p.Limit.Or(8))
	if err != nil {
		return nil, err
	}
	return searchHits(hits), nil
}

func (s *Server) SearchParse(ctx context.Context, p gen.SearchParseParams) (*gen.ParsedQuery, error) {
	ps, err := s.search.Parser(ctx)
	if err != nil {
		return nil, err
	}
	out := parsedQuery(ps.Parse(p.Q))
	return &out, nil
}

func (s *Server) Geocode(ctx context.Context, p gen.GeocodeParams) ([]gen.SearchHit, error) {
	hits, err := s.search.Geocode(ctx, searchRequest(ctx, p.Q, gen.OptFloat64{}, gen.OptFloat64{}), p.Limit.Or(5))
	if err != nil {
		return nil, err
	}
	return searchHits(hits), nil
}

func (s *Server) ReverseGeocode(ctx context.Context, p gen.ReverseGeocodeParams) ([]gen.SearchHit, error) {
	hits, err := s.search.Reverse(ctx, search.Point{Lon: p.Lon, Lat: p.Lat}, searchRequest(ctx, "", gen.OptFloat64{}, gen.OptFloat64{}))
	if err != nil {
		return nil, err
	}
	return searchHits(hits), nil
}
