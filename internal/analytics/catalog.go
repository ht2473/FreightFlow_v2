// Package analytics — показатели системы: каталог с методиками, расчёт по уровням (объект,
// район, округ, участок, город, ячейка H3) в порядке зависимостей, хранение значений с входами.
package analytics

import (
	"errors"
	"fmt"
	"slices"
)

// Уровни показателей.
const (
	LevelFacility = "facility"
	LevelDistrict = "district"
	LevelOkrug    = "okrug"
	LevelSegment  = "segment"
	LevelCity     = "city"
	LevelH3r8     = "h3_8"
	LevelH3r9     = "h3_9"
)

// Metric — показатель. Compute — запрос, возвращающий строки (level, entity, period, value,
// inputs): уровень, объект уровня, период ряда (NULL — текущее значение), значение и входы
// расчёта (jsonb). Пересчёт — при изменении версий доменов Domains или значений показателей
// DependsOn; Daily — ещё и раз в сутки (значение зависит от текущей даты).
type Metric struct {
	Code    string
	Title   string
	Unit    string
	Levels  []string
	Formula string
	// Method — методика (Markdown): что считается, из каких данных, ограничения.
	Method    string
	Domains   []string
	DependsOn []string
	Daily     bool
	Version   int
	Compute   string
}

// ErrUnknown — неизвестный показатель.
var ErrUnknown = errors.New("analytics: unknown metric")

// Catalog — все показатели.
func Catalog() []Metric {
	var out []Metric
	out = append(out, facilityMetrics...)
	out = append(out, territoryMetrics...)
	out = append(out, segmentMetrics...)
	out = append(out, cityMetrics...)
	out = append(out, h3Metrics...)
	return out
}

// Find — показатель по коду.
func Find(code string) (Metric, bool) {
	for _, m := range Catalog() {
		if m.Code == code {
			return m, true
		}
	}
	return Metric{}, false
}

// ordered — показатели в порядке зависимостей (зависимые — после тех, от кого зависят).
func ordered(list []Metric) ([]Metric, error) {
	byCode := map[string]Metric{}
	for _, m := range list {
		byCode[m.Code] = m
	}
	var out []Metric
	state := map[string]int{} // 1 — обходится, 2 — готов
	var visit func(code string, path []string) error
	visit = func(code string, path []string) error {
		switch state[code] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("analytics: cycle %v", append(path, code))
		}
		m, ok := byCode[code]
		if !ok {
			return fmt.Errorf("%w: %s (зависимость %v)", ErrUnknown, code, path)
		}
		state[code] = 1
		for _, d := range m.DependsOn {
			if err := visit(d, append(path, code)); err != nil {
				return err
			}
		}
		state[code] = 2
		out = append(out, m)
		return nil
	}
	codes := make([]string, 0, len(list))
	for _, m := range list {
		codes = append(codes, m.Code)
	}
	slices.Sort(codes)
	for _, c := range codes {
		if err := visit(c, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}
