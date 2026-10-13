package rosstat

import (
	"os"
	"strings"
	"testing"
)

// testdata — ответы БД ПМО Росстата 13.10.2026 (windows-1251): форма показателя 8112027 и
// таблица численности по муниципальным образованиям Москвы за 2020–2025 годы.
func readPage(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseFormAndTable(t *testing.T) {
	t.Parallel()
	f, err := parseForm(readPage(t, "pmo_form.html"))
	if err != nil {
		t.Fatal(err)
	}
	if f.indicator != "8112027" || f.munr != "45000000" || len(f.oktmo) != 149 || f.names[0] != "Богородское" {
		t.Fatalf("form: %s %s %d %q", f.indicator, f.munr, len(f.oktmo), f.names[0])
	}
	if q := string(f.query()); !strings.Contains(q, "Pokazateli%3A8112027") || !strings.Contains(q, "45301000") {
		t.Fatalf("query: %s", q[:200])
	}

	obs, err := parseTable(readPage(t, "pmo_table.html"), f)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		if o.Name == "Москва" && o.Year == 2025 && o.Value != 13274285 {
			t.Fatalf("Москва 2025: %d", o.Value)
		}
		if o.Name == "Богородское" && o.Year == 2025 && (o.Value != 108907 || o.OKTMO != "45301000") {
			t.Fatalf("Богородское 2025: %+v", o)
		}
	}
	if len(obs) < 140*5 {
		t.Fatalf("observations: %d", len(obs))
	}
}

func TestParseTableWithoutData(t *testing.T) {
	t.Parallel()
	if _, err := parseTable("<html><body>нет</body></html>", form{}); err == nil {
		t.Fatal("want error")
	}
}
