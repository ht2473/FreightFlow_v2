package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/analytics"
	"freightflow/internal/platform/ids"
	"freightflow/internal/scenario/mcda"
)

// Analytics — значения показателей (модуль analytics).
type Analytics interface {
	Values(ctx context.Context, code, level string, series bool, limit int) ([]analytics.Value, error)
}

// SiteCriterionDef — критерий оценки локации из каталога показателей H3: направление по умолчанию
// (Benefit — больше лучше) и пояснение.
type SiteCriterionDef struct {
	Metric  string
	Title   string
	Unit    string
	Benefit bool
	Note    string
}

// SiteCriteria — критерии, доступные в оценке локации.
var SiteCriteria = []SiteCriterionDef{
	{"h3.access_mkad_min", "Время до МКАД", "мин", false, "выезд из города и на федеральные трассы"},
	{"h3.access_ttk_min", "Время до ТТК", "мин", false, "доставка в центральную часть города"},
	{"h3.access_rail_min", "Время до ж/д грузовых пунктов", "мин", false, "мультимодальные перевозки"},
	{"h3.population_5km", "Население в 5 км", "чел.", true, "спрос последней мили (модель)"},
	{"h3.share_industrial", "Доля промзон", "%", true, "совместимое землепользование"},
	{"h3.infra", "Инфраструктура для грузовиков", "точек", true, "АЗС, стоянки, площадки отдыха"},
	{"h3.restricted_km", "Сеть с ограничениями", "км", false, "запреты грузовиков и пределы массы и габаритов"},
	{"h3.works", "Дорожные работы", "работ", false, "действующие на дату расчёта"},
	{"h3.facilities", "Объекты логистики", "объектов", false, "конкуренция (или кластер — смените направление)"},
}

// SiteExclusionDef — исключающая зона: показатель доли ячейки, покрытой слоем.
type SiteExclusionDef struct {
	Code, Title, Metric string
}

// SiteExclusions — исключающие зоны оценки локации.
var SiteExclusions = []SiteExclusionDef{
	{"residential", "Жилая застройка", "h3.share_residential"},
	{"heritage", "Зоны охраны ОКН", "h3.share_heritage"},
	{"parks", "Парковые территории", "h3.share_parks"},
	{"green", "Природные территории", "h3.share_green"},
	{"water", "Водные объекты", "h3.share_water"},
}

// Пределы оценки локации.
const (
	minSiteCriteria = 2
	maxSiteCriteria = 10
	maxSiteTop      = 50
	// siteSensitivity — изменение веса в анализе чувствительности (±20 %).
	siteSensitivity = 0.2
	// siteScoreDigits — знаков оценки в карте пригодности.
	siteScoreDigits = 1000
)

// SiteCriterion — критерий запуска: показатель, направление и прямой вес.
type SiteCriterion struct {
	Metric  string  `json:"metric"`
	Benefit bool    `json:"benefit"`
	Weight  float64 `json:"weight"`
}

// SiteParams — параметры оценки локации.
type SiteParams struct {
	// Resolution — разрешение ячеек H3: 8 (≈ 0,74 км²) или 9 (≈ 0,11 км²).
	Resolution int             `json:"resolution"`
	Criteria   []SiteCriterion `json:"criteria"`
	// Weighting — direct (веса критериев) или ahp (матрица попарных сравнений Pairwise).
	Weighting string      `json:"weighting"`
	Pairwise  [][]float64 `json:"pairwise,omitempty"`
	// Exclusions — исключающие зоны; ячейка исключается, если доля любой из них не меньше
	// ExclusionShare процентов.
	Exclusions     []string `json:"exclusions,omitempty"`
	ExclusionShare float64  `json:"exclusionShare"`
	Top            int      `json:"top"`
	// SpacingKm — кандидаты в списке лучших не ближе этого расстояния друг к другу.
	SpacingKm float64 `json:"spacingKm"`
}

// Validate проверяет параметры и подставляет значения по умолчанию.
func (p *SiteParams) Validate() error {
	if p.Resolution == 0 {
		p.Resolution = 8
	}
	if p.Resolution != 8 && p.Resolution != 9 {
		return invalid("разрешение ячеек — 8 или 9")
	}
	n := len(p.Criteria)
	if n < minSiteCriteria || n > maxSiteCriteria {
		return invalid("критериев должно быть от %d до %d", minSiteCriteria, maxSiteCriteria)
	}
	seen := map[string]bool{}
	for _, c := range p.Criteria {
		if !slices.ContainsFunc(SiteCriteria, func(d SiteCriterionDef) bool { return d.Metric == c.Metric }) {
			return invalid("критерий %q не из каталога оценки локации", c.Metric)
		}
		if seen[c.Metric] {
			return invalid("критерий %q выбран дважды", c.Metric)
		}
		seen[c.Metric] = true
	}
	switch p.Weighting {
	case "", "direct":
		p.Weighting = "direct"
		p.Pairwise = nil
		var sum float64
		for _, c := range p.Criteria {
			if c.Weight < 0 || math.IsNaN(c.Weight) {
				return invalid("вес не может быть отрицательным")
			}
			sum += c.Weight
		}
		if sum == 0 {
			return invalid("все веса нулевые")
		}
	case "ahp":
		if len(p.Pairwise) != n {
			return invalid("матрица попарных сравнений должна быть %d×%d", n, n)
		}
		for _, row := range p.Pairwise {
			if len(row) != n {
				return invalid("матрица попарных сравнений должна быть %d×%d", n, n)
			}
		}
	default:
		return invalid("способ задания весов — direct или ahp")
	}
	for _, e := range p.Exclusions {
		if !slices.ContainsFunc(SiteExclusions, func(d SiteExclusionDef) bool { return d.Code == e }) {
			return invalid("неизвестная исключающая зона %q", e)
		}
	}
	if p.ExclusionShare == 0 {
		p.ExclusionShare = 50
	}
	if p.ExclusionShare < 1 || p.ExclusionShare > 100 {
		return invalid("порог доли исключающей зоны — от 1 до 100 %%")
	}
	if p.Top == 0 {
		p.Top = 20
	}
	if p.Top < 1 || p.Top > maxSiteTop {
		return invalid("кандидатов — от 1 до %d", maxSiteTop)
	}
	if p.SpacingKm < 0 || p.SpacingKm > 20 {
		return invalid("расстояние между кандидатами — от 0 до 20 км")
	}
	return nil
}

// SiteAHP — согласованность матрицы попарных сравнений.
type SiteAHP struct {
	LambdaMax  float64 `json:"lambdaMax"`
	CI         float64 `json:"ci"`
	CR         float64 `json:"cr"`
	Consistent bool    `json:"consistent"`
}

// SiteCell — ячейка-кандидат.
type SiteCell struct {
	H3       string  `json:"h3"`
	Rank     int     `json:"rank"`
	Score    float64 `json:"score"`
	District string  `json:"district"`
	Lon      float64 `json:"lon"`
	Lat      float64 `json:"lat"`
	// Values — значения критериев (nil — нет данных), Contributions — доли критериев в оценке.
	Values        []*float64 `json:"values"`
	Contributions []float64  `json:"contributions"`
}

// SiteSensitivity — устойчивость ранжирования к весу критерия: худшее из ±20 %.
type SiteSensitivity struct {
	Metric   string  `json:"metric"`
	Spearman float64 `json:"spearman"`
	TopKept  float64 `json:"topKept"`
}

// SiteScore — оценка ячейки для карты пригодности.
type SiteScore struct {
	H3    string  `json:"h3"`
	Score float64 `json:"score"`
}

// SiteResult — итог оценки локации.
type SiteResult struct {
	Weights     []float64         `json:"weights"`
	AHP         *SiteAHP          `json:"ahp,omitempty"`
	Cells       int               `json:"cells"`
	Candidates  int               `json:"candidates"`
	Excluded    []string          `json:"excluded"`
	Top         []SiteCell        `json:"top"`
	Scores      []SiteScore       `json:"scores"`
	Sensitivity []SiteSensitivity `json:"sensitivity"`
	// Missing — доля пропусков по критерию (пропуск в TOPSIS — худшее значение).
	Missing []float64 `json:"missing"`
}

// EvaluateSite ранжирует ячейки H3 по критериям: исключающие зоны, веса (прямые или МАИ),
// TOPSIS, лучшие кандидаты с разнесением, вклад критериев и чувствительность к весам.
func (s *Service) EvaluateSite(ctx context.Context, p SiteParams) (*SiteResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if s.analytics == nil {
		return nil, errors.New("scenario: analytics is not configured")
	}
	level := fmt.Sprintf("h3_%d", p.Resolution)
	res := &SiteResult{Excluded: []string{}}

	// Веса.
	weights := make([]float64, len(p.Criteria))
	if p.Weighting == "ahp" {
		a, err := mcda.AHP(p.Pairwise)
		if errors.Is(err, mcda.ErrMatrix) {
			return nil, invalid("матрица попарных сравнений неверна: значения положительные, a[j][i] = 1 / a[i][j]")
		}
		if err != nil {
			return nil, err
		}
		weights = a.Weights
		res.AHP = &SiteAHP{LambdaMax: a.LambdaMax, CI: a.CI, CR: a.CR, Consistent: a.Consistent()}
	} else {
		for i, c := range p.Criteria {
			weights[i] = c.Weight
		}
		w, err := mcda.Normalize(weights)
		if err != nil {
			return nil, invalid("веса неверны: %s", err)
		}
		weights = w
	}
	res.Weights = weights

	// Ячейки и исключения: доли зон есть у каждой ячейки.
	var cells []string
	excluded := map[string]bool{}
	for i, e := range p.Exclusions {
		def := SiteExclusions[slices.IndexFunc(SiteExclusions, func(d SiteExclusionDef) bool { return d.Code == e })]
		vals, err := s.analytics.Values(ctx, def.Metric, level, false, 100_000)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			if i == 0 {
				cells = append(cells, v.Entity)
			}
			if v.Value >= p.ExclusionShare {
				excluded[v.Entity] = true
			}
		}
	}
	values := make([]map[string]float64, len(p.Criteria))
	for j, c := range p.Criteria {
		vals, err := s.analytics.Values(ctx, c.Metric, level, false, 100_000)
		if err != nil {
			return nil, err
		}
		values[j] = make(map[string]float64, len(vals))
		for _, v := range vals {
			values[j][v.Entity] = v.Value
		}
	}
	if len(cells) == 0 {
		// Без исключений — ячейки из критериев с полным покрытием (доли зон есть у всех ячеек).
		vals, err := s.analytics.Values(ctx, SiteExclusions[0].Metric, level, false, 100_000)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			cells = append(cells, v.Entity)
		}
	}
	slices.Sort(cells)
	res.Cells = len(cells)
	var cand []string
	for _, h := range cells {
		if excluded[h] {
			res.Excluded = append(res.Excluded, h)
		} else {
			cand = append(cand, h)
		}
	}
	res.Candidates = len(cand)
	if len(cand) == 0 {
		return nil, invalid("все ячейки исключены: ослабьте исключающие зоны или порог доли")
	}

	x := make([][]float64, len(cand))
	missing := make([]int, len(p.Criteria))
	for i, h := range cand {
		x[i] = make([]float64, len(p.Criteria))
		for j := range p.Criteria {
			v, ok := values[j][h]
			if !ok {
				v = math.NaN()
				missing[j]++
			}
			x[i][j] = v
		}
	}
	res.Missing = make([]float64, len(missing))
	for j, m := range missing {
		res.Missing[j] = math.Round(float64(m)/float64(len(cand))*1000) / 1000
	}
	cs := make([]mcda.Criterion, len(p.Criteria))
	for j, c := range p.Criteria {
		cs[j] = mcda.Criterion{Weight: weights[j], Benefit: c.Benefit}
	}
	t, err := mcda.TOPSIS(x, cs)
	if err != nil {
		return nil, err
	}
	res.Scores = make([]SiteScore, len(cand))
	for i, h := range cand {
		res.Scores[i] = SiteScore{H3: h, Score: math.Round(t.Closeness[i]*siteScoreDigits) / siteScoreDigits}
	}

	// Лучшие кандидаты с разнесением: центры ячеек — из БД для первых по оценке.
	order := mcda.Order(t.Closeness)
	head := order[:min(len(order), max(p.Top*40, 400))]
	heads := make([]string, len(head))
	for i, k := range head {
		heads[i] = cand[k]
	}
	geo, err := s.cellInfo(ctx, heads)
	if err != nil {
		return nil, err
	}
	var chosen []SiteCell
	for _, k := range head {
		if len(chosen) >= p.Top {
			break
		}
		h := cand[k]
		g := geo[h]
		if p.SpacingKm > 0 && slices.ContainsFunc(chosen, func(c SiteCell) bool {
			return haversineKm(c.Lon, c.Lat, g.lon, g.lat) < p.SpacingKm
		}) {
			continue
		}
		vals := make([]*float64, len(p.Criteria))
		for j := range p.Criteria {
			if v := x[k][j]; !math.IsNaN(v) {
				vals[j] = &v
			}
		}
		chosen = append(chosen, SiteCell{
			H3: h, Rank: len(chosen) + 1, Score: math.Round(t.Closeness[k]*10000) / 10000, District: g.district,
			Lon: g.lon, Lat: g.lat, Values: vals, Contributions: roundAll(t.Contribution[k], 1000),
		})
	}
	res.Top = chosen

	sens, err := mcda.Sensitivities(x, cs, siteSensitivity, p.Top)
	if err != nil {
		return nil, err
	}
	worst := map[int]*SiteSensitivity{}
	for _, v := range sens {
		w := worst[v.Criterion]
		if w == nil {
			w = &SiteSensitivity{Metric: p.Criteria[v.Criterion].Metric, Spearman: 1, TopKept: 1}
			worst[v.Criterion] = w
		}
		w.Spearman = min(w.Spearman, math.Round(v.Spearman*1000)/1000)
		w.TopKept = min(w.TopKept, v.TopKept)
	}
	for j := range p.Criteria {
		res.Sensitivity = append(res.Sensitivity, *worst[j])
	}
	return res, nil
}

func roundAll(v []float64, k float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = math.Round(x*k) / k
	}
	return out
}

// haversineKm — расстояние по сфере, км.
func haversineKm(lon1, lat1, lon2, lat2 float64) float64 {
	const r = 6371.0088
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

type cellGeo struct {
	lon, lat float64
	district string
}

// cellInfo — центры ячеек и районы.
func (s *Service) cellInfo(ctx context.Context, cells []string) (map[string]cellGeo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.h3::text, ST_X(h3_cell_to_geometry(c.h3)), ST_Y(h3_cell_to_geometry(c.h3)), coalesce(a.name, '')
		FROM territory.h3_cell c LEFT JOIN territory.admin_area a ON a.id = c.district_id
		WHERE c.h3 = ANY ($1::h3index[])`, cells)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]cellGeo, len(cells))
	for rows.Next() {
		var h string
		var g cellGeo
		if err := rows.Scan(&h, &g.lon, &g.lat, &g.district); err != nil {
			return nil, err
		}
		out[h] = g
	}
	return out, rows.Err()
}

// SiteRun — сохранённая оценка локации: параметры и лучшие кандидаты на момент расчёта.
type SiteRun struct {
	ID        uuid.UUID
	Name      string
	Params    SiteParams
	Top       []SiteCell
	Weights   []float64
	AHP       *SiteAHP
	CreatedAt time.Time
}

// SaveSite считает оценку и сохраняет параметры с лучшими кандидатами.
func (s *Service) SaveSite(ctx context.Context, userID uuid.UUID, name string, p SiteParams) (*SiteRun, error) {
	if name == "" {
		return nil, invalid("не задано название")
	}
	// Параметры сохраняются в каноническом виде (с подставленными значениями по умолчанию).
	if err := p.Validate(); err != nil {
		return nil, err
	}
	r, err := s.EvaluateSite(ctx, p)
	if err != nil {
		return nil, err
	}
	params, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	summary, err := json.Marshal(siteSummary{Top: r.Top, Weights: r.Weights, AHP: r.AHP})
	if err != nil {
		return nil, err
	}
	return scanSiteRun(s.pool.QueryRow(ctx, `INSERT INTO scenario.site_selection (id, user_id, name, params, summary)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+siteColumns, ids.New(), userID, name, params, summary))
}

type siteSummary struct {
	Top     []SiteCell `json:"top"`
	Weights []float64  `json:"weights"`
	AHP     *SiteAHP   `json:"ahp,omitempty"`
}

const siteColumns = `id, name, params, summary, created_at`

func scanSiteRun(r pgx.Row) (*SiteRun, error) {
	var out SiteRun
	var params, summary []byte
	err := r.Scan(&out.ID, &out.Name, &params, &summary, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(params, &out.Params); err != nil {
		return nil, err
	}
	var sum siteSummary
	if err := json.Unmarshal(summary, &sum); err != nil {
		return nil, err
	}
	out.Top, out.Weights, out.AHP = sum.Top, sum.Weights, sum.AHP
	return &out, nil
}

// SiteRuns — сохранённые оценки пользователя, новые первыми.
func (s *Service) SiteRuns(ctx context.Context, userID uuid.UUID, limit int) ([]SiteRun, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+siteColumns+` FROM scenario.site_selection
		WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SiteRun, error) {
		x, err := scanSiteRun(r)
		if err != nil {
			return SiteRun{}, err
		}
		return *x, nil
	})
}

// SiteRunByID — сохранённая оценка пользователя.
func (s *Service) SiteRunByID(ctx context.Context, userID, id uuid.UUID) (*SiteRun, error) {
	return scanSiteRun(s.pool.QueryRow(ctx, `SELECT `+siteColumns+` FROM scenario.site_selection
		WHERE id = $1 AND user_id = $2`, id, userID))
}

// DeleteSiteRun удаляет сохранённую оценку пользователя.
func (s *Service) DeleteSiteRun(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM scenario.site_selection WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SiteCells — сетка ячеек H3 разрешения res (GeoJSON FeatureCollection; свойство h3).
func (s *Service) SiteCells(ctx context.Context, res int) (json.RawMessage, error) {
	if res != 8 && res != 9 {
		return nil, invalid("разрешение ячеек — 8 или 9")
	}
	var out json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT jsonb_build_object('type', 'FeatureCollection', 'features', coalesce(jsonb_agg(jsonb_build_object(
		           'type', 'Feature', 'properties', jsonb_build_object('h3', h3::text),
		           'geometry', ST_AsGeoJSON(geom, 5)::jsonb)), '[]'::jsonb))
		FROM territory.h3_cell WHERE res = $1`, res).Scan(&out)
	return out, err
}
