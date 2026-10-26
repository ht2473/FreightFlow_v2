package scenario

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/routing"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	ring := [][2]float64{{37.6, 55.7}, {37.61, 55.7}, {37.61, 55.71}, {37.6, 55.7}}
	ok := Params{
		Profile: "truck_12", Targets: []string{"ttk", "mkad", "ttk"},
		Closures:            []Closure{{Kind: ClosureArea, Polygon: ring}},
		IsochroneFacilities: []uuid.UUID{uuid.New()},
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ok.Targets, []string{"mkad", "ttk"}) {
		t.Errorf("targets = %v, want canonical [mkad ttk]", ok.Targets)
	}
	if !slices.Equal(ok.IsochroneMinutes, []int{15, 30, 45}) {
		t.Errorf("isochrone minutes = %v, want default", ok.IsochroneMinutes)
	}

	nf := Params{Profile: "p", NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}}
	if err := nf.Validate(); err != nil || nf.NewFacility.Name == "" || !slices.Equal(nf.Targets, []string{"mkad"}) {
		t.Errorf("new facility: err %v, name %q, targets %v", err, nf.NewFacility.Name, nf.Targets)
	}

	bad := map[string]Params{
		"нет профиля":            {Targets: []string{"mkad"}, NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}},
		"ничего не меняет":       {Profile: "p"},
		"цель":                   {Profile: "p", Targets: []string{"moon"}, NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}},
		"пропуск без зон":        {Profile: "p", Passes: []Pass{{"mkad", "day"}}, NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}},
		"переопределение":        {Profile: "p", ZoneOverrides: map[string]bool{"mkad": true}},
		"неизвестная зона":       {Profile: "p", Zones: true, ZoneOverrides: map[string]bool{"garden": true}},
		"объект вне области":     {Profile: "p", NewFacility: &NewFacility{Lon: 30.3, Lat: 59.9}},
		"контур не замкнут":      {Profile: "p", Closures: []Closure{{Kind: ClosureArea, Polygon: ring[:3]}}},
		"нет участков":           {Profile: "p", Closures: []Closure{{Kind: ClosureSegments}}},
		"нет элемента":           {Profile: "p", Closures: []Closure{{Kind: ClosureElement}}},
		"вид перекрытия":         {Profile: "p", Closures: []Closure{{Kind: "flood"}}},
		"контур изохроны":        {Profile: "p", NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}, IsochroneMinutes: []int{200}},
		"много объектов изохрон": {Profile: "p", NewFacility: &NewFacility{Lon: 37.5, Lat: 55.8}, IsochroneFacilities: make([]uuid.UUID, 6)},
	}
	for name, p := range bad {
		var inv *ErrInvalid
		if err := p.Validate(); !errors.As(err, &inv) || inv.Reason == "" {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	z := Params{Profile: "p", Zones: true, ZoneOverrides: map[string]bool{"mkad": true}}
	if err := z.Validate(); err != nil {
		t.Errorf("zone override: %v", err)
	}
}

func TestApplyOverrides(t *testing.T) {
	t.Parallel()
	base := []routing.ZoneDecision{
		{Zone: "sk", Forbidden: true, Rules: []routing.Applied{{Blocking: true}}},
		{Zone: "ttk", Forbidden: true},
		{Zone: "mkad", Forbidden: false},
	}
	got := applyOverrides(base, map[string]bool{"ttk": true, "mkad": false})
	if !got[0].Forbidden || got[1].Forbidden || !got[2].Forbidden || got[1].Rules != nil {
		t.Errorf("overrides = %+v", got)
	}
	if !base[1].Forbidden {
		t.Error("base decisions must not change")
	}
	if routing.ClosedZone(got) != "mkad" {
		t.Errorf("closed zone = %q, want mkad", routing.ClosedZone(got))
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	// 0: +5 мин; 1: без изменений; 2: потерял доступ; 3: получил; 4: −2 мин; 5: в закрытой зоне
	// сценария; 6: новый объект (в сводку по реестру не входит).
	base := []float64{10, 20, 30, -1, 12, 8, 1}
	scen := []float64{15, 20, -1, 25, 10, -1, 100}
	s := summarize("mkad", base, scen, map[int]bool{}, map[int]bool{5: true}, true)
	want := TargetSummary{
		Kind: "mkad", Affected: 1, Improved: 1, Lost: 2, Gained: 1, Blocked: 1,
		DelayMin: 5, MeanDelay: 5, MaxDelay: 5, BaseMedian: 12, ScenarioMedian: 17.5,
	}
	if s != want {
		t.Errorf("summary = %+v\nwant      %+v", s, want)
	}
}

func TestBatchCount(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ n, targets, want int }{
		{1588, 72, 49}, {1588, 1, 32}, {0, 72, 0}, {10, 0, 0}, {33, 72, 1},
	} {
		if got := batchCount(c.n, c.targets); got != c.want {
			t.Errorf("batchCount(%d, %d) = %d, want %d", c.n, c.targets, got, c.want)
		}
	}
}
