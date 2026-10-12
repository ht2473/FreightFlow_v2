package api

import (
	"context"

	"freightflow/internal/api/gen"
	"freightflow/internal/tiles"
)

// --- Слои карты ---

func (s *Server) ListLayers(ctx context.Context) (*gen.LayerCatalog, error) {
	list, err := s.layers.Catalog(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := &gen.LayerCatalog{Groups: make([]gen.LayerGroup, 0, len(tiles.Groups)), Layers: make([]gen.MapLayer, len(list))}
	used := map[string]bool{}
	for i, l := range list {
		out.Layers[i] = toMapLayer(l)
		used[l.Group] = true
	}
	for _, g := range tiles.Groups {
		if used[g.Code] {
			out.Groups = append(out.Groups, gen.LayerGroup{Code: g.Code, Title: g.Title})
		}
	}
	return out, nil
}

func toMapLayer(l tiles.LayerInfo) gen.MapLayer {
	st := gen.LayerStyle{Kind: gen.LayerStyleKind(l.Style.Kind), Categories: make([]gen.LayerCategory, len(l.Style.Categories))}
	for i, c := range l.Style.Categories {
		st.Categories[i] = gen.LayerCategory{Values: c.Values, Label: c.Label, Color: c.Color}
	}
	setOpt(&st.Color, l.Style.Color)
	setOpt(&st.Property, l.Style.Property)
	setOpt(&st.DashedBy, l.Style.DashedBy)
	setOpt(&st.Note, l.Style.Note)

	props := make([]gen.LayerProperty, len(l.Properties))
	for i, p := range l.Properties {
		props[i] = gen.LayerProperty{Name: p.Name, Title: p.Title, Kind: gen.LayerPropertyKind(p.Kind)}
		setOpt(&props[i].Unit, p.Unit)
		if len(p.Labels) > 0 {
			props[i].Labels = gen.NewOptLayerPropertyLabels(p.Labels)
		}
	}
	return gen.MapLayer{
		Code: l.Code, Group: l.Group, Title: l.Title, Description: l.Description,
		Geometry: gen.MapLayerGeometry(l.Geometry), Minzoom: l.MinZoom, Maxzoom: l.MaxZoom,
		Tiles: l.Tiles, Version: l.Version, Attribution: l.Attribution, Visible: l.Visible,
		Style: st, Properties: props,
	}
}

func setOpt(o *gen.OptString, v string) {
	if v != "" {
		*o = gen.NewOptString(v)
	}
}
