package calendar

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata/ru<год>.txt — ответы API isdayoff.ru (cc=ru, pre=1, covid=1).
func year(t *testing.T, y int) Year {
	t.Helper()
	b, err := os.ReadFile("testdata/ru" + strconv.Itoa(y) + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return Year{Year: y, Days: strings.TrimSpace(string(b))}
}

func kindOn(t *testing.T, days []Day, month time.Month, day int) string {
	t.Helper()
	for _, d := range days {
		if d.Date.Month() == month && d.Date.Day() == day {
			return d.Kind
		}
	}
	t.Fatalf("no day %02d.%02d", day, month)
	return ""
}

func TestParseYear(t *testing.T) {
	t.Parallel()
	cases := []struct {
		year  int
		days  int
		month time.Month
		day   int
		kind  string
	}{
		{2026, 365, time.January, 1, "dayoff"},
		{2026, 365, time.January, 12, "workday"},
		// Високосный год; 7 марта — сокращённый предпраздничный день.
		{2024, 366, time.March, 7, "short"},
		{2024, 366, time.February, 29, "workday"},
		// Нерабочие оплачиваемые дни весны 2020 года.
		{2020, 366, time.April, 15, "nonworking_paid"},
	}
	for _, c := range cases {
		days, err := ParseYear(year(t, c.year))
		if err != nil {
			t.Fatal(err)
		}
		if len(days) != c.days {
			t.Errorf("%d: %d days", c.year, len(days))
		}
		if k := kindOn(t, days, c.month, c.day); k != c.kind {
			t.Errorf("%02d.%02d.%d = %s, want %s", c.day, c.month, c.year, k, c.kind)
		}
	}
}

func TestParseYearRejectsErrors(t *testing.T) {
	t.Parallel()
	y := year(t, 2026)
	// Ответ API с кодом ошибки (101 — данные не найдены) и неполный год.
	for _, bad := range []Year{{Year: 2027, Days: "101"}, {Year: 2026, Days: y.Days[:364]}, {Year: 2024, Days: y.Days}} {
		if _, err := ParseYear(bad); err == nil {
			t.Errorf("year %d with %d codes accepted", bad.Year, len(bad.Days))
		}
	}
	broken := Year{Year: 2026, Days: "9" + y.Days[1:]}
	if _, err := ParseYear(broken); err == nil {
		t.Error("unknown code accepted")
	}
}
