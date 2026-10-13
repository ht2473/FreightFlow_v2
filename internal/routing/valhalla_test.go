package routing

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecodePolyline6(t *testing.T) {
	t.Parallel()
	// Пример из описания формата polyline (точность 5); с точностью 6 те же числа в 10 раз меньше.
	got := DecodePolyline6("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	want := []Point{{Lat: 3.85, Lon: -12.02}, {Lat: 4.07, Lon: -12.095}, {Lat: 4.3252, Lon: -12.6453}}
	if len(got) != len(want) {
		t.Fatalf("points: %v", got)
	}
	for i := range want {
		if math.Abs(got[i].Lat-want[i].Lat) > 1e-9 || math.Abs(got[i].Lon-want[i].Lon) > 1e-9 {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}
	if p := DecodePolyline6("_p~i"); len(p) != 0 {
		t.Errorf("broken line decoded: %v", p)
	}
}

func TestRouteRequestAndResponse(t *testing.T) {
	t.Parallel()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = io.WriteString(w, `{"trip":{"legs":[{"shape":"_p~iF~ps|U_ulLnnqC","summary":{"time":600,"length":7.5}}],
			"summary":{"time":600,"length":7.5}},"alternates":[{"trip":{"legs":[],"summary":{"time":700,"length":8}}}]}`)
	}))
	defer srv.Close()

	routes, err := NewClient(srv.URL+"/").Route(t.Context(), RouteRequest{
		Locations: []Point{{Lon: 37.6, Lat: 55.7}, {Lon: 37.7, Lat: 55.8}},
		Costing:   "truck", Truck: &Truck{Weight: 20, Height: 4},
		Exclude:    []Ring{{{37.5, 55.7}, {37.7, 55.7}, {37.7, 55.8}, {37.5, 55.7}}},
		Alternates: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Time != 600 || len(routes[0].Legs[0].Shape) != 2 || routes[1].Length != 8 {
		t.Fatalf("routes: %+v", routes)
	}
	opts, _ := body["costing_options"].(map[string]any)
	truck, _ := opts["truck"].(map[string]any)
	if body["costing"] != "truck" || truck["weight"] != 20.0 || truck["height"] != 4.0 || body["exclude_polygons"] == nil {
		t.Errorf("request: %v", body)
	}
	if _, ok := truck["hazmat"]; ok {
		t.Error("empty option sent")
	}
}

func TestErrorsAndEmptyOptions(t *testing.T) {
	t.Parallel()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error_code":442,"error":"No path could be found for input","status_code":400}`)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL).Route(t.Context(), RouteRequest{Locations: []Point{{}, {}}, Costing: "auto"})
	var ve *Error
	if !errors.Is(err, ErrNoRoute) || !errors.As(err, &ve) || ve.Status != http.StatusBadRequest {
		t.Fatalf("err = %v", err)
	}
	// Без профиля и исключений эти поля не передаются (null Valhalla не принимает).
	for _, k := range []string{"costing_options", "exclude_polygons", "date_time"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s sent: %v", k, body[k])
		}
	}
}

func TestMatrixUnreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"sources_to_targets":[[{"time":120,"distance":1.5},{"time":null,"distance":null}]]}`)
	}))
	defer srv.Close()
	m, err := NewClient(srv.URL).Matrix(t.Context(), MatrixRequest{
		Sources: []Point{{}}, Targets: []Point{{}, {}}, Costing: "truck",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !m[0][0].Reachable || m[0][0].Time != 120 || m[0][1].Reachable {
		t.Fatalf("matrix: %+v", m)
	}
}
