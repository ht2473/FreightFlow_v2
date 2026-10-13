package transport

import (
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseStreets(t *testing.T) {
	t.Parallel()
	streets, err := ParseStreets(read(t, "ulitsy.html"))
	if err != nil {
		t.Fatal(err)
	}
	// Девять округов по три пункта во фрагменте, в одном пункте две улицы; меню сайта, боковая
	// колонка и подвал не попадают.
	if len(streets) != 28 {
		t.Fatalf("streets = %d, want 28: %+v", len(streets), streets)
	}
	first := streets[0]
	if first.Okrug != "Центральный административный округ" || first.Entry != "1-й Красногвардейский проезд" {
		t.Errorf("first = %+v", first)
	}
	okrugs := map[string]int{}
	for _, s := range streets {
		okrugs[s.Okrug]++
		if strings.HasSuffix(s.Entry, ";") || s.Entry != strings.TrimSpace(s.Entry) {
			t.Errorf("entry not trimmed: %q", s.Entry)
		}
		for _, bad := range []string{"Пассажирам", "Парковки для грузовиков", "Подвал"} {
			if s.Entry == bad {
				t.Errorf("entry from page chrome: %q", s.Entry)
			}
		}
	}
	if len(okrugs) != 9 || okrugs["Юго-Восточный административный округ"] != 3 || okrugs["Северо-западный административный округ"] != 4 {
		t.Errorf("okrugs = %v", okrugs)
	}
	// В Восточном округе строки перечня заканчиваются точкой с запятой.
	found := false
	for _, s := range streets {
		if s.Okrug == "Восточный административный округ" && s.Entry == "МКАД" {
			found = true
		}
	}
	if !found {
		t.Errorf("no «МКАД» in Восточный округ: %+v", streets)
	}
	if e := streets[14].Entry; e != "Волоколамский проезд" || !strings.HasPrefix(streets[15].Entry, "Волоколамское шоссе (исключая") {
		t.Errorf("two streets in one item not split: %q, %q", e, streets[15].Entry)
	}
}

func TestParsePage(t *testing.T) {
	t.Parallel()
	raw := read(t, "perevod_ckad.html")
	p, err := ParsePage("tm-cargo-pass", "https://transport.mos.ru/gruzoviki/perevod_ckad", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Title, "С 1 января для проезда по Москве грузовикам свыше 3,5 тонн") {
		t.Errorf("title = %q", p.Title)
	}
	for _, want := range []string{"(с 7 до 23 часов)", "(с 23 до 7 часов)", "в границах МКАД включительно"} {
		if !strings.Contains(p.Content, want) {
			t.Errorf("content lacks %q", want)
		}
	}
	for _, bad := range []string{"Пассажирам", "Подвал", "Парковки для грузовиков", "В проработке", "&nbsp;", "<"} {
		if strings.Contains(p.Content, bad) {
			t.Errorf("content contains %q", bad)
		}
	}
	if len(p.Hash) != 64 {
		t.Errorf("hash = %q", p.Hash)
	}
	// Хеш зависит только от текста: другой порядок атрибутов и пробелы его не меняют.
	q, err := ParsePage(p.Code, p.URL, strings.ReplaceAll(raw, "<p>", "<p class=\"x\">  "))
	if err != nil {
		t.Fatal(err)
	}
	if q.Hash != p.Hash {
		t.Error("hash depends on markup")
	}
	if _, err := ParsePage(p.Code, p.URL, "<html><body>нет страницы</body></html>"); err == nil {
		t.Error("page without title accepted")
	}
}
