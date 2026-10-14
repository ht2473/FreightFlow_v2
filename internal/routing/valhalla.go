package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client — клиент службы Valhalla.
type Client struct {
	base string
	http *http.Client
}

// NewClient создаёт клиент службы по адресу base (FF_VALHALLA_URL).
func NewClient(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}

// Point — точка: долгота и широта.
type Point struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

// Truck — параметры грузового профиля Valhalla: масса и нагрузка на ось в тоннах, габариты в
// метрах.
type Truck struct {
	Weight   float64 `json:"weight,omitempty"`
	AxleLoad float64 `json:"axle_load,omitempty"`
	Height   float64 `json:"height,omitempty"`
	Width    float64 `json:"width,omitempty"`
	Length   float64 `json:"length,omitempty"`
	Hazmat   bool    `json:"hazmat,omitempty"`
}

// Ring — многоугольник для исключения: кольцо координат [долгота, широта].
type Ring [][2]float64

// RouteRequest — запрос маршрута.
type RouteRequest struct {
	Locations []Point
	// Costing — truck для грузовика, auto — для сравнения «без ограничений».
	Costing string
	Truck   *Truck
	Exclude []Ring
	// Depart — время отправления (местное); пусто — без учёта времени.
	Depart     time.Time
	Alternates int
}

// Leg — участок маршрута между соседними точками.
type Leg struct {
	Shape []Point
	// Time — секунды, Length — километры.
	Time   float64
	Length float64
}

// Route — маршрут.
type Route struct {
	Legs   []Leg
	Time   float64
	Length float64
}

// Error — ошибка Valhalla: код (например, 442 — путь не найден) и текст.
type Error struct {
	Status int
	Code   int    `json:"error_code"`
	Text   string `json:"error"`
}

func (e *Error) Error() string { return fmt.Sprintf("valhalla %d: %s", e.Code, e.Text) }

// ErrUnavailable — служба маршрутизации не отвечает.
var ErrUnavailable = errors.New("valhalla: unavailable")

// ErrNoRoute — путь между точками не найден (код 442).
var ErrNoRoute = errors.New("valhalla: no route")

// Is сопоставляет ошибку «путь не найден» с ErrNoRoute.
func (e *Error) Is(target error) bool { return target == ErrNoRoute && e.Code == 442 }

type location struct {
	Lat              float64  `json:"lat"`
	Lon              float64  `json:"lon"`
	Radius           int      `json:"radius,omitempty"`
	Heading          *float64 `json:"heading,omitempty"`
	HeadingTolerance int      `json:"heading_tolerance,omitempty"`
}

// ExcludePoint — участок, исключаемый из графа: ближайший к точке с направлением движения
// Heading (градусы от севера по часовой стрелке). Направление отличает мост от дороги под ним и
// одну проезжую часть от встречной.
type ExcludePoint struct {
	Point
	Heading float64
}

// excludeHeadingTolerance — допуск направления исключаемого участка, градусы.
const excludeHeadingTolerance = 30

func excludeLocations(ps []ExcludePoint) []location {
	if len(ps) == 0 {
		return nil
	}
	out := make([]location, len(ps))
	for i, p := range ps {
		h := p.Heading
		out[i] = location{Lat: p.Lat, Lon: p.Lon, Heading: &h, HeadingTolerance: excludeHeadingTolerance}
	}
	return out
}

// locations — точки запроса; radius — в каком радиусе (м) искать участки для привязки, кроме
// ближайшего: точка объекта (центр контура) бывает на внутреннем проезде, недоступном извне.
func locations(ps []Point, radius int) []location {
	out := make([]location, len(ps))
	for i, p := range ps {
		out[i] = location{Lat: p.Lat, Lon: p.Lon, Radius: radius}
	}
	return out
}

func costingOptions(costing string, t *Truck) map[string]any {
	if t == nil {
		return nil
	}
	return map[string]any{costing: t}
}

func excludePolygons(rings []Ring) [][][2]float64 {
	if len(rings) == 0 {
		return nil
	}
	out := make([][][2]float64, len(rings))
	for i, r := range rings {
		out[i] = r
	}
	return out
}

// Route строит маршрут; при Alternates > 0 — ещё и альтернативные.
func (c *Client) Route(ctx context.Context, req RouteRequest) ([]Route, error) {
	body := map[string]any{
		"locations":        locations(req.Locations, 0),
		"costing":          req.Costing,
		"costing_options":  costingOptions(req.Costing, req.Truck),
		"exclude_polygons": excludePolygons(req.Exclude),
		"directions_type":  "none",
		"alternates":       req.Alternates,
		"units":            "kilometers",
		"shape_format":     "polyline6",
	}
	if !req.Depart.IsZero() {
		body["date_time"] = map[string]any{"type": 1, "value": req.Depart.Format("2006-01-02T15:04")}
	}
	var resp struct {
		Trip       trip `json:"trip"`
		Alternates []struct {
			Trip trip `json:"trip"`
		} `json:"alternates"`
	}
	if err := c.post(ctx, "/route", body, &resp); err != nil {
		return nil, err
	}
	out := []Route{resp.Trip.route()}
	for _, a := range resp.Alternates {
		out = append(out, a.Trip.route())
	}
	return out, nil
}

type trip struct {
	Legs []struct {
		Shape   string  `json:"shape"`
		Summary summary `json:"summary"`
	} `json:"legs"`
	Summary summary `json:"summary"`
}

type summary struct {
	Time   float64 `json:"time"`
	Length float64 `json:"length"`
}

func (t trip) route() Route {
	r := Route{Time: t.Summary.Time, Length: t.Summary.Length}
	for _, l := range t.Legs {
		r.Legs = append(r.Legs, Leg{Shape: DecodePolyline6(l.Shape), Time: l.Summary.Time, Length: l.Summary.Length})
	}
	return r
}

// IsochroneRequest — запрос изохрон: время в минутах для каждого контура.
type IsochroneRequest struct {
	Origin  Point
	Costing string
	Truck   *Truck
	Exclude []Ring
	Minutes []int
	Depart  time.Time
}

// Isochrone возвращает изохроны в GeoJSON (FeatureCollection многоугольников).
func (c *Client) Isochrone(ctx context.Context, req IsochroneRequest) (json.RawMessage, error) {
	contours := make([]map[string]int, len(req.Minutes))
	for i, m := range req.Minutes {
		contours[i] = map[string]int{"time": m}
	}
	body := map[string]any{
		"locations":        locations([]Point{req.Origin}, 0),
		"costing":          req.Costing,
		"costing_options":  costingOptions(req.Costing, req.Truck),
		"exclude_polygons": excludePolygons(req.Exclude),
		"contours":         contours,
		"polygons":         true,
		"denoise":          0.3,
		"generalize":       50,
	}
	if !req.Depart.IsZero() {
		body["date_time"] = map[string]any{"type": 1, "value": req.Depart.Format("2006-01-02T15:04")}
	}
	var raw json.RawMessage
	err := c.post(ctx, "/isochrone", body, &raw)
	return raw, err
}

// MatrixRequest — запрос матрицы времени и расстояния.
type MatrixRequest struct {
	Sources, Targets []Point
	Costing          string
	Truck            *Truck
	Exclude          []Ring
	// SourceRadius — радиус привязки источников, м (0 — ближайший участок).
	SourceRadius int
	// ExcludeLocations — закрытые участки (сценарий выпадения элемента сети).
	ExcludeLocations []ExcludePoint
}

// Cell — время (секунды) и расстояние (километры) между источником и целью; Reachable = false,
// если путь не найден.
type Cell struct {
	Time, Length float64
	Reachable    bool
}

// Matrix возвращает матрицу «источники × цели».
func (c *Client) Matrix(ctx context.Context, req MatrixRequest) ([][]Cell, error) {
	body := map[string]any{
		"sources":           locations(req.Sources, req.SourceRadius),
		"targets":           locations(req.Targets, 0),
		"costing":           req.Costing,
		"costing_options":   costingOptions(req.Costing, req.Truck),
		"exclude_polygons":  excludePolygons(req.Exclude),
		"exclude_locations": excludeLocations(req.ExcludeLocations),
		"units":             "kilometers",
	}
	var resp struct {
		Rows [][]struct {
			Time     *float64 `json:"time"`
			Distance *float64 `json:"distance"`
		} `json:"sources_to_targets"`
	}
	if err := c.post(ctx, "/sources_to_targets", body, &resp); err != nil {
		return nil, err
	}
	out := make([][]Cell, len(resp.Rows))
	for i, row := range resp.Rows {
		out[i] = make([]Cell, len(row))
		for j, cell := range row {
			if cell.Time != nil && cell.Distance != nil {
				out[i][j] = Cell{Time: *cell.Time, Length: *cell.Distance, Reachable: true}
			}
		}
	}
	return out, nil
}

// Status — версия службы и время сборки графа.
type Status struct {
	Version  string `json:"version"`
	Tileset  int64  `json:"tileset_last_modified"`
	Services []any  `json:"available_actions"`
}

// Status проверяет готовность службы.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	err := c.do(ctx, http.MethodGet, "/status", nil, &st)
	return st, err
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(dropNil(body))
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, b, out)
}

// dropNil убирает пустые параметры запроса: Valhalla отклоняет null на месте объекта.
func dropNil(body any) any {
	m, ok := body.(map[string]any)
	if !ok {
		return body
	}
	out := map[string]any{}
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case map[string]any:
			if x != nil {
				out[k] = x
			}
		case [][][2]float64:
			if x != nil {
				out[k] = x
			}
		case []location:
			if x != nil {
				out[k] = x
			}
		default:
			out[k] = v
		}
	}
	return out
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("valhalla %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		e := &Error{Status: resp.StatusCode}
		if json.Unmarshal(data, e) != nil || e.Text == "" {
			e.Text = strings.TrimSpace(string(data))
		}
		return e
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("valhalla %s: decode: %w", path, err)
	}
	return nil
}

// DecodePolyline6 раскодирует линию в формате polyline с точностью 6 знаков (формат Valhalla).
func DecodePolyline6(s string) []Point {
	var out []Point
	var lat, lon int64
	for i := 0; i < len(s); {
		var dlat, dlon int64
		var ok bool
		if dlat, i, ok = decodeValue(s, i); !ok {
			break
		}
		if dlon, i, ok = decodeValue(s, i); !ok {
			break
		}
		lat += dlat
		lon += dlon
		out = append(out, Point{Lat: float64(lat) / 1e6, Lon: float64(lon) / 1e6})
	}
	return out
}

func decodeValue(s string, i int) (int64, int, bool) {
	var result int64
	var shift uint
	for i < len(s) {
		b := int64(s[i]) - 63
		i++
		result |= (b & 0x1f) << shift
		shift += 5
		if b < 0x20 {
			if result&1 != 0 {
				return ^(result >> 1), i, true
			}
			return result >> 1, i, true
		}
	}
	return 0, i, false
}
