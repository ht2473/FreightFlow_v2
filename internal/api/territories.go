package api

import (
	"context"
	"encoding/json"

	"github.com/go-faster/jx"

	"freightflow/internal/api/gen"
	"freightflow/internal/territory"
)

// --- Территории ---

func optInt(v *int) gen.OptInt {
	if v == nil {
		return gen.OptInt{}
	}
	return gen.NewOptInt(*v)
}

func territoryRow(r territory.Row) gen.TerritoryRow {
	out := gen.TerritoryRow{
		ID: r.ID, Kind: gen.TerritoryRowKind(r.Kind), Name: r.Name, ShortName: optStr(r.ShortName),
		ParentName: optStr(r.ParentName), AreaKm2: r.AreaKm2, Population: optInt(r.Population),
		PopulationYear: optInt(r.PopulationYear), Density: optNum(r.Density()), Facilities: r.Facilities,
		BuildingAreaM2: r.BuildingM2, Poi: r.Poi, WorksActive: r.WorksActive,
	}
	if r.ParentID != nil {
		out.ParentId = gen.NewOptUUID(*r.ParentID)
	}
	return out
}

func (s *Server) ListTerritories(ctx context.Context, p gen.ListTerritoriesParams) ([]gen.TerritoryRow, error) {
	list, err := s.territories.List(ctx, string(p.Kind.Or("")))
	if err != nil {
		return nil, err
	}
	out := make([]gen.TerritoryRow, len(list))
	for i, r := range list {
		out[i] = territoryRow(r)
	}
	return out, nil
}

func (s *Server) GetTerritory(ctx context.Context, p gen.GetTerritoryParams) (*gen.TerritoryProfile, error) {
	t, err := s.territories.Get(ctx, p.TerritoryId)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(t.Geometry, &fields); err != nil {
		return nil, err
	}
	geom := gen.TerritoryProfileGeometry{}
	for k, v := range fields {
		geom[k] = jx.Raw(v)
	}
	row := territoryRow(t.Row)
	out := &gen.TerritoryProfile{
		ID: row.ID, Kind: gen.TerritoryProfileKind(row.Kind), Name: row.Name, ShortName: row.ShortName,
		ParentId: row.ParentId, ParentName: row.ParentName, AreaKm2: row.AreaKm2, Population: row.Population,
		PopulationYear: row.PopulationYear, Density: row.Density, Facilities: row.Facilities,
		BuildingAreaM2: row.BuildingAreaM2, Poi: row.Poi, WorksActive: row.WorksActive,
		Geometry: geom, Bbox: t.BBox[:], Summary: *areaSummary(t.Summary),
		PopulationSeries: make([]gen.TerritoryProfilePopulationSeriesItem, len(t.Population)),
		Children:         make([]gen.TerritoryRow, len(t.Children)),
	}
	for i, v := range t.Population {
		out.PopulationSeries[i] = gen.TerritoryProfilePopulationSeriesItem{Year: v.Year, Value: v.Value}
	}
	for i, c := range t.Children {
		out.Children[i] = territoryRow(c)
	}
	return out, nil
}
