package rosstat

import (
	"net/url"
	"os"
	"slices"
	"testing"
)

// testdata — страница показателя ЕМИСС 57791 и выгрузка SDMX по Москве за 2016–2025 годы
// (13.10.2026).
func TestEmissPageAndSDMX(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile("testdata/emiss_57791.html")
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseEmissPage(string(page))
	if err != nil {
		t.Fatal(err)
	}
	if p.Unit != "тысяча тонн" || len(p.Filters) != 5 || !slices.Equal(p.Left, []string{"57831", "30611"}) ||
		!slices.Equal(p.Top, []string{"3", "33560"}) {
		t.Fatalf("page: %q %d %v %v", p.Unit, len(p.Filters), p.Left, p.Top)
	}
	q, err := p.query("57791")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := url.ParseQuery(q)
	sel := v["selectedFilterIds"]
	if !slices.Contains(sel, "57831_1688506") || slices.Contains(sel, "57831_1688487") || !slices.Contains(sel, "3_2025") {
		t.Fatalf("selected: %v", sel)
	}

	doc, err := os.ReadFile("testdata/emiss_57791_moscow.xml")
	if err != nil {
		t.Fatal(err)
	}
	obs := parseSDMX(string(doc), "57791", p.Title, p.Unit)
	if len(obs) != 10 || obs[0].Year != 2016 || obs[0].Value != 33248.2 || obs[9].Value != 55399.5 {
		t.Fatalf("observations: %+v", obs)
	}
}
