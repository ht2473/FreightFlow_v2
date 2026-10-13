package routing

import (
	"testing"
	"time"
)

// rules — правила зон в том виде, как они заданы в миграции (regulation.zone_rule).
func rules() []ZoneRule {
	since := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	all := []string{"mkad", "ttk", "sk"}
	inner := []string{"ttk", "sk"}
	return []ZoneRule{
		{Code: "ttk_eco", Zone: "ttk", Effect: "prohibited", EcoClassMin: 3, From: -1, To: -1, ValidFrom: since(2017, 1, 1), Confirmed: true},
		{Code: "mkad_eco", Zone: "mkad", Effect: "prohibited", EcoClassMin: 2, From: -1, To: -1, ValidFrom: since(2017, 1, 1), Confirmed: true},
		{Code: "mkad_gross_day", Zone: "mkad", Effect: "pass_required", MassBasis: "gross", MassOverT: 3.5, From: 420, To: 1380, PassKind: "day", PassZones: all, ValidFrom: since(2021, 5, 5), Confirmed: true},
		{Code: "mkad_gross_night", Zone: "mkad", Effect: "pass_required", MassBasis: "gross", MassOverT: 3.5, From: 1380, To: 420, PassKind: "night", PassZones: all, ValidFrom: since(2022, 1, 1), Confirmed: true},
		{Code: "ttk_gross_day", Zone: "ttk", Effect: "pass_required", MassBasis: "gross", MassOverT: 3.5, From: 420, To: 1380, PassKind: "day", PassZones: inner, ValidFrom: since(2021, 5, 5), Confirmed: true},
		{Code: "ttk_payload_day", Zone: "ttk", Effect: "pass_required", MassBasis: "payload", MassOverT: 1, From: 420, To: 1380, PassKind: "day", PassZones: inner, ValidFrom: since(2021, 5, 5)},
		{Code: "sk_gross_day", Zone: "sk", Effect: "pass_required", MassBasis: "gross", MassOverT: 3.5, From: 420, To: 1380, PassKind: "day", PassZones: []string{"sk"}, ValidFrom: since(2021, 5, 5)},
		{Code: "mkad_frame", Zone: "mkad", Effect: "frame_only", MassBasis: "gross", MassOverT: 2.5, From: -1, To: -1, Confirmed: true},
	}
}

func at(hour int) time.Time { return time.Date(2026, 10, 14, hour, 0, 0, 0, Moscow) }

func forbidden(ds []ZoneDecision) map[string]bool {
	out := map[string]bool{}
	for _, d := range ds {
		if d.Forbidden {
			out[d.Zone] = true
		}
	}
	return out
}

func TestDecide(t *testing.T) {
	t.Parallel()
	truck := Vehicle{GrossT: 20, PayloadT: 10, EcoClass: 5}
	cases := []struct {
		name   string
		v      Vehicle
		passes []Pass
		at     time.Time
		want   map[string]bool
	}{
		{"20 т без пропуска днём", truck, nil, at(10), map[string]bool{"mkad": true, "ttk": true, "sk": true}},
		{"дневной пропуск зоны МКАД", truck, []Pass{{Zone: "mkad", Kind: "day"}}, at(10), map[string]bool{"ttk": true, "sk": true}},
		{"дневной пропуск зоны СК — всё внутри МКАД", truck, []Pass{{Zone: "sk", Kind: "day"}}, at(10), map[string]bool{}},
		{"дневной пропуск ночью не действует", truck, []Pass{{Zone: "sk", Kind: "day"}}, at(2), map[string]bool{"mkad": true}},
		{"ночной пропуск ночью", truck, []Pass{{Zone: "mkad", Kind: "night"}}, at(23), map[string]bool{}},
		{"фургон 3 т с грузоподъёмностью 1,2 т", Vehicle{GrossT: 3, PayloadT: 1.2, EcoClass: 5}, nil, at(10), map[string]bool{"ttk": true}},
		{
			"евро-2 с пропусками — в ТТК нельзя",
			Vehicle{GrossT: 20, PayloadT: 10, EcoClass: 2},
			[]Pass{{Zone: "sk", Kind: "day"}},
			at(10),
			map[string]bool{"ttk": true},
		},
		{"легковой фургон 2 т", Vehicle{GrossT: 2, PayloadT: 0.8, EcoClass: 5}, nil, at(10), map[string]bool{}},
	}
	for _, c := range cases {
		got := forbidden(Decide(rules(), c.v, c.passes, c.at))
		if len(got) != len(c.want) {
			t.Errorf("%s: forbidden %v, want %v", c.name, got, c.want)
			continue
		}
		for z := range c.want {
			if !got[z] {
				t.Errorf("%s: forbidden %v, want %v", c.name, got, c.want)
			}
		}
	}
}

func TestDecideUnconfirmedAndUnknown(t *testing.T) {
	t.Parallel()
	// Грузоподъёмность не указана — правило 1 т считается сработавшим для ТС от 2,5 т; оно не
	// подтверждено, решение по ТТК помечается.
	ds := Decide(rules(), Vehicle{GrossT: 3, EcoClass: 5}, nil, at(12))
	for _, d := range ds {
		if d.Zone == "ttk" && (!d.Forbidden || !d.Unconfirmed) {
			t.Errorf("ttk: %+v", d)
		}
		if d.Zone == "mkad" && d.Forbidden {
			t.Errorf("mkad: %+v", d)
		}
	}
	// Если зону закрывает и подтверждённое правило, решение подтверждено.
	for _, d := range Decide(rules(), Vehicle{GrossT: 20, PayloadT: 10, EcoClass: 5}, nil, at(12)) {
		if d.Zone == "ttk" && (!d.Forbidden || d.Unconfirmed) {
			t.Errorf("ttk with confirmed rule: %+v", d)
		}
	}
	// Экокласс не указан — правило показывается, но зону не закрывает.
	ds = Decide(rules(), Vehicle{GrossT: 20, PayloadT: 10}, []Pass{{Zone: "sk", Kind: "day"}}, at(12))
	for _, d := range ds {
		if d.Forbidden {
			t.Errorf("%s forbidden with unknown eco class: %+v", d.Zone, d)
		}
		if d.Zone == "ttk" && (len(d.Rules) == 0 || d.Rules[0].Note != "eco_unknown") {
			t.Errorf("ttk eco rule not shown: %+v", d.Rules)
		}
	}
	// До начала действия правила (2021) пропуск для 3,5–12 т не требовался.
	ds = Decide(rules(), Vehicle{GrossT: 10, PayloadT: 5, EcoClass: 5}, nil, time.Date(2020, 6, 1, 10, 0, 0, 0, Moscow))
	if f := forbidden(ds); len(f) != 0 {
		t.Errorf("2020: %v", f)
	}
}

func TestInHoursAcrossMidnight(t *testing.T) {
	t.Parallel()
	night := ZoneRule{From: 1380, To: 420}
	for m, want := range map[int]bool{1380: true, 0: true, 419: true, 420: false, 720: false} {
		if inHours(night, m) != want {
			t.Errorf("minute %d: %v", m, !want)
		}
	}
}
