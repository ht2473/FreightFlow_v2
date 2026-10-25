package search

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

var (
	sevao      = Area{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Kind: "okrug", Name: "Юго-Восточный административный округ", ShortName: "ЮВАО"}
	nizhegor   = Area{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Kind: "district", Name: "Нижегородский район"}
	khamovniki = Area{ID: uuid.MustParse("00000000-0000-0000-0000-000000000003"), Kind: "district", Name: "район Хамовники"}
	maryina    = Area{ID: uuid.MustParse("00000000-0000-0000-0000-000000000004"), Kind: "district", Name: "район Марьина Роща"}
	testParser = NewParser([]Area{sevao, nizhegor, khamovniki, maryina})
)

func chips(p Parsed) []string {
	out := make([]string, len(p.Chips))
	for i, c := range p.Chips {
		out[i] = c.Label
	}
	return out
}

func TestParseFacilityFilters(t *testing.T) {
	t.Parallel()
	p := testParser.Parse("склады ЮВАО больше 10 000 м² у МКАД")
	if !slices.Equal(p.Types, []string{"warehouse"}) || !slices.Equal(p.Okrugs, []uuid.UUID{sevao.ID}) ||
		p.MinArea == nil || *p.MinArea != 10000 || p.MaxMkadMin == nil || *p.MaxMkadMin != 10 || p.Text != "" {
		t.Fatalf("разбор: %+v", p)
	}
	if want := []string{"До МКАД ≤ 10 мин", "Площадь ≥ 10 000 м²", "Склад", "ЮВАО"}; !slices.Equal(chips(p), want) {
		t.Errorf("чипы %q, want %q", chips(p), want)
	}
}

func TestParseCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		q     string
		check func(p Parsed) bool
	}{
		{"логистические центры в Хамовниках", func(p Parsed) bool {
			return slices.Equal(p.Types, []string{"logistics_center"}) && slices.Equal(p.Districts, []uuid.UUID{khamovniki.ID}) && p.Text == ""
		}},
		{"РЦ Нижегородский район до 2 га", func(p Parsed) bool {
			return slices.Equal(p.Types, []string{"logistics_center"}) && slices.Equal(p.Districts, []uuid.UUID{nizhegor.ID}) &&
				p.MaxArea != nil && *p.MaxArea == 20000
		}},
		{"холодильные склады в Марьиной Роще", func(p Parsed) bool {
			return slices.Equal(p.Types, []string{"cold_storage"}) && slices.Equal(p.Districts, []uuid.UUID{maryina.ID})
		}},
		{"склады не более 5000 кв.м за МКАД", func(p Parsed) bool {
			return p.MaxArea != nil && *p.MaxArea == 5000 && slices.Equal(p.Rings, []string{"ckad", "outside"})
		}},
		{"терминалы до 30 минут до МКАД без ограничений", func(p Parsed) bool {
			return p.MaxMkadMin != nil && *p.MaxMkadMin == 30 && p.NearRestrictions != nil && !*p.NearRestrictions && p.Text == ""
		}},
		{"склады в 5 км от Курского вокзала", func(p Parsed) bool {
			return p.NearText == "курского вокзала" && p.NearKm == 5
		}},
		{"оптовые базы рядом с Люблино", func(p Parsed) bool {
			return slices.Equal(p.Types, []string{"wholesale"}) && p.NearText == "люблино" && p.NearKm == 2
		}},
		{"склад Вайлдберриз Подольск", func(p Parsed) bool {
			return slices.Equal(p.Types, []string{"warehouse"}) && p.Text == "вайлдберриз подольск"
		}},
		// Без фильтров объектов территория — предмет поиска, а не фильтр.
		{"Хамовники", func(p Parsed) bool { return !p.Facility() && p.Text == "хамовники" }},
		{"ул. Газгольдерная, д. 10", func(p Parsed) bool {
			return !p.Facility() && p.Text == "улица газгольдерная дом 10"
		}},
		{"55.7558, 37.6176", func(p Parsed) bool {
			return p.Coordinates != nil && p.Coordinates.Lat == 55.7558 && p.Coordinates.Lon == 37.6176
		}},
		{"37,6176; 55,7558", func(p Parsed) bool { return p.Coordinates != nil && p.Coordinates.Lat == 55.7558 }},
		{"10 000", func(p Parsed) bool { return p.Coordinates == nil && !p.Facility() }},
		{"наборы данных портала", func(p Parsed) bool { return !p.Facility() }},
		{"речной порт", func(p Parsed) bool { return slices.Equal(p.Types, []string{"port"}) && p.Text == "" }},
	}
	for _, tc := range cases {
		if p := testParser.Parse(tc.q); !tc.check(p) {
			t.Errorf("%q: %+v, чипы %q", tc.q, p, chips(p))
		}
	}
}
