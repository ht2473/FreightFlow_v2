package routing

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
)

// Контрактные тесты клиента: ответы записаны со службы Valhalla 3.9.1 на графе Москвы
// (testdata/valhalla, точки в Нижегородском районе); сервер отдаёт записанный ответ и сохраняет
// запрос клиента.

// recorded — сервер с записанными ответами: путь запроса → файл (код ответа — из поля
// status_code ошибки).
func recorded(t *testing.T, files map[string]string) (*Client, func(path string) map[string]any) {
	t.Helper()
	var mu sync.Mutex
	requests := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests[r.URL.Path] = body
		mu.Unlock()
		data, err := os.ReadFile("testdata/valhalla/" + name + ".json")
		if err != nil {
			t.Error(err)
			return
		}
		var e struct {
			Status int `json:"status_code"`
		}
		if json.Unmarshal(data, &e) == nil && e.Status != 0 {
			w.WriteHeader(e.Status)
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL), func(path string) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return requests[path]
	}
}

var (
	contractTruck = &Truck{Weight: 20, AxleLoad: 10, Height: 4, Width: 2.55, Length: 10}
	pointA        = Point{Lon: 37.7310, Lat: 55.7290}
	pointB        = Point{Lon: 37.7560, Lat: 55.7405}
	pointC        = Point{Lon: 37.7480, Lat: 55.7180}
)

func near(p, q Point, m float64) bool {
	return geo.Distance(orb.Point{p.Lon, p.Lat}, orb.Point{q.Lon, q.Lat}) <= m
}

func TestContractStatusAndRoute(t *testing.T) {
	t.Parallel()
	c, request := recorded(t, map[string]string{"/status": "status", "/route": "route"})
	st, err := c.Status(t.Context())
	if err != nil || st.Version != "3.9.1" || st.Tileset == 0 {
		t.Fatalf("status %+v, %v", st, err)
	}
	routes, err := c.Route(t.Context(), RouteRequest{
		Locations: []Point{pointA, pointB}, Costing: "truck", Truck: contractTruck, Alternates: 1,
	})
	if err != nil || len(routes) == 0 {
		t.Fatalf("routes %d, %v", len(routes), err)
	}
	r := routes[0]
	shape := r.Legs[0].Shape
	if math.Abs(r.Time-445.5) > 1 || math.Abs(r.Length-6.054) > 0.001 || len(shape) < 20 {
		t.Fatalf("route: %.1f s, %.3f km, %d points", r.Time, r.Length, len(shape))
	}
	// Линия раскодирована с точностью 6 знаков: начало и конец — точки привязки у точек запроса
	// (B привязана к дороге в 85 м).
	if !near(shape[0], pointA, 30) || !near(shape[len(shape)-1], pointB, 120) {
		t.Errorf("shape ends %v … %v", shape[0], shape[len(shape)-1])
	}
	body := request("/route")
	if body["costing"] != "truck" || body["shape_format"] != "polyline6" || body["costing_options"] == nil {
		t.Errorf("route request %v", body)
	}
}

func TestContractIsochroneAndMatrix(t *testing.T) {
	t.Parallel()
	c, request := recorded(t, map[string]string{"/isochrone": "isochrone", "/sources_to_targets": "matrix"})
	raw, err := c.Isochrone(t.Context(), IsochroneRequest{Origin: pointA, Costing: "truck", Truck: contractTruck, Minutes: []int{5, 10}})
	if err != nil {
		t.Fatal(err)
	}
	var fc struct {
		Features []struct {
			Properties struct {
				Contour float64 `json:"contour"`
			} `json:"properties"`
			Geometry struct {
				Type string `json:"type"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil || len(fc.Features) != 2 {
		t.Fatalf("isochrone: %d features, %v", len(fc.Features), err)
	}
	var contours []float64
	for _, f := range fc.Features {
		contours = append(contours, f.Properties.Contour)
		if f.Geometry.Type != "Polygon" && f.Geometry.Type != "MultiPolygon" {
			t.Errorf("contour geometry %s", f.Geometry.Type)
		}
	}
	slices.Sort(contours)
	if !slices.Equal(contours, []float64{5, 10}) {
		t.Errorf("contours %v", contours)
	}

	m, err := c.Matrix(t.Context(), MatrixRequest{
		Sources: []Point{pointA, pointC}, Targets: []Point{pointB, pointC}, Costing: "truck", Truck: contractTruck,
	})
	if err != nil || len(m) != 2 || len(m[0]) != 2 {
		t.Fatalf("matrix %v, %v", m, err)
	}
	if !m[0][0].Reachable || m[0][0].Time != 445 || m[0][0].Length != 6.054 || !m[1][1].Reachable || m[1][1].Time != 0 {
		t.Errorf("cells %+v", m)
	}
	if body := request("/sources_to_targets"); len(body["sources"].([]any)) != 2 || body["exclude_locations"] != nil {
		t.Errorf("matrix request %v", body)
	}
}

func TestContractLocateAndTrace(t *testing.T) {
	t.Parallel()
	c, request := recorded(t, map[string]string{"/locate": "locate", "/trace_attributes": "trace_attributes"})
	snaps, err := c.Locate(t.Context(), []Point{pointA}, contractTruck)
	if err != nil || len(snaps) != 1 || snaps[0] == nil {
		t.Fatalf("locate %v, %v", snaps, err)
	}
	if s := snaps[0]; s.WayID != 49507818 || s.Distance != 10.5 || !near(s.Point, pointA, 15) {
		t.Errorf("snap %+v", s)
	}

	edges, err := c.TraceAttributes(t.Context(), []Point{pointA, pointB}, "truck", contractTruck)
	if err != nil || len(edges) < 10 {
		t.Fatalf("trace: %d edges, %v", len(edges), err)
	}
	var length float64
	named := false
	for i, e := range edges {
		length += e.Length
		named = named || slices.Contains(e.Names, "Газгольдерная улица")
		if e.WayID == 0 || e.EndShape < e.BeginShape || i > 0 && e.BeginShape != edges[i-1].EndShape {
			t.Fatalf("edge %d: %+v", i, e)
		}
	}
	// Участки покрывают весь путь маршрута (6,054 км).
	if math.Abs(length-6.054) > 0.06 || !named {
		t.Errorf("trace length %.3f km, named %v", length, named)
	}
	if body := request("/trace_attributes"); body["shape_match"] != "map_snap" || len(body["shape"].([]any)) != 2 {
		t.Errorf("trace request %v", body)
	}
}

func TestContractErrors(t *testing.T) {
	t.Parallel()
	c, _ := recorded(t, map[string]string{"/route": "error_location", "/sources_to_targets": "error_distance"})
	_, err := c.Route(t.Context(), RouteRequest{Locations: []Point{{Lon: 37.6, Lat: 61}, pointB}, Costing: "truck"})
	var ve *Error
	if !errors.Is(err, ErrNoLocation) || !errors.As(err, &ve) || ve.Status != http.StatusBadRequest {
		t.Errorf("no location: %v", err)
	}
	_, err = c.Matrix(t.Context(), MatrixRequest{Sources: []Point{pointA, {Lon: 37.6, Lat: 61}}, Targets: []Point{pointB}, Costing: "truck"})
	if !errors.Is(err, ErrLimit) || errors.Is(err, ErrNoRoute) {
		t.Errorf("limit: %v", err)
	}
}
