package routing

import (
	"testing"

	"github.com/paulmach/orb"
)

var truck12 = Profile{Code: "truck_12", GrossT: 12, AxleLoadT: 8, HeightM: 3.6, WidthM: 2.55, LengthM: 9}

func value(v float64) *float64 { return &v }

func TestBuildNetwork(t *testing.T) {
	t.Parallel()
	ways := []Way{
		// Улица A–B–C и поперечная D–B–E с общей точкой B.
		{Class: "primary", Oneway: 1, Line: orb.LineString{{37.60, 55.70}, {37.61, 55.70}, {37.62, 55.70}}},
		{Class: "secondary", Line: orb.LineString{{37.61, 55.69}, {37.61, 55.70}, {37.61, 55.71}}},
		// Мост над участком A–B без общей точки — не перекрёсток.
		{Class: "trunk", Line: orb.LineString{{37.605, 55.69}, {37.605, 55.71}}},
	}
	n := BuildNetwork(ways)
	if len(n.Nodes) != 7 || len(n.Edges) != 5 {
		t.Fatalf("nodes %d, edges %d", len(n.Nodes), len(n.Edges))
	}
	if e := n.Edges[0]; len(e.Line) != 2 || e.LengthM < 600 || e.LengthM > 650 {
		t.Errorf("first edge %+v", e)
	}
	g, dirs := n.Digraph(truck12, false)
	// Односторонняя улица — по дуге на ребро, остальные — по две.
	if len(g.arcs) != 2+2*3 {
		t.Errorf("arcs %d", len(g.arcs))
	}
	if !dirs[0].Forward.Open || dirs[0].Backward.ClosedBy != "oneway" {
		t.Errorf("oneway directions %+v", dirs[0])
	}
	// Время: 0,63 км по главной улице со скоростью 45 км/ч — около 50 с.
	if c := g.arcs[0].cost; c < 480 || c > 520 {
		t.Errorf("cost %d", c)
	}
	// Мост — отдельная компонента: из 7 узлов в наибольшей сильно связной — 3 (D, B, E).
	if lc := g.LargestComponent(); lc < 3.0/7-1e-9 || lc > 3.0/7+1e-9 {
		t.Errorf("largest component %.3f", lc)
	}
}

func TestWayPass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		limit Limit
		dir   int
		want  Passage
	}{
		{"height below", Limit{Kind: "maxheight", Value: value(3.5)}, 1, Passage{ClosedBy: "maxheight", DimsOnly: true, Value: 3.5}},
		{"height above", Limit{Kind: "maxheight", Value: value(4)}, 1, Passage{Open: true}},
		{"weight unknown", Limit{Kind: "maxweight"}, 1, Passage{Open: true}},
		{"other direction", Limit{Kind: "maxweight", Value: value(5), Direction: -1}, 1, Passage{Open: true}},
		{"ban", Limit{Kind: "hgv_ban"}, 1, Passage{ClosedBy: "hgv_ban"}},
		{"ban by time", Limit{Kind: "hgv_ban", Condition: "время по табличке 8.5.4"}, 1, Passage{Open: true}},
		{"ban by unknown mass", Limit{Kind: "hgv_ban", Condition: "по разрешённой максимальной массе (табличка 8.11)"}, 1, Passage{Open: true}},
		{"ban above mass", Limit{Kind: "hgv_ban", Condition: "weight>8"}, 1, Passage{ClosedBy: "hgv_ban"}},
		{"ban entry", Limit{Kind: "hgv_ban", Condition: "въезд"}, 1, Passage{Open: true, Destination: true}},
		{"destination above mass", Limit{Kind: "hgv_destination", Condition: "maxweight>2.5"}, 1, Passage{Open: true, Destination: true}},
		{"destination heavier only", Limit{Kind: "hgv_destination", Condition: "weight>12"}, 1, Passage{Open: true}},
		{"destination at night", Limit{Kind: "hgv_destination", Condition: "Mo-Su 00:00-08:00 AND weight>3.5"}, 1, Passage{Open: true}},
		{"hazmat", Limit{Kind: "hazmat_ban"}, 1, Passage{Open: true}},
	}
	for _, c := range cases {
		w := Way{Class: "primary", Limits: []Limit{c.limit}}
		if got := w.Pass(c.dir, truck12); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	// Запрет сильнее предела габарита; из пределов причиной называется высота.
	w := Way{Class: "primary", Limits: []Limit{
		{Kind: "maxweight", Value: value(10)}, {Kind: "maxheight", Value: value(3)}, {Kind: "hgv_ban"},
	}}
	if got := w.Pass(1, truck12); got.ClosedBy != "hgv_ban" || got.DimsOnly {
		t.Errorf("ban with limits: %+v", got)
	}
	w.Limits = w.Limits[:2]
	w.Limits = append(w.Limits, Limit{Kind: "maxheight", Value: value(2.8)})
	if got := w.Pass(1, truck12); got.ClosedBy != "maxheight" || !got.DimsOnly || got.Value != 2.8 {
		t.Errorf("limits: %+v", got)
	}
}

func TestDigraphIgnoreDims(t *testing.T) {
	t.Parallel()
	ways := []Way{
		{Class: "primary", Line: orb.LineString{{37.60, 55.70}, {37.61, 55.70}}, Limits: []Limit{{Kind: "maxheight", Value: value(3)}}},
		{Class: "primary", Line: orb.LineString{{37.61, 55.70}, {37.62, 55.70}}, Limits: []Limit{{Kind: "hgv_ban"}}},
		{Class: "residential", Line: orb.LineString{{37.62, 55.70}, {37.63, 55.70}}, Limits: []Limit{{Kind: "hgv_destination"}}},
	}
	n := BuildNetwork(ways)
	g, _ := n.Digraph(truck12, false)
	free, _ := n.Digraph(truck12, true)
	// Профиль: только участок «для подъезда» (с увеличенным временем); без габаритов — и участок
	// под путепроводом; запрет остаётся.
	if len(g.arcs) != 2 || len(free.arcs) != 4 {
		t.Fatalf("arcs %d, free %d", len(g.arcs), len(free.arcs))
	}
	plain := BuildNetwork([]Way{{Class: "residential", Line: ways[2].Line}})
	pg, _ := plain.Digraph(truck12, false)
	if d := g.arcs[0].cost - destinationPenalty*pg.arcs[0].cost; d < -destinationPenalty || d > destinationPenalty {
		t.Errorf("destination cost %d, plain %d", g.arcs[0].cost, pg.arcs[0].cost)
	}
}
