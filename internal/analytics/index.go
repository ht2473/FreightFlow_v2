package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Component — составляющая индекса: показатель или другой индекс, направление (+1 — больше
// лучше, −1 — меньше лучше) и вес по умолчанию.
type Component struct {
	Ref       string
	Direction int
	Weight    float64
}

// Index — сводный индекс: составляющие по уровням.
type Index struct {
	Code   string
	Title  string
	Method string
	Levels map[string][]Component
}

// Нормировка составляющих.
const (
	NormMinMax = "minmax"
	NormZScore = "zscore"
)

// ErrIndexConfig — неверные веса или нормировка.
var ErrIndexConfig = errors.New("analytics: invalid index config")

// Indexes — сводные индексы (01-product.md §5).
var Indexes = []Index{
	{
		Code: "index.accessibility", Title: "Индекс доступности",
		Method: "Насколько легко грузовику добраться до объектов территории и от них: время до МКАД, доля сети без " +
			"ограничений, дорожные работы, близость объектов к населению.",
		Levels: map[string][]Component{
			LevelDistrict: {
				{"territory.median_mkad_min", -1, 0.35},
				{"territory.restricted_share", -1, 0.25},
				{"territory.works_per_100km", -1, 0.1},
				{"territory.population_coverage", 1, 0.3},
			},
			LevelH3r8: {{"h3.mkad_min", -1, 0.6}, {"h3.restricted_km", -1, 0.3}, {"h3.works", -1, 0.1}},
		},
	},
	{
		Code: "index.infrastructure", Title: "Индекс обеспеченности инфраструктурой",
		Method: "Стоянки грузовиков, площадки отдыха и АЗС относительно магистральной сети.",
		Levels: map[string][]Component{
			LevelDistrict: {{"territory.parking_per_100km", 1, 0.5}, {"territory.fuel_per_100km", 1, 0.5}},
			LevelH3r8:     {{"h3.infra", 1, 1}},
		},
	},
	{
		Code: "index.restrictions", Title: "Индекс ограничений",
		Method: "Стеснённость движения грузовиков: доля сети с запретами и пределами, интенсивность дорожных работ. " +
			"Больше — ограничений больше.",
		Levels: map[string][]Component{
			LevelDistrict: {{"territory.restricted_share", 1, 0.6}, {"territory.works_per_100km", 1, 0.4}},
			LevelH3r8:     {{"h3.restricted_km", 1, 0.7}, {"h3.works", 1, 0.3}},
		},
	},
	{
		Code: "index.attractiveness", Title: "Индекс логистической привлекательности",
		Method: "Взвешенная сумма индексов доступности, обеспеченности инфраструктурой и ограничений (с обратным знаком), " +
			"для районов — и плотности складских площадей (сложившийся рынок).",
		Levels: map[string][]Component{
			LevelDistrict: {
				{"index.accessibility", 1, 0.4},
				{"index.infrastructure", 1, 0.25},
				{"index.restrictions", -1, 0.2},
				{"territory.warehouse_density", 1, 0.15},
			},
			LevelH3r8: {{"index.accessibility", 1, 0.45}, {"index.infrastructure", 1, 0.25}, {"index.restrictions", -1, 0.3}},
		},
	},
}

// FindIndex — индекс по коду.
func FindIndex(code string) (Index, bool) {
	for _, ix := range Indexes {
		if ix.Code == code {
			return ix, true
		}
	}
	return Index{}, false
}

// Config — веса составляющих по уровням и нормировка.
type Config struct {
	Weights       map[string]map[string]float64 `json:"weights"`
	Normalization string                        `json:"normalization"`
}

// defaults — веса из описания индекса.
func (ix Index) defaults() Config {
	c := Config{Weights: map[string]map[string]float64{}, Normalization: NormMinMax}
	for level, comps := range ix.Levels {
		c.Weights[level] = map[string]float64{}
		for _, cm := range comps {
			c.Weights[level][cm.Ref] = cm.Weight
		}
	}
	return c
}

// validate проверяет веса: известные составляющие, неотрицательные, сумма на уровне больше нуля.
func (ix Index) validate(c Config) error {
	if c.Normalization != NormMinMax && c.Normalization != NormZScore {
		return fmt.Errorf("%w: нормировка %q", ErrIndexConfig, c.Normalization)
	}
	for level, ws := range c.Weights {
		comps, ok := ix.Levels[level]
		if !ok {
			return fmt.Errorf("%w: уровень %s", ErrIndexConfig, level)
		}
		sum := 0.0
		for ref, w := range ws {
			if !slices.ContainsFunc(comps, func(cm Component) bool { return cm.Ref == ref }) {
				return fmt.Errorf("%w: составляющая %s", ErrIndexConfig, ref)
			}
			if w < 0 || math.IsNaN(w) || w > 100 {
				return fmt.Errorf("%w: вес %s = %v", ErrIndexConfig, ref, w)
			}
			sum += w
		}
		if sum <= 0 {
			return fmt.Errorf("%w: сумма весов %s", ErrIndexConfig, level)
		}
	}
	return nil
}

// ConfigFor — настройки индекса для пользователя: свои, иначе по умолчанию (администратор),
// иначе из описания; own — есть свои.
func (s *Service) ConfigFor(ctx context.Context, code string, user uuid.UUID) (Config, bool, error) {
	ix, ok := FindIndex(code)
	if !ok {
		return Config{}, false, fmt.Errorf("%w: %s", ErrUnknown, code)
	}
	cfg := ix.defaults()
	rows, err := s.pool.Query(ctx, `SELECT owner_id IS NOT NULL, weights, normalization FROM analytics.index_config
		WHERE index_code = $1 AND (owner_id IS NULL OR owner_id = $2) ORDER BY owner_id IS NOT NULL`, code, user)
	if err != nil {
		return cfg, false, err
	}
	defer rows.Close()
	own := false
	for rows.Next() {
		var mine bool
		var raw []byte
		var norm string
		if err := rows.Scan(&mine, &raw, &norm); err != nil {
			return cfg, false, err
		}
		var ws map[string]map[string]float64
		if err := json.Unmarshal(raw, &ws); err != nil {
			return cfg, false, err
		}
		// Сохранённые веса поверх описания: новые составляющие получают вес по умолчанию.
		for level, m := range ws {
			for ref, w := range m {
				if _, ok := cfg.Weights[level][ref]; ok {
					cfg.Weights[level][ref] = w
				}
			}
		}
		cfg.Normalization = norm
		own = own || mine
	}
	return cfg, own, rows.Err()
}

// SaveConfig сохраняет веса: owner — свои, nil — по умолчанию для всех.
func (s *Service) SaveConfig(ctx context.Context, code string, owner *uuid.UUID, by uuid.UUID, c Config) error {
	ix, ok := FindIndex(code)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknown, code)
	}
	if err := ix.validate(c); err != nil {
		return err
	}
	raw, err := json.Marshal(c.Weights)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO analytics.index_config (index_code, owner_id, weights, normalization, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT ON CONSTRAINT index_config_key DO UPDATE SET weights = excluded.weights,
		    normalization = excluded.normalization, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		code, owner, raw, c.Normalization, by)
	return err
}

// ResetConfig удаляет свои веса пользователя.
func (s *Service) ResetConfig(ctx context.Context, code string, owner uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM analytics.index_config WHERE index_code = $1 AND owner_id = $2`, code, owner)
	return err
}

// Part — нормированная составляющая индекса у объекта.
type Part struct {
	Ref        string
	Value      float64 // исходное значение (у вложенного индекса — его итог)
	Normalized float64 // 0–1 с учётом направления
	Weight     float64
}

// Score — итог индекса объекта уровня 0–100.
type Score struct {
	Entity string
	Label  string
	Score  float64
	Rank   int
	Parts  []Part
}

// Sensitivity — устойчивость рангов при изменении веса одной составляющей на ±20 %.
type Sensitivity struct {
	Ref      string
	RhoMinus float64 // корреляция Спирмена с исходными рангами при весе −20 %
	RhoPlus  float64
	MaxShift int // наибольший сдвиг места
	Entities int
}

// IndexResult — значения индекса на уровне и анализ чувствительности.
type IndexResult struct {
	Code          string
	Level         string
	Normalization string
	Weights       map[string]float64
	Scores        []Score
	Sensitivity   []Sensitivity
}

// Compute считает индекс на уровне по настройкам cfg; объекты без какой-либо составляющей с
// ненулевым весом в итог не входят.
func (s *Service) Compute(ctx context.Context, code, level string, cfg Config) (*IndexResult, error) {
	ix, ok := FindIndex(code)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknown, code)
	}
	if _, ok := ix.Levels[level]; !ok {
		return nil, fmt.Errorf("%w: уровень %s у %s", ErrUnknown, level, code)
	}
	if err := ix.validate(cfg); err != nil {
		return nil, err
	}
	inputs, labels, err := s.indexInputs(ctx, ix, level)
	if err != nil {
		return nil, err
	}
	scores := score(ix, level, cfg, inputs)
	res := &IndexResult{Code: code, Level: level, Normalization: cfg.Normalization, Weights: cfg.Weights[level]}
	res.Scores = ranked(scores, labels)
	// Чувствительность: вес одной составляющей ±20 %.
	base := scoreMap(scores)
	for _, cm := range ix.Levels[level] {
		if cfg.Weights[level][cm.Ref] == 0 {
			continue
		}
		sens := Sensitivity{Ref: cm.Ref, Entities: len(base)}
		for _, k := range []float64{0.8, 1.2} {
			alt := cloneConfig(cfg)
			alt.Weights[level][cm.Ref] *= k
			other := scoreMap(score(ix, level, alt, inputs))
			rho, shift := spearman(base, other)
			if k < 1 {
				sens.RhoMinus = rho
			} else {
				sens.RhoPlus = rho
			}
			sens.MaxShift = max(sens.MaxShift, shift)
		}
		res.Sensitivity = append(res.Sensitivity, sens)
	}
	return res, nil
}

// indexInputs — значения показателей-составляющих (и составляющих вложенных индексов) на уровне.
func (s *Service) indexInputs(ctx context.Context, ix Index, level string) (map[string]map[string]float64, map[string]string, error) {
	var refs []string
	var collect func(ix Index)
	collect = func(ix Index) {
		for _, cm := range ix.Levels[level] {
			if sub, ok := FindIndex(cm.Ref); ok {
				collect(sub)
			} else if !slices.Contains(refs, cm.Ref) {
				refs = append(refs, cm.Ref)
			}
		}
	}
	collect(ix)
	rows, err := s.pool.Query(ctx, `
		SELECT v.metric_code, v.entity, v.value,
		       CASE v.level WHEN 'district' THEN (SELECT name FROM territory.admin_area WHERE id::text = v.entity)
		                    WHEN 'okrug' THEN (SELECT coalesce(short_name, name) FROM territory.admin_area WHERE id::text = v.entity)
		                    ELSE v.entity END
		FROM analytics.metric_value v
		WHERE v.level = $1 AND v.period IS NULL AND v.metric_code = ANY($2)`, level, refs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	inputs := map[string]map[string]float64{}
	labels := map[string]string{}
	for rows.Next() {
		var code, entity string
		var v float64
		var label *string
		if err := rows.Scan(&code, &entity, &v, &label); err != nil {
			return nil, nil, err
		}
		if inputs[code] == nil {
			inputs[code] = map[string]float64{}
		}
		inputs[code][entity] = v
		if label != nil {
			labels[entity] = *label
		}
	}
	return inputs, labels, rows.Err()
}

// entityScore — итог и составляющие объекта.
type entityScore struct {
	score float64
	parts []Part
}

// score — итоги индекса по объектам уровня.
func score(ix Index, level string, cfg Config, inputs map[string]map[string]float64) map[string]entityScore {
	comps := ix.Levels[level]
	// Значения составляющих: показатели — как есть, вложенные индексы — их итог.
	values := map[string]map[string]float64{}
	for _, cm := range comps {
		if sub, ok := FindIndex(cm.Ref); ok {
			m := map[string]float64{}
			for e, sc := range score(sub, level, cfg.sub(sub), inputs) {
				m[e] = sc.score
			}
			values[cm.Ref] = m
		} else {
			values[cm.Ref] = inputs[cm.Ref]
		}
	}
	// Объекты, у которых есть все составляющие с ненулевым весом.
	var entities []string
	for _, cm := range comps {
		if cfg.Weights[level][cm.Ref] == 0 {
			continue
		}
		if entities == nil {
			for e := range values[cm.Ref] {
				entities = append(entities, e)
			}
			continue
		}
		entities = slices.DeleteFunc(entities, func(e string) bool { _, ok := values[cm.Ref][e]; return !ok })
	}
	norm := map[string]map[string]float64{}
	for _, cm := range comps {
		norm[cm.Ref] = normalize(values[cm.Ref], entities, cfg.Normalization, cm.Direction)
	}
	out := map[string]entityScore{}
	for _, e := range entities {
		var sum, wsum float64
		var parts []Part
		for _, cm := range comps {
			w := cfg.Weights[level][cm.Ref]
			if w == 0 {
				continue
			}
			n := norm[cm.Ref][e]
			sum += w * n
			wsum += w
			parts = append(parts, Part{Ref: cm.Ref, Value: values[cm.Ref][e], Normalized: n, Weight: w})
		}
		out[e] = entityScore{score: sum / wsum * 100, parts: parts}
	}
	return out
}

// sub — настройки вложенного индекса: его веса по умолчанию, нормировка — общая.
func (c Config) sub(ix Index) Config {
	d := ix.defaults()
	d.Normalization = c.Normalization
	return d
}

// normalize — значения 0–1 по объектам: min–max или z-оценка, усечённая до ±3 σ; направление −1
// переворачивает шкалу. Без разброса — 0,5.
func normalize(vals map[string]float64, entities []string, method string, dir int) map[string]float64 {
	out := map[string]float64{}
	if len(entities) == 0 {
		return out
	}
	xs := make([]float64, 0, len(entities))
	for _, e := range entities {
		xs = append(xs, vals[e])
	}
	lo, hi := slices.Min(xs), slices.Max(xs)
	var mean, sd float64
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	for _, x := range xs {
		sd += (x - mean) * (x - mean)
	}
	sd = math.Sqrt(sd / float64(len(xs)))
	for _, e := range entities {
		v := vals[e]
		var n float64
		switch {
		case method == NormZScore && sd > 0:
			z := math.Max(-3, math.Min(3, (v-mean)/sd))
			n = (z + 3) / 6
		case method == NormMinMax && hi > lo:
			n = (v - lo) / (hi - lo)
		default:
			n = 0.5
		}
		if dir < 0 {
			n = 1 - n
		}
		out[e] = n
	}
	return out
}

func cloneConfig(c Config) Config {
	out := Config{Normalization: c.Normalization, Weights: map[string]map[string]float64{}}
	for l, m := range c.Weights {
		out.Weights[l] = map[string]float64{}
		for k, v := range m {
			out.Weights[l][k] = v
		}
	}
	return out
}

func scoreMap(m map[string]entityScore) map[string]float64 {
	out := make(map[string]float64, len(m))
	for e, s := range m {
		out[e] = s.score
	}
	return out
}

// ranked — итоги по убыванию с местами.
func ranked(m map[string]entityScore, labels map[string]string) []Score {
	out := make([]Score, 0, len(m))
	for e, s := range m {
		out = append(out, Score{Entity: e, Label: labels[e], Score: s.score, Parts: s.parts})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.Compare(out[i].Entity, out[j].Entity) < 0
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// ranks — места по убыванию (при равенстве — средний ранг).
func ranks(m map[string]float64, keys []string) map[string]float64 {
	idx := slices.Clone(keys)
	sort.Slice(idx, func(i, j int) bool { return m[idx[i]] > m[idx[j]] })
	out := map[string]float64{}
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && m[idx[j+1]] == m[idx[i]] {
			j++
		}
		r := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			out[idx[k]] = r
		}
		i = j + 1
	}
	return out
}

// spearman — корреляция рангов двух оценок одних объектов и наибольший сдвиг места.
func spearman(a, b map[string]float64) (float64, int) {
	keys := make([]string, 0, len(a))
	for k := range a {
		if _, ok := b[k]; ok {
			keys = append(keys, k)
		}
	}
	if len(keys) < 2 {
		return 1, 0
	}
	ra, rb := ranks(a, keys), ranks(b, keys)
	var ma, mb float64
	for _, k := range keys {
		ma += ra[k]
		mb += rb[k]
	}
	n := float64(len(keys))
	ma, mb = ma/n, mb/n
	var cov, va, vb float64
	shift := 0
	for _, k := range keys {
		da, db := ra[k]-ma, rb[k]-mb
		cov += da * db
		va += da * da
		vb += db * db
		shift = max(shift, int(math.Abs(ra[k]-rb[k])))
	}
	if va == 0 || vb == 0 {
		return 1, shift
	}
	return cov / math.Sqrt(va*vb), shift
}
