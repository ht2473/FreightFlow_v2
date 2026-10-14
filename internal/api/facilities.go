package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-faster/jx"
	"github.com/google/uuid"

	"freightflow/internal/api/gen"
	"freightflow/internal/facility"
)

// --- Реестр объектов ---

// facilityFilter собирает фильтр реестра из параметров запроса (общих у списка и фасетов).
func facilityFilter(q gen.OptString, types []string, okrugs, districts []uuid.UUID, rings []gen.FacilityRingItem,
	minArea, maxArea, minConfidence gen.OptFloat64, status gen.OptFacilityStatus, bbox, near []float64,
	maxMkad gen.OptFloat64, nearRestrictions, hasPermit gen.OptBool,
) facility.Filter {
	f := facility.Filter{
		Q: q.Or(""), Types: types, Okrugs: okrugs, Districts: districts, Status: string(status.Or(gen.FacilityStatusActive)),
		BBox: bbox,
	}
	for _, r := range rings {
		f.Rings = append(f.Rings, string(r))
	}
	opt := func(v gen.OptFloat64) *float64 {
		if x, ok := v.Get(); ok {
			return &x
		}
		return nil
	}
	optBool := func(v gen.OptBool) *bool {
		if x, ok := v.Get(); ok {
			return &x
		}
		return nil
	}
	f.MinArea, f.MaxArea, f.MinConfidence, f.MaxMkadMin = opt(minArea), opt(maxArea), opt(minConfidence), opt(maxMkad)
	f.NearRestrictions, f.HasPermit = optBool(nearRestrictions), optBool(hasPermit)
	if len(near) == 3 {
		f.Near = &facility.Circle{Lon: near[0], Lat: near[1], RadiusM: near[2]}
	}
	return f
}

func (s *Server) ListFacilities(ctx context.Context, p gen.ListFacilitiesParams) (*gen.FacilityPage, error) {
	f := facilityFilter(p.Q, p.Type, p.Okrug, p.District, p.Ring, p.MinArea, p.MaxArea, p.MinConfidence, p.Status,
		p.Bbox, p.Near, p.MaxMkadMin, p.NearRestrictions, p.HasPermit)
	page, err := s.facilities.List(ctx, f, p.Sort.Or(facility.DefaultSort), p.Cursor.Or(""), p.Limit.Or(facility.DefaultLimit))
	if err != nil {
		return nil, err
	}
	out := &gen.FacilityPage{Items: make([]gen.FacilityItem, len(page.Items)), Total: page.Total}
	for i, it := range page.Items {
		out.Items[i] = facilityItem(it)
	}
	if page.Next != "" {
		out.Next = gen.NewOptString(page.Next)
	}
	return out, nil
}

func (s *Server) FacilityFacets(ctx context.Context, p gen.FacilityFacetsParams) (*gen.FacilityFacets, error) {
	f := facilityFilter(p.Q, p.Type, p.Okrug, p.District, p.Ring, p.MinArea, p.MaxArea, p.MinConfidence, p.Status,
		p.Bbox, p.Near, p.MaxMkadMin, p.NearRestrictions, p.HasPermit)
	fs, err := s.facilities.Facets(ctx, f)
	if err != nil {
		return nil, err
	}
	conv := func(in []facility.FacetValue) []gen.FacetValue {
		out := make([]gen.FacetValue, len(in))
		for i, v := range in {
			out[i] = gen.FacetValue{Value: v.Value, Label: v.Label, Count: v.Count}
		}
		return out
	}
	return &gen.FacilityFacets{
		Types: conv(fs.Types), Okrugs: conv(fs.Okrugs), Rings: conv(fs.Rings), Confidence: conv(fs.Confidence),
	}, nil
}

func (s *Server) GetFacility(ctx context.Context, p gen.GetFacilityParams) (*gen.FacilityCard, error) {
	c, err := s.facilities.Get(ctx, p.FacilityId)
	if err != nil {
		return nil, err
	}
	return facilityCard(c)
}

func (s *Server) CompareFacilities(ctx context.Context, p gen.CompareFacilitiesParams) ([]gen.FacilityCard, error) {
	cards, err := s.facilities.Compare(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := make([]gen.FacilityCard, len(cards))
	for i, c := range cards {
		v, err := facilityCard(c)
		if err != nil {
			return nil, err
		}
		out[i] = *v
	}
	return out, nil
}

func optStr(v *string) gen.OptString {
	if v == nil {
		return gen.OptString{}
	}
	return gen.NewOptString(*v)
}

func optNum(v *float64) gen.OptFloat64 {
	if v == nil {
		return gen.OptFloat64{}
	}
	return gen.NewOptFloat64(*v)
}

func facilityItem(it facility.Item) gen.FacilityItem {
	out := gen.FacilityItem{
		ID: it.ID, Name: optStr(it.Name), Type: it.Type, TypeName: it.TypeName, Address: optStr(it.Address),
		Okrug: optStr(it.Okrug), District: optStr(it.District), BuildingAreaM2: optNum(it.BuildingM2),
		FloorAreaM2: optNum(it.FloorM2), TerritoryAreaM2: optNum(it.TerritoryM2), Buildings: it.Buildings,
		Confidence: it.Confidence, MkadMin: optNum(it.MkadMin), HasPermit: it.HasPermit,
		Point: gen.LngLat{Lon: it.Lon, Lat: it.Lat}, FirstSeen: it.FirstSeen,
	}
	if it.Ring != nil {
		out.Ring = gen.NewOptFacilityItemRing(gen.FacilityItemRing(*it.Ring))
	}
	return out
}

// rawObject — объект JSON как набор необработанных значений полей.
func rawObject(b json.RawMessage) (map[string]jx.Raw, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	out := make(map[string]jx.Raw, len(m))
	for k, v := range m {
		out[k] = jx.Raw(v)
	}
	return out, nil
}

func facilityCard(c *facility.Card) (*gen.FacilityCard, error) {
	attrs, err := rawObject(c.Attrs)
	if err != nil {
		return nil, fmt.Errorf("attrs: %w", err)
	}
	var reasons []gen.ConfidenceReason
	if err := json.Unmarshal(c.ConfidenceReasons, &reasons); err != nil {
		return nil, fmt.Errorf("confidence reasons: %w", err)
	}
	out := &gen.FacilityCard{
		Facility: facilityItem(c.Item), Status: gen.FacilityCardStatus(c.Status), OsmType: gen.FacilityCardOsmType(c.OSMType),
		OsmId: c.OSMID, Attrs: gen.FacilityCardAttrs(attrs), ConfidenceReasons: reasons, LastSeen: c.LastSeen,
		UpdatedAt: c.UpdatedAt, Sources: []gen.FacilitySource{}, Access: []gen.FacilityAccess{},
		Restrictions: []gen.NearbyRestriction{}, Works: []gen.NearbyWork{}, Nearby: []gen.NearbyPoi{},
	}
	if out.Attrs == nil {
		out.Attrs = gen.FacilityCardAttrs{}
	}
	if out.ConfidenceReasons == nil {
		out.ConfidenceReasons = []gen.ConfidenceReason{}
	}
	if c.Levels != nil {
		out.Levels = gen.NewOptInt(*c.Levels)
	}
	if g, err := rawObject(c.Footprint); err != nil {
		return nil, err
	} else if g != nil {
		out.Footprint = gen.NewOptFacilityCardFootprint(g)
	}
	if g, err := rawObject(c.Territory); err != nil {
		return nil, err
	} else if g != nil {
		out.Territory = gen.NewOptFacilityCardTerritory(g)
	}
	for _, v := range c.Sources {
		details, err := rawObject(v.Details)
		if err != nil {
			return nil, err
		}
		if details == nil {
			details = map[string]jx.Raw{}
		}
		out.Sources = append(out.Sources, gen.FacilitySource{
			Relation: gen.FacilitySourceRelation(v.Relation), Ref: v.Ref, Details: details,
		})
	}
	for _, v := range c.Access {
		out.Access = append(out.Access, gen.FacilityAccess{
			Profile: v.Profile, ProfileName: v.ProfileName, Target: gen.FacilityAccessTarget(v.Target),
			TargetName: optStr(v.TargetName), Minutes: optNum(v.Minutes), Km: optNum(v.Km),
		})
	}
	for _, v := range c.Restrictions {
		out.Restrictions = append(out.Restrictions, gen.NearbyRestriction{
			Kind: v.Kind, Value: optNum(v.Value), Condition: optStr(v.Condition), Street: optStr(v.Street),
			Origins: v.Origins, DistanceM: v.DistanceM,
		})
	}
	for _, v := range c.Works {
		w := gen.NearbyWork{
			ID: v.ID, Title: v.Title, Kind: gen.NearbyWorkKind(v.Kind), State: gen.NearbyWorkState(v.State),
			Closure: v.Closure, DistanceM: v.DistanceM,
		}
		if v.Starts != nil {
			w.Starts = gen.NewOptDate(*v.Starts)
		}
		if v.Ends != nil {
			w.Ends = gen.NewOptDate(*v.Ends)
		}
		out.Works = append(out.Works, w)
	}
	for _, v := range c.Nearby {
		out.Nearby = append(out.Nearby, gen.NearbyPoi{
			Kind: v.Kind, Name: optStr(v.Name), DistanceM: v.DistanceM, Point: gen.LngLat{Lon: v.Lon, Lat: v.Lat},
		})
	}
	return out, nil
}
