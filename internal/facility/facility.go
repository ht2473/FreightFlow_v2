// Package facility — реестр объектов логистики: выборка с фильтрами, фасетами и сортировкой,
// карточка объекта со связанными данными, сравнение объектов.
package facility

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound — объекта нет в реестре.
var ErrNotFound = errors.New("facility: not found")

// ErrBadFilter — неверный параметр выборки (сортировка, рамка, многоугольник).
var ErrBadFilter = errors.New("facility: bad filter")

// AccessProfile — профиль ТС, по которому в списке показывается время до МКАД.
const AccessProfile = "truck_20"

// Filter — условия выборки реестра. Пустые поля не ограничивают выборку.
type Filter struct {
	// Q — подстрока названия или адреса (полнотекстовый поиск — в разделе поиска).
	Q         string
	Types     []string
	Okrugs    []uuid.UUID
	Districts []uuid.UUID
	Rings     []string
	// MinArea, MaxArea — площадь застройки, м².
	MinArea, MaxArea *float64
	MinConfidence    *float64
	// Status — active (по умолчанию), missing или excluded.
	Status string
	// BBox — запад, юг, восток, север.
	BBox []float64
	// Polygon — GeoJSON-геометрия области.
	Polygon json.RawMessage
	Near    *Circle
	// MaxMkadMin — время до МКАД грузовика 20 т не больше, мин.
	MaxMkadMin *float64
	// NearRestrictions — запрет или предел массы и габаритов в 300 м (true — есть, false — нет).
	NearRestrictions *bool
	// HasPermit — есть разрешение на ввод в эксплуатацию.
	HasPermit *bool
}

// Circle — окрестность точки.
type Circle struct {
	Lon, Lat, RadiusM float64
}

// Item — строка реестра.
type Item struct {
	ID          uuid.UUID
	Name        *string
	Type        string
	TypeName    string
	Address     *string
	Okrug       *string
	District    *string
	Ring        *string
	BuildingM2  *float64
	FloorM2     *float64
	TerritoryM2 *float64
	Buildings   int
	Confidence  float64
	// MkadMin — время до МКАД грузовика 20 т, мин; nil — не рассчитано или недостижимо.
	MkadMin   *float64
	HasPermit bool
	Lon, Lat  float64
	FirstSeen time.Time
}

// Page — страница реестра: строки, всего по фильтру и курсор следующей страницы.
type Page struct {
	Items []Item
	Total int
	Next  string
}

// FacetValue — значение фасета с числом объектов.
type FacetValue struct {
	Value, Label string
	Count        int
}

// Facets — распределения по типу, округу, кольцу и достоверности; в каждом учитываются все
// фильтры, кроме фильтра по самому фасету.
type Facets struct {
	Types, Okrugs, Rings, Confidence []FacetValue
}

// sortColumns — поля сортировки списка.
var sortColumns = map[string]string{
	"name":       "f.name",
	"type":       "t.ord",
	"area":       "f.building_area_m2",
	"floorArea":  "f.floor_area_m2",
	"confidence": "f.confidence",
	"mkad":       "acc.minutes",
	"firstSeen":  "f.first_seen_at",
}

// DefaultSort — сначала достоверные, затем по названию.
const DefaultSort = "-confidence,name"

// orderBy разбирает сортировку «поле,-поле» в ORDER BY; пустые значения — в конце.
func orderBy(sort string) (string, error) {
	if sort == "" {
		sort = DefaultSort
	}
	var parts []string
	for _, f := range strings.Split(sort, ",") {
		dir := "ASC"
		if after, ok := strings.CutPrefix(f, "-"); ok {
			f, dir = after, "DESC"
		}
		col, ok := sortColumns[f]
		if !ok {
			return "", fmt.Errorf("%w: sort %q", ErrBadFilter, f)
		}
		parts = append(parts, col+" "+dir+" NULLS LAST")
	}
	return strings.Join(append(parts, "f.id"), ", "), nil
}

// where — условия запроса с нумерацией параметров.
type where struct {
	conds []string
	args  []any
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return fmt.Sprintf("$%d", len(w.args))
}

func (w *where) add(format string, args ...any) {
	ph := make([]any, len(args))
	for i, a := range args {
		ph[i] = w.arg(a)
	}
	w.conds = append(w.conds, fmt.Sprintf(format, ph...))
}

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return "true"
	}
	return strings.Join(w.conds, " AND ")
}

// likeEscape экранирует шаблонные символы LIKE.
var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// nearRestriction — запрет грузовиков или движения либо предел массы и габаритов без условия по
// времени на участке в 300 м от объекта.
const nearRestriction = `EXISTS (
	SELECT 1 FROM network.road_segment s JOIN network.restriction r ON r.segment_id = s.id
	WHERE s.geom && ST_Expand(f.point, 0.005) AND ST_DWithin(s.geom::geography, f.point::geography, 300)
	  AND r.kind IN ('hgv_ban', 'motor_vehicle_ban', 'maxweight', 'maxaxleload', 'maxheight', 'maxwidth', 'maxlength')
	  AND (r.condition IS NULL OR NOT network.time_based(r.condition)))`

const hasPermit = `EXISTS (SELECT 1 FROM infra.facility_source fs WHERE fs.facility_id = f.id AND fs.relation = 'permit')`

// conditions — условия фильтра; skip — фасет, собственный фильтр которого не применяется.
func (f Filter) conditions(skip string) (*where, error) {
	w := &where{}
	status := f.Status
	if status == "" {
		status = "active"
	}
	w.add("f.status = %s", status)
	if q := strings.TrimSpace(f.Q); q != "" {
		pattern := "%" + likeEscape.Replace(q) + "%"
		w.add("(f.name ILIKE %[1]s OR coalesce(f.official_address, f.address, '') ILIKE %[1]s)", pattern)
	}
	if len(f.Types) > 0 && skip != "type" {
		w.add("f.type_code = ANY (%s)", f.Types)
	}
	if len(f.Okrugs) > 0 && skip != "okrug" {
		w.add("f.okrug_id = ANY (%s)", f.Okrugs)
	}
	if len(f.Districts) > 0 {
		w.add("f.district_id = ANY (%s)", f.Districts)
	}
	if len(f.Rings) > 0 && skip != "ring" {
		w.add("f.ring = ANY (%s)", f.Rings)
	}
	if f.MinArea != nil {
		w.add("coalesce(f.building_area_m2, 0) >= %s", *f.MinArea)
	}
	if f.MaxArea != nil {
		w.add("coalesce(f.building_area_m2, 0) <= %s", *f.MaxArea)
	}
	if f.MinConfidence != nil && skip != "confidence" {
		w.add("f.confidence >= %s", *f.MinConfidence)
	}
	if len(f.BBox) > 0 {
		if len(f.BBox) != 4 || f.BBox[0] > f.BBox[2] || f.BBox[1] > f.BBox[3] {
			return nil, fmt.Errorf("%w: bbox", ErrBadFilter)
		}
		w.add("f.point && ST_MakeEnvelope(%s, %s, %s, %s, 4326)", f.BBox[0], f.BBox[1], f.BBox[2], f.BBox[3])
	}
	if len(f.Polygon) > 0 {
		w.add("ST_Intersects(f.point, ST_SetSRID(ST_GeomFromGeoJSON(%s), 4326))", string(f.Polygon))
	}
	if c := f.Near; c != nil {
		w.add("ST_DWithin(f.point::geography, ST_SetSRID(ST_MakePoint(%s, %s), 4326)::geography, %s)", c.Lon, c.Lat, c.RadiusM)
	}
	if f.MaxMkadMin != nil {
		w.add("acc.minutes <= %s", *f.MaxMkadMin)
	}
	if b := f.NearRestrictions; b != nil {
		if *b {
			w.add(nearRestriction)
		} else {
			w.add("NOT " + nearRestriction)
		}
	}
	if b := f.HasPermit; b != nil {
		if *b {
			w.add(hasPermit)
		} else {
			w.add("NOT " + hasPermit)
		}
	}
	return w, nil
}

// fromClause — таблицы выборки: объект, тип, территории, время до МКАД для профиля списка.
const fromClause = `
	FROM infra.facility f
	JOIN ref.facility_type t ON t.code = f.type_code
	LEFT JOIN territory.admin_area o ON o.id = f.okrug_id
	LEFT JOIN territory.admin_area d ON d.id = f.district_id
	LEFT JOIN analytics.accessibility acc
	       ON acc.facility_id = f.id AND acc.profile_code = '` + AccessProfile + `' AND acc.target_kind = 'mkad'`
