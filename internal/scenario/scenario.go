// Package scenario — сценарии «что если» (перекрытия, правила зон, профиль ТС, новый объект),
// анализ устойчивости сети и оценка локации.
package scenario

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Виды целей доступности (analytics.accessibility_target).
var targetKinds = []string{"mkad", "ttk", "center", "rail_freight"}

// Зоны пропусков от внутренней к внешней.
var zones = []string{"sk", "ttk", "mkad"}

// Пределы параметров сценария.
const (
	maxClosures            = 20
	maxIsochroneFacilities = 5
	maxIsochroneContours   = 4
	maxPolygonPoints       = 500
	maxClosureSegments     = 100
)

// Виды перекрытий.
const (
	ClosureSegments = "segments" // участки сети (линии OSM)
	ClosureElement  = "element"  // мост или участок из рейтинга критичности
	ClosureArea     = "area"     // область (многоугольник)
)

// Closure — перекрытие: участки, элемент рейтинга критичности или область.
type Closure struct {
	Kind     string       `json:"kind"`
	Label    string       `json:"label,omitempty"`
	Segments []uuid.UUID  `json:"segments,omitempty"`
	Element  *int         `json:"element,omitempty"`
	Polygon  [][2]float64 `json:"polygon,omitempty"`
}

// NewFacility — гипотетический объект: точка и название.
type NewFacility struct {
	Name string  `json:"name"`
	Lon  float64 `json:"lon"`
	Lat  float64 `json:"lat"`
}

// Pass — пропуск: зона и время действия (day, night).
type Pass struct {
	Zone string `json:"zone"`
	Kind string `json:"kind"`
}

// Params — параметры сценария.
type Params struct {
	Profile string `json:"profile"`
	// Zones — учитывать правила зон пропусков (база и сценарий); без них — физическая доступность
	// по сети с ограничениями участков, как в показателях доступности.
	Zones  bool       `json:"zones"`
	Passes []Pass     `json:"passes,omitempty"`
	Depart *time.Time `json:"depart,omitempty"`
	// ZoneOverrides — решение по зоне в сценарии: true — открыта для профиля, false — закрыта.
	ZoneOverrides map[string]bool `json:"zoneOverrides,omitempty"`
	Closures      []Closure       `json:"closures,omitempty"`
	NewFacility   *NewFacility    `json:"newFacility,omitempty"`
	Targets       []string        `json:"targets"`
	// IsochroneMinutes — контуры изохрон; IsochroneFacilities — объекты реестра, от которых они
	// строятся (и от нового объекта, если он задан).
	IsochroneMinutes    []int       `json:"isochroneMinutes,omitempty"`
	IsochroneFacilities []uuid.UUID `json:"isochroneFacilities,omitempty"`
}

// ErrInvalid — неверные параметры сценария; текст — для пользователя.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return "scenario: " + e.Reason }

func invalid(format string, a ...any) error { return &ErrInvalid{Reason: fmt.Sprintf(format, a...)} }

// moscowBounds — охват расчёта (Москва и область): точки сценария вне его — ошибка.
var moscowBounds = [4]float64{35.1, 54.9, 38.9, 56.5}

func inBounds(lon, lat float64) bool {
	b := moscowBounds
	return lon >= b[0] && lon <= b[2] && lat >= b[1] && lat <= b[3]
}

// Validate проверяет параметры и приводит их к каноническому виду (цели по порядку, контуры по
// возрастанию). Сценарий должен что-то менять: перекрытие, решение по зоне или новый объект.
func (p *Params) Validate() error {
	if p.Profile == "" {
		return invalid("не выбран профиль ТС")
	}
	if len(p.Targets) == 0 {
		p.Targets = []string{"mkad"}
	}
	for _, t := range p.Targets {
		if !slices.Contains(targetKinds, t) {
			return invalid("неизвестная цель доступности %q", t)
		}
	}
	p.Targets = canonical(p.Targets, targetKinds)
	for _, ps := range p.Passes {
		if !slices.Contains(zones, ps.Zone) || (ps.Kind != "day" && ps.Kind != "night") {
			return invalid("неверный пропуск %s/%s", ps.Zone, ps.Kind)
		}
	}
	if len(p.Passes) > 0 && !p.Zones {
		return invalid("пропуска действуют только с учётом правил зон")
	}
	for z := range p.ZoneOverrides {
		if !slices.Contains(zones, z) {
			return invalid("неизвестная зона %q", z)
		}
	}
	if len(p.ZoneOverrides) > 0 && !p.Zones {
		return invalid("изменение правил зон требует учёта правил зон")
	}
	if len(p.Closures) > maxClosures {
		return invalid("перекрытий больше %d", maxClosures)
	}
	for i, c := range p.Closures {
		if err := c.validate(); err != nil {
			return invalid("перекрытие %d: %s", i+1, err)
		}
	}
	if f := p.NewFacility; f != nil {
		if !inBounds(f.Lon, f.Lat) {
			return invalid("новый объект вне Москвы и области")
		}
		if f.Name == "" {
			f.Name = "Новый объект"
		}
	}
	if len(p.IsochroneFacilities) > maxIsochroneFacilities {
		return invalid("изохроны строятся не больше чем от %d объектов", maxIsochroneFacilities)
	}
	if len(p.IsochroneMinutes) == 0 && (len(p.IsochroneFacilities) > 0 || p.NewFacility != nil) {
		p.IsochroneMinutes = []int{15, 30, 45}
	}
	if len(p.IsochroneMinutes) > maxIsochroneContours {
		return invalid("контуров изохрон больше %d", maxIsochroneContours)
	}
	for _, m := range p.IsochroneMinutes {
		if m < 5 || m > 90 {
			return invalid("контур изохроны %d мин вне 5–90", m)
		}
	}
	slices.Sort(p.IsochroneMinutes)
	p.IsochroneMinutes = slices.Compact(p.IsochroneMinutes)
	if len(p.Closures) == 0 && len(p.ZoneOverrides) == 0 && p.NewFacility == nil {
		return invalid("сценарий ничего не меняет: задайте перекрытие, правило зоны или новый объект")
	}
	return nil
}

func (c *Closure) validate() error {
	switch c.Kind {
	case ClosureSegments:
		if len(c.Segments) == 0 || len(c.Segments) > maxClosureSegments {
			return fmt.Errorf("участков должно быть от 1 до %d", maxClosureSegments)
		}
	case ClosureElement:
		if c.Element == nil || *c.Element < 0 {
			return errors.New("не указан элемент сети")
		}
	case ClosureArea:
		n := len(c.Polygon)
		if n < 4 || n > maxPolygonPoints {
			return fmt.Errorf("в области должно быть от 4 до %d точек", maxPolygonPoints)
		}
		if c.Polygon[0] != c.Polygon[n-1] {
			return errors.New("контур области не замкнут")
		}
		for _, pt := range c.Polygon {
			if !inBounds(pt[0], pt[1]) {
				return errors.New("область вне Москвы и области")
			}
		}
	default:
		return fmt.Errorf("неизвестный вид перекрытия %q", c.Kind)
	}
	return nil
}

// canonical — значения list в порядке order без повторов.
func canonical(list, order []string) []string {
	var out []string
	for _, v := range order {
		if slices.Contains(list, v) {
			out = append(out, v)
		}
	}
	return out
}

// Статусы сценария.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Progress — ход расчёта.
type Progress struct {
	Stage string `json:"stage,omitempty"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Scenario — сценарий пользователя.
type Scenario struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	Name         string
	Description  string
	Params       Params
	Status       string
	Progress     Progress
	Summary      *Summary
	Error        *string
	GraphBuiltAt *time.Time
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

// Summary — итог сценария.
type Summary struct {
	Targets  []TargetSummary `json:"targets"`
	Closures []ClosureInfo   `json:"closures"`
	// NewFacility — время нового объекта до целей (база и сценарий).
	NewFacility []EntityTime `json:"newFacility,omitempty"`
	Isochrones  []IsoSummary `json:"isochrones,omitempty"`
	// Zones — решения по зонам базы и сценария (учёт правил зон).
	Zones []ZoneChange `json:"zones,omitempty"`
	// BaseReused — база взята из показателей доступности (тот же граф, без правил зон).
	BaseReused bool `json:"baseReused"`
	Facilities int  `json:"facilities"`
	DurationMs int  `json:"durationMs"`
}

// TargetSummary — изменения доступности одного вида целей.
type TargetSummary struct {
	Kind string `json:"kind"`
	// Affected — объекты с приростом времени; Improved — с сокращением; Lost — потеряли доступ;
	// Gained — получили; Blocked — в закрытой зоне в сценарии.
	Affected int `json:"affected"`
	Improved int `json:"improved"`
	Lost     int `json:"lost"`
	Gained   int `json:"gained"`
	Blocked  int `json:"blocked"`
	// DelayMin — суммарный прирост времени (объекто-минуты), MeanDelay и MaxDelay — средний и
	// наибольший у затронутых; BaseMedian и ScenarioMedian — медианы времени достижимых объектов.
	DelayMin       float64 `json:"delayMin"`
	MeanDelay      float64 `json:"meanDelay"`
	MaxDelay       float64 `json:"maxDelay"`
	BaseMedian     float64 `json:"baseMedian"`
	ScenarioMedian float64 `json:"scenarioMedian"`
}

// ClosureInfo — описание перекрытия для итога.
type ClosureInfo struct {
	Kind     string  `json:"kind"`
	Label    string  `json:"label"`
	Segments int     `json:"segments"`
	LengthKm float64 `json:"lengthKm"`
	AreaKm2  float64 `json:"areaKm2,omitempty"`
	Excluded int     `json:"excluded"`
}

// EntityTime — время до цели: база и сценарий (nil — недостижимо или в закрытой зоне).
type EntityTime struct {
	Kind           string   `json:"kind"`
	BaseMin        *float64 `json:"baseMin"`
	ScenarioMin    *float64 `json:"scenarioMin"`
	BaseStatus     string   `json:"baseStatus"`
	ScenarioStatus string   `json:"scenarioStatus"`
}

// IsoSummary — площадь изохроны базы и сценария.
type IsoSummary struct {
	Origin      string  `json:"origin"`
	Name        string  `json:"name"`
	Minutes     int     `json:"minutes"`
	BaseKm2     float64 `json:"baseKm2"`
	ScenarioKm2 float64 `json:"scenarioKm2"`
}

// ZoneChange — решение по зоне: закрыта ли она для профиля в базе и в сценарии.
type ZoneChange struct {
	Zone           string `json:"zone"`
	BaseClosed     bool   `json:"baseClosed"`
	ScenarioClosed bool   `json:"scenarioClosed"`
}
