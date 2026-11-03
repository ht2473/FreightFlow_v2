package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"freightflow/internal/identity"
)

// runDemo наполняет учётную запись демонстрационным набором: дашборд, сохранённые виды,
// правила алертов, сценарии, оценка локации, отчёты и расписание. Всё создаётся через API
// запущенного сервера от имени пользователя по временному токену, который в конце отзывается;
// значения считаются системой по загруженным данным. Повторный запуск добавляет только то,
// чего ещё нет (по названию).
func runDemo(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "адрес электронной почты пользователя")
	base := fs.String("api", "http://127.0.0.1:8080/api/v1", "адрес API запущенного сервера")
	if err := fs.Parse(args); err != nil || *email == "" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow demo -email ... [-api http://127.0.0.1:8080/api/v1]")
		return ErrUsage
	}
	pool, err := openDatabase(ctx, env)
	if err != nil {
		return err
	}
	defer pool.Close()
	ids, err := newIdentity(ctx, env, pool)
	if err != nil {
		return err
	}
	users, err := ids.ListUsers(ctx)
	if err != nil {
		return err
	}
	target := strings.ToLower(strings.TrimSpace(*email))
	var p identity.Principal
	for _, u := range users {
		if u.Email == target {
			if p, err = ids.Principal(ctx, u.ID); err != nil {
				return err
			}
		}
	}
	if p.UserID == [16]byte{} {
		return fmt.Errorf("user %s not found", target)
	}
	meta := identity.RequestMeta{UserAgent: "freightflow demo"}
	expires := time.Now().Add(time.Hour)
	plain, tok, err := ids.CreateAPIToken(ctx, p, "Демонстрационный набор", p.Permissions, &expires, meta)
	if err != nil {
		return err
	}
	defer func() {
		if err := ids.RevokeAPIToken(context.WithoutCancel(ctx), p, tok.ID, meta); err != nil {
			fmt.Fprintf(env.Stderr, "не удалось отозвать временный токен %s: %v\n", tok.ID, err)
		}
	}()
	d := &demo{base: strings.TrimRight(*base, "/"), token: plain, out: env.Stdout, http: &http.Client{Timeout: 2 * time.Minute}}
	return d.run(ctx)
}

type demo struct {
	base, token string
	out         io.Writer
	http        *http.Client
}

type obj = map[string]any

func (d *demo) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (d *demo) logf(format string, a ...any) {
	fmt.Fprintf(d.out, format+"\n", a...)
}

// named — названия уже созданных записей: повторный запуск их не дублирует.
func named(items []obj) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		name, _ := it["name"].(string)
		if name == "" {
			name, _ = it["title"].(string)
		}
		id, _ := it["id"].(string)
		out[name] = id
	}
	return out
}

func (d *demo) run(ctx context.Context) error {
	steps := []struct {
		title string
		fn    func(context.Context) error
	}{
		{"Дашборд", d.dashboard},
		{"Виды карты и реестра", d.views},
		{"Правила алертов", d.rules},
		{"Сценарии", d.scenarios},
		{"Оценка локации", d.site},
		{"Отчёты и расписания", d.reports},
	}
	var errs []error
	for _, st := range steps {
		d.logf("== %s", st.title)
		if err := st.fn(ctx); err != nil {
			d.logf("   ошибка: %v", err)
			errs = append(errs, fmt.Errorf("%s: %w", st.title, err))
		}
	}
	return errors.Join(errs...)
}

// dashboard — набор по умолчанию и рейтинги двух сводных индексов.
func (d *demo) dashboard(ctx context.Context) error {
	if err := d.call(ctx, http.MethodDelete, "/me/dashboard", nil, nil); err != nil {
		return err
	}
	var dash struct {
		Widgets []obj `json:"widgets"`
	}
	if err := d.call(ctx, http.MethodGet, "/me/dashboard", nil, &dash); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, w := range dash.Widgets {
		if ix, ok := w["index"].(string); ok {
			have[ix] = true
		}
	}
	for _, ix := range []string{"index.accessibility", "index.restrictions"} {
		if !have[ix] {
			dash.Widgets = append(dash.Widgets, obj{"kind": "index", "index": ix, "level": "district"})
		}
	}
	if err := d.call(ctx, http.MethodPut, "/me/dashboard", obj{"widgets": dash.Widgets}, nil); err != nil {
		return err
	}
	d.logf("   виджетов: %d", len(dash.Widgets))
	return nil
}

func (d *demo) views(ctx context.Context) error {
	views := []obj{
		{"page": "map", "name": "Объекты и дорожные работы", "params": obj{
			"c": "37.62000,55.75000", "z": 10, "l": "facilities,facility_areas,rings,road_works",
		}},
		{"page": "map", "name": "Ограничения для грузовиков в центре", "params": obj{
			"c": "37.62000,55.75500", "z": 12.5, "l": "facilities,rings,restrictions,cargo_frame",
		}},
		{"page": "map", "name": "Критичные мосты и устойчивость сети", "params": obj{
			"c": "37.62000,55.74000", "z": 10.5, "l": "rings,critical_elements,resilience",
		}},
		{"page": "map", "name": "Складские площади по ячейкам H3", "params": obj{
			"c": "37.62000,55.73000", "z": 10, "l": "rings,h3_cells,industrial_zones",
		}},
		{"page": "facilities", "name": "Крупные логистические центры", "params": obj{
			"type": "logistics_center,terminal,sorting_center", "amin": 20000, "sort": "-area",
		}},
		{"page": "facilities", "name": "Склады до 15 минут от МКАД", "params": obj{
			"type": "warehouse,cold_storage", "mkad": 15, "conf": "high",
		}},
		{"page": "facilities", "name": "Объекты с ограничениями подъезда", "params": obj{
			"restr": "yes", "sort": "-area",
		}},
	}
	for _, page := range []string{"map", "facilities"} {
		var have []obj
		if err := d.call(ctx, http.MethodGet, "/views?page="+page, nil, &have); err != nil {
			return err
		}
		names := named(have)
		for _, v := range views {
			if v["page"] != page {
				continue
			}
			if _, ok := names[v["name"].(string)]; ok {
				continue
			}
			if err := d.call(ctx, http.MethodPost, "/views", v, nil); err != nil {
				return err
			}
			d.logf("   вид «%s»", v["name"])
		}
	}
	return nil
}

// topFacilities — крупнейшие по площади логистические центры реестра.
func (d *demo) topFacilities(ctx context.Context, n int) ([]string, error) {
	var page struct {
		Items []obj `json:"items"`
	}
	q := url.Values{"type": {"logistics_center"}, "sort": {"-area"}, "limit": {fmt.Sprint(n)}}
	if err := d.call(ctx, http.MethodGet, "/facilities?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		if id, ok := it["id"].(string); ok {
			out = append(out, id)
		}
	}
	return out, nil
}

func (d *demo) rules(ctx context.Context) error {
	top, err := d.topFacilities(ctx, 8)
	if err != nil {
		return err
	}
	// Варшавское шоссе от ТТК до МКАД — коридор для правила по работам.
	varshavka := obj{"type": "LineString", "coordinates": [][]float64{
		{37.6195, 55.7065}, {37.6230, 55.6870}, {37.6250, 55.6600}, {37.6175, 55.6330}, {37.6060, 55.6000}, {37.5960, 55.5760},
	}}
	// Область в пределах Третьего транспортного кольца (контур по основным развязкам).
	ttk := obj{"type": "Polygon", "coordinates": [][][]float64{{
		{37.6170, 55.7920},
		{37.6680, 55.7810},
		{37.6870, 55.7600},
		{37.6800, 55.7320},
		{37.6400, 55.7080},
		{37.5870, 55.7130},
		{37.5560, 55.7440},
		{37.5650, 55.7760},
		{37.6170, 55.7920},
	}}}
	rules := []obj{
		{
			"name": "Перекрытия в пределах ТТК", "kind": "roadworks", "scope": obj{"type": "area", "polygon": ttk},
			"severity": "critical",
			"params":   obj{"closureOnly": true, "changes": []string{"appeared", "started"}}, "channels": []string{"in_app"},
		},
		{
			"name": "Работы у крупнейших логистических центров", "kind": "roadworks",
			"scope": obj{"type": "facilities", "facilityIds": top, "radiusM": 1000}, "severity": "warning",
			"params": obj{"changes": []string{"appeared", "changed", "started"}}, "channels": []string{"in_app", "email"},
		},
		{
			"name": "Варшавское шоссе: работы на коридоре", "kind": "roadworks",
			"scope": obj{"type": "corridor", "line": varshavka, "bufferM": 300}, "severity": "warning",
			"params": obj{"changes": []string{"appeared", "started"}}, "channels": []string{"in_app"},
		},
		{
			"name": "Доступность крупнейших центров до МКАД", "kind": "accessibility",
			"scope": obj{"type": "facilities", "facilityIds": top, "radiusM": 500}, "severity": "critical",
			"params": obj{"profile": "truck_20", "target": "mkad", "thresholdMin": 5}, "channels": []string{"in_app"},
		},
		{
			"name": "Изменения ограничений для грузовиков в пределах ТТК", "kind": "restrictions",
			"scope":    obj{"type": "area", "polygon": ttk},
			"severity": "info", "params": obj{}, "channels": []string{"in_app"},
		},
		{
			"name": "Новые объекты в реестре", "kind": "facilities", "scope": obj{"type": "city"},
			"severity": "info", "params": obj{}, "channels": []string{"in_app"},
		},
		{
			"name": "Сбои и устаревание источников", "kind": "sources", "scope": obj{"type": "none"},
			"severity": "warning", "params": obj{"staleFactor": 2}, "channels": []string{"in_app"},
		},
	}
	var have []obj
	if err := d.call(ctx, http.MethodGet, "/alert-rules", nil, &have); err != nil {
		return err
	}
	names := named(have)
	for _, r := range rules {
		id, ok := names[r["name"].(string)]
		if !ok {
			var created obj
			if err := d.call(ctx, http.MethodPost, "/alert-rules", r, &created); err != nil {
				return err
			}
			id, _ = created["id"].(string)
		}
		var check struct {
			Hits int `json:"hits"`
		}
		if err := d.call(ctx, http.MethodPost, "/alert-rules/"+id+"/check", nil, &check); err != nil {
			return err
		}
		d.logf("   правило «%s»: срабатываний %d", r["name"], check.Hits)
	}
	return nil
}

func (d *demo) scenarios(ctx context.Context) error {
	var elems []obj
	if err := d.call(ctx, http.MethodGet, "/scenarios/elements?limit=3", nil, &elems); err != nil {
		return err
	}
	if len(elems) == 0 {
		return errors.New("нет рейтинга критичности: сначала выполните сетевой анализ")
	}
	first := elems[0]
	list := []obj{
		{
			"name":        fmt.Sprintf("Перекрытие: %v", first["name"]),
			"description": "Выпадение элемента с наибольшим ущербом в рейтинге критичности: как меняется время грузовика 20 т до МКАД и ТТК.",
			"params": obj{
				"profile": "truck_20", "zones": true, "targets": []string{"mkad", "ttk"},
				"closures": []obj{{"kind": "element", "element": first["id"], "label": first["name"]}},
			},
		},
		{
			"name":        "Новый логистический центр в промзоне Бирюлёво",
			"description": "Объект у Липецкой улицы: доступность до центра и МКАД, изохроны 15 и 30 минут.",
			"params": obj{
				"profile": "truck_20", "zones": true, "targets": []string{"center", "mkad"},
				"newFacility":      obj{"name": "Логистический центр (проект)", "lon": 37.6720, "lat": 55.5880},
				"isochroneMinutes": []int{15, 30},
			},
		},
	}
	var have []obj
	if err := d.call(ctx, http.MethodGet, "/scenarios", nil, &have); err != nil {
		return err
	}
	names := named(have)
	for _, sc := range list {
		id, ok := names[sc["name"].(string)]
		if !ok {
			var created obj
			if err := d.call(ctx, http.MethodPost, "/scenarios", sc, &created); err != nil {
				return err
			}
			id, _ = created["id"].(string)
		}
		status, err := d.waitScenario(ctx, id)
		if err != nil {
			return err
		}
		d.logf("   сценарий «%s»: %s", sc["name"], status)
	}
	return nil
}

// waitScenario ждёт окончания расчёта сценария (до 10 минут).
func (d *demo) waitScenario(ctx context.Context, id string) (string, error) {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		var sc struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := d.call(ctx, http.MethodGet, "/scenarios/"+id, nil, &sc); err != nil {
			return "", err
		}
		switch sc.Status {
		case "done":
			return sc.Status, nil
		case "failed":
			return "", fmt.Errorf("сценарий %s: %s", id, sc.Error)
		}
		if time.Now().After(deadline) {
			return sc.Status, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (d *demo) site(ctx context.Context) error {
	const name = "Распределительный центр последней мили"
	var have []obj
	if err := d.call(ctx, http.MethodGet, "/site-selection/runs", nil, &have); err != nil {
		return err
	}
	if _, ok := named(have)[name]; ok {
		return nil
	}
	params := obj{
		"resolution": 8,
		"criteria": []obj{
			{"metric": "h3.access_mkad_min", "benefit": false, "weight": 3},
			{"metric": "h3.population_5km", "benefit": true, "weight": 3},
			{"metric": "h3.share_industrial", "benefit": true, "weight": 2},
			{"metric": "h3.infra", "benefit": true, "weight": 1},
			{"metric": "h3.restricted_km", "benefit": false, "weight": 2},
		},
		"weighting":      "direct",
		"exclusions":     []string{"residential", "heritage", "parks", "green", "water"},
		"exclusionShare": 50,
		"top":            10,
		"spacingKm":      2,
	}
	if err := d.call(ctx, http.MethodPost, "/site-selection/runs", obj{"name": name, "params": params}, nil); err != nil {
		return err
	}
	d.logf("   оценка «%s»", name)
	return nil
}

func (d *demo) reports(ctx context.Context) error {
	var territories []obj
	if err := d.call(ctx, http.MethodGet, "/territories?kind=district", nil, &territories); err != nil {
		return err
	}
	district := ""
	for _, t := range territories {
		if n, _ := t["name"].(string); strings.Contains(strings.ToLower(n), "нагатинский затон") {
			district, _ = t["id"].(string)
		}
	}
	top, err := d.topFacilities(ctx, 1)
	if err != nil {
		return err
	}
	var scenarios, runs []obj
	if err := d.call(ctx, http.MethodGet, "/scenarios", nil, &scenarios); err != nil {
		return err
	}
	if err := d.call(ctx, http.MethodGet, "/site-selection/runs", nil, &runs); err != nil {
		return err
	}
	reports := []obj{
		{"template": "roadworks", "format": "pdf", "params": obj{"days": "14"}},
		{"template": "quality", "format": "pdf"},
	}
	if district != "" {
		reports = append(reports,
			obj{"template": "territory", "format": "pdf", "params": obj{"territoryId": district}},
			obj{"template": "passability", "format": "pdf", "params": obj{"territoryId": district, "profile": "truck_20"}},
		)
	}
	if len(top) > 0 {
		reports = append(reports,
			obj{"template": "facility", "format": "pdf", "params": obj{"facilityId": top[0]}},
			obj{"template": "facility", "format": "xlsx", "params": obj{"facilityId": top[0]}},
		)
	}
	for _, sc := range scenarios {
		if sc["status"] == "done" {
			reports = append(reports, obj{"template": "scenario", "format": "pdf", "params": obj{"scenarioId": sc["id"]}})
			break
		}
	}
	if len(runs) > 0 {
		reports = append(reports, obj{"template": "site", "format": "pdf", "params": obj{"siteRunId": runs[0]["id"]}})
	}
	// Уже сформированные отчёты (тот же шаблон, формат и параметры) повторно не заказываются.
	var existing []obj
	if err := d.call(ctx, http.MethodGet, "/reports", nil, &existing); err != nil {
		return err
	}
	key := func(r obj) string {
		b, _ := json.Marshal(r["params"])
		if string(b) == "null" {
			b = []byte("{}")
		}
		return fmt.Sprint(r["template"], "|", r["format"], "|", string(b))
	}
	done := map[string]bool{}
	for _, r := range existing {
		done[key(r)] = true
	}
	for _, r := range reports {
		if done[key(r)] {
			continue
		}
		var created obj
		if err := d.call(ctx, http.MethodPost, "/reports", r, &created); err != nil {
			return err
		}
		d.logf("   отчёт «%v»", created["title"])
	}

	schedules := []obj{
		{
			"template": "roadworks", "format": "pdf", "params": obj{"days": "7"}, "title": "Дорожные работы на неделю",
			"frequency": "weekly", "weekday": 1, "hour": 8,
		},
		{
			"template": "quality", "format": "xlsx", "title": "Качество данных за месяц",
			"frequency": "monthly", "monthDay": 1, "hour": 9,
		},
	}
	var have []obj
	if err := d.call(ctx, http.MethodGet, "/report-schedules", nil, &have); err != nil {
		return err
	}
	names := named(have)
	for _, sc := range schedules {
		if _, ok := names[sc["title"].(string)]; ok {
			continue
		}
		if err := d.call(ctx, http.MethodPost, "/report-schedules", sc, nil); err != nil {
			return err
		}
		d.logf("   расписание «%s»", sc["title"])
	}
	return nil
}
