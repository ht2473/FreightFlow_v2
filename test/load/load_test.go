//go:build load

// Package load — нагрузочные сценарии по бюджетам 03-architecture §24: API, тайлы, поиск,
// маршруты. Идут против запущенного сервера (`task serve`) от имени пользователя по сеансу,
// созданному прямо в хранилище сеансов: интерфейс ходит так же, без лимита токенов API.
//
//	task load                       — все сценарии
//	task load -- -run TestLoad/tiles — один сценарий
package load

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vegeta "github.com/tsenart/vegeta/v12/lib"

	"freightflow/internal/platform/db"
	"freightflow/internal/platform/session"
)

type scenario struct {
	name    string
	rate    int // запросов в секунду
	dur     time.Duration
	budget  time.Duration // предел p95
	targets func(t *testing.T, c *client) []vegeta.Target
	// warm — сначала прогнать те же запросы без замера (тайлы из кеша).
	warm bool
}

type client struct {
	base   string
	header http.Header
}

func (c *client) get(t *testing.T, path string, out any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header = c.header.Clone()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d", path, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func (c *client) target(method, path string, body any) vegeta.Target {
	tg := vegeta.Target{Method: method, URL: c.base + path, Header: c.header.Clone()}
	if body != nil {
		tg.Body, _ = json.Marshal(body)
		tg.Header.Set("Content-Type", "application/json")
	}
	return tg
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// login создаёт сеанс пользователя в хранилище сеансов сервера и возвращает его cookie.
func login(t *testing.T, email string) string {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, os.Getenv("FF_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM core.app_user WHERE email = lower($1)`, email).Scan(&id); err != nil {
		t.Fatalf("пользователь %s: %v", email, err)
	}
	m := session.New(pool, false)
	sctx, err := m.Load(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Login(sctx, id); err != nil {
		t.Fatal(err)
	}
	token, _, err := m.Commit(sctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Store.Delete(token) })
	return session.CookieName + "=" + token
}

var truck = map[string]any{"grossT": 20, "heightM": 4, "widthM": 2.55, "lengthM": 12, "axleLoadT": 10}

// Точки Москвы для маршрутов и изохрон: промзоны и логистические центры внутри МКАД и у неё.
var points = [][2]float64{
	{37.7404, 55.6544},
	{37.5840, 55.8550},
	{37.4120, 55.7480},
	{37.7980, 55.8130},
	{37.6170, 55.6050},
	{37.5310, 55.6800},
	{37.8420, 55.7410},
	{37.6590, 55.7950},
}

func scenarios() []scenario {
	return []scenario{
		{name: "api", rate: 30, dur: 20 * time.Second, budget: 200 * time.Millisecond, targets: apiTargets},
		{name: "tiles", rate: 50, dur: 20 * time.Second, budget: 300 * time.Millisecond, targets: tileTargets},
		{name: "tiles-cached", rate: 100, dur: 10 * time.Second, budget: 80 * time.Millisecond, targets: tileTargets, warm: true},
		{name: "search", rate: 20, dur: 15 * time.Second, budget: 300 * time.Millisecond, targets: searchTargets},
		{name: "suggest", rate: 30, dur: 15 * time.Second, budget: 50 * time.Millisecond, targets: suggestTargets},
		// Маршруты на пользователя ограничены (60 подряд, затем один в секунду) — нагрузка в пределе.
		{name: "route", rate: 1, dur: 30 * time.Second, budget: time.Second, targets: routeTargets},
		{name: "isochrone", rate: 1, dur: 20 * time.Second, budget: 2 * time.Second, targets: isoTargets},
	}
}

func apiTargets(t *testing.T, c *client) []vegeta.Target {
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	c.get(t, "/api/v1/facilities?limit=20&sort=-area", &page)
	out := []vegeta.Target{
		c.target(http.MethodGet, "/api/v1/facilities?limit=50", nil),
		c.target(http.MethodGet, "/api/v1/facilities?limit=50&type=warehouse&sort=-area", nil),
		c.target(http.MethodGet, "/api/v1/facilities/facets", nil),
		c.target(http.MethodGet, "/api/v1/territories?kind=district", nil),
		c.target(http.MethodGet, "/api/v1/metrics/territory.facilities?level=district&limit=20", nil),
		c.target(http.MethodGet, "/api/v1/metrics/entity?level=city&entity=moscow", nil),
		c.target(http.MethodGet, "/api/v1/alerts?limit=50", nil),
		c.target(http.MethodGet, "/api/v1/data/updates?limit=8", nil),
	}
	for _, it := range page.Items {
		out = append(out, c.target(http.MethodGet, "/api/v1/facilities/"+it.ID, nil))
	}
	return out
}

func tileTargets(t *testing.T, c *client) []vegeta.Target {
	var cat struct {
		Layers []struct {
			Code    string `json:"code"`
			Tiles   string `json:"tiles"`
			MinZoom int    `json:"minZoom"`
		} `json:"layers"`
	}
	c.get(t, "/api/v1/layers", &cat)
	want := map[string]bool{"facilities": true, "facility_areas": true, "road_works": true, "restrictions": true, "rings": true}
	rnd := rand.New(rand.NewPCG(1, 2))
	var out []vegeta.Target
	for _, l := range cat.Layers {
		if !want[l.Code] {
			continue
		}
		// Центр Москвы на масштабах 12–14: тайлы, которых нет в прогреве мелких масштабов.
		for range 40 {
			z := 12 + rnd.IntN(3)
			lon := 37.45 + rnd.Float64()*0.35
			lat := 55.65 + rnd.Float64()*0.2
			if z < l.MinZoom {
				z = l.MinZoom
			}
			x, y := tileXY(lon, lat, z)
			url := strings.NewReplacer("{z}", fmt.Sprint(z), "{x}", fmt.Sprint(x), "{y}", fmt.Sprint(y)).Replace(l.Tiles)
			out = append(out, c.target(http.MethodGet, url, nil))
		}
	}
	return out
}

func searchTargets(_ *testing.T, c *client) []vegeta.Target {
	qs := []string{
		"склад", "склады ЮВАО больше 10000 м2", "Варшавское шоссе", "логистический центр у МКАД",
		"Подольск", "район Марьино", "холодильный склад", "ограничение массы", "ДТП", "терминал",
	}
	out := make([]vegeta.Target, 0, len(qs))
	for _, q := range qs {
		out = append(out, c.target(http.MethodGet, "/api/v1/search?q="+url.QueryEscape(q), nil))
	}
	return out
}

func suggestTargets(_ *testing.T, c *client) []vegeta.Target {
	qs := []string{"скл", "лог", "варш", "марь", "холод", "терм", "мкад", "проф", "ЮВА", "под"}
	out := make([]vegeta.Target, 0, len(qs))
	for _, q := range qs {
		out = append(out, c.target(http.MethodGet, "/api/v1/search/suggest?limit=8&q="+url.QueryEscape(q), nil))
	}
	return out
}

func routeTargets(_ *testing.T, c *client) []vegeta.Target {
	var out []vegeta.Target
	for i := range points {
		a, b := points[i], points[(i+3)%len(points)]
		out = append(out, c.target(http.MethodPost, "/api/v1/routing/route", map[string]any{
			"points":  []map[string]float64{{"lon": a[0], "lat": a[1]}, {"lon": b[0], "lat": b[1]}},
			"vehicle": truck,
		}))
	}
	return out
}

func isoTargets(_ *testing.T, c *client) []vegeta.Target {
	var out []vegeta.Target
	for _, p := range points {
		out = append(out, c.target(http.MethodPost, "/api/v1/routing/isochrone", map[string]any{
			"origin": map[string]float64{"lon": p[0], "lat": p[1]}, "vehicle": truck, "minutes": []int{15, 30},
		}))
	}
	return out
}

func TestLoad(t *testing.T) {
	email := env("FF_LOAD_EMAIL", os.Getenv("FF_E2E_EMAIL"))
	if email == "" || os.Getenv("FF_DATABASE_URL") == "" {
		t.Skip("нужны FF_DATABASE_URL и FF_LOAD_EMAIL (или FF_E2E_EMAIL)")
	}
	c := &client{base: env("FF_LOAD_BASE_URL", "http://127.0.0.1:8080"), header: http.Header{}}
	c.header.Set("Cookie", login(t, email))
	c.header.Set("Accept-Encoding", "gzip")

	t.Logf("%-13s %6s %5s %8s %8s %8s %8s %7s", "сценарий", "запр.", "rps", "p50", "p95", "p99", "бюджет", "успех")
	for _, sc := range scenarios() {
		t.Run(sc.name, func(t *testing.T) {
			targets := sc.targets(t, c)
			if sc.warm {
				attack(targets, len(targets), time.Second)
			}
			m := attack(targets, sc.rate, sc.dur)
			t.Logf("%-13s %6d %5d %8s %8s %8s %8s %6.1f%%", sc.name, m.Requests, sc.rate,
				m.Latencies.P50.Round(time.Millisecond), m.Latencies.P95.Round(time.Millisecond),
				m.Latencies.P99.Round(time.Millisecond), sc.budget, m.Success*100)
			if m.Success < 0.99 {
				t.Errorf("успешных ответов %.1f%%, коды %v, ошибки %v", m.Success*100, m.StatusCodes, m.Errors)
			}
			if m.Latencies.P95 > sc.budget {
				t.Errorf("p95 %s больше бюджета %s", m.Latencies.P95.Round(time.Millisecond), sc.budget)
			}
		})
	}
}

func attack(targets []vegeta.Target, rate int, dur time.Duration) *vegeta.Metrics {
	atk := vegeta.NewAttacker(vegeta.Timeout(30*time.Second), vegeta.KeepAlive(true))
	var m vegeta.Metrics
	for res := range atk.Attack(vegeta.NewStaticTargeter(targets...), vegeta.Rate{Freq: rate, Per: time.Second}, dur, "load") {
		m.Add(res)
	}
	m.Close()
	return &m
}

// tileXY — номер тайла, содержащего точку, на масштабе z (веб-Меркатор).
func tileXY(lon, lat float64, z int) (int, int) {
	n := math.Exp2(float64(z))
	r := lat * math.Pi / 180
	x := (lon + 180) / 360 * n
	y := (1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n
	return int(x), int(y)
}
