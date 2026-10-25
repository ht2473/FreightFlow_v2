package analytics

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/ingest/sources/osm/osmtest"
	"freightflow/internal/platform/dbtest"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNormalize(t *testing.T) {
	t.Parallel()
	vals := map[string]float64{"a": 10, "b": 20, "c": 30}
	ents := []string{"a", "b", "c"}
	mm := normalize(vals, ents, NormMinMax, 1)
	if !near(mm["a"], 0) || !near(mm["b"], 0.5) || !near(mm["c"], 1) {
		t.Errorf("min–max %v", mm)
	}
	rev := normalize(vals, ents, NormMinMax, -1)
	if !near(rev["a"], 1) || !near(rev["c"], 0) {
		t.Errorf("обратное направление %v", rev)
	}
	z := normalize(vals, ents, NormZScore, 1)
	// z = ±1,2247 → (z+3)/6.
	if !near(z["b"], 0.5) || !near(z["c"], (math.Sqrt(1.5)+3)/6) {
		t.Errorf("z-оценка %v", z)
	}
	flat := normalize(map[string]float64{"a": 5, "b": 5}, []string{"a", "b"}, NormMinMax, 1)
	if !near(flat["a"], 0.5) {
		t.Errorf("без разброса %v", flat)
	}
	clip := normalize(map[string]float64{"a": 0, "b": 0, "c": 0, "d": 0, "e": 0, "f": 0, "g": 0, "h": 0, "i": 0, "x": 1000},
		[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "x"}, NormZScore, 1)
	if !near(clip["x"], 1) {
		t.Errorf("z ≤ 3σ: %v", clip["x"])
	}
}

func TestSpearman(t *testing.T) {
	t.Parallel()
	a := map[string]float64{"a": 1, "b": 2, "c": 3, "d": 4}
	if rho, shift := spearman(a, a); !near(rho, 1) || shift != 0 {
		t.Errorf("одинаковые: %v %d", rho, shift)
	}
	rev := map[string]float64{"a": 4, "b": 3, "c": 2, "d": 1}
	if rho, shift := spearman(a, rev); !near(rho, -1) || shift != 3 {
		t.Errorf("обратные: %v %d", rho, shift)
	}
	// Равные значения — средний ранг.
	if r := ranks(map[string]float64{"a": 5, "b": 5, "c": 1}, []string{"a", "b", "c"}); !near(r["a"], 1.5) || !near(r["c"], 3) {
		t.Errorf("ранги %v", r)
	}
}

func TestScoreAndValidate(t *testing.T) {
	t.Parallel()
	ix, _ := FindIndex("index.restrictions")
	inputs := map[string]map[string]float64{
		"territory.restricted_share": {"a": 10, "b": 20, "c": 30},
		"territory.works_per_100km":  {"a": 3, "b": 2, "c": 1},
	}
	cfg := ix.defaults()
	sc := score(ix, LevelDistrict, cfg, inputs)
	// a: 0,6·0 + 0,4·1 = 40; c: 0,6·1 + 0,4·0 = 60.
	if !near(sc["a"].score, 40) || !near(sc["c"].score, 60) || !near(sc["b"].score, 50) {
		t.Errorf("итоги %v", sc)
	}
	// Без составляющей объект в итог не входит; нулевой вес — составляющая не нужна.
	delete(inputs["territory.works_per_100km"], "c")
	if _, ok := score(ix, LevelDistrict, cfg, inputs)["c"]; ok {
		t.Error("объект без составляющей в итоге")
	}
	cfg.Weights[LevelDistrict]["territory.works_per_100km"] = 0
	if sc := score(ix, LevelDistrict, cfg, inputs); !near(sc["c"].score, 100) {
		t.Errorf("нулевой вес: %v", sc["c"])
	}
	for _, bad := range []Config{
		{Normalization: "rank", Weights: ix.defaults().Weights},
		{Normalization: NormMinMax, Weights: map[string]map[string]float64{LevelDistrict: {"territory.population": 1}}},
		{Normalization: NormMinMax, Weights: map[string]map[string]float64{LevelDistrict: {"territory.restricted_share": -1}}},
		{Normalization: NormMinMax, Weights: map[string]map[string]float64{LevelDistrict: {"territory.restricted_share": 0}}},
		{Normalization: NormMinMax, Weights: map[string]map[string]float64{LevelSegment: {"territory.restricted_share": 1}}},
	} {
		if err := ix.validate(bad); !errors.Is(err, ErrIndexConfig) {
			t.Errorf("неверные веса приняты: %+v", bad)
		}
	}
	// Все составляющие индексов — известные показатели или индексы своего уровня.
	for _, x := range Indexes {
		for level, comps := range x.Levels {
			for _, cm := range comps {
				if _, ok := FindIndex(cm.Ref); ok {
					continue
				}
				m, ok := Find(cm.Ref)
				if !ok || !contains(m.Levels, level) || (cm.Direction != 1 && cm.Direction != -1) {
					t.Errorf("%s/%s: составляющая %s", x.Code, level, cm.Ref)
				}
			}
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestComputeIndexAndConfig(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	osmtest.LoadDistrict(t, pool)
	ctx := t.Context()
	svc := New(pool)
	if _, err := svc.Recompute(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	cfg, own, err := svc.ConfigFor(ctx, "index.attractiveness", uuid.Nil)
	if err != nil || own {
		t.Fatal(err, own)
	}
	res, err := svc.Compute(ctx, "index.attractiveness", LevelDistrict, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Scores) == 0 || res.Scores[0].Rank != 1 || res.Scores[0].Score < res.Scores[len(res.Scores)-1].Score ||
		len(res.Sensitivity) != 4 {
		t.Fatalf("индекс: %d итогов, чувствительность %d", len(res.Scores), len(res.Sensitivity))
	}
	for _, sc := range res.Scores {
		if sc.Score < 0 || sc.Score > 100 || len(sc.Parts) != 4 {
			t.Fatalf("итог %+v", sc)
		}
	}
	for _, s := range res.Sensitivity {
		if s.RhoMinus < -1 || s.RhoMinus > 1 || s.RhoPlus < -1 || s.RhoPlus > 1 {
			t.Errorf("чувствительность %+v", s)
		}
	}

	// Свои веса пользователя поверх общих; сброс возвращает общие.
	var user uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO core.app_user (id, email, name, password_hash, status)
		VALUES (gen_random_uuid(), 'ix@example.com', 'Аналитик', 'x', 'active') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	mine := cloneConfig(cfg)
	mine.Weights[LevelDistrict]["territory.warehouse_density"] = 0
	mine.Normalization = NormZScore
	if err := svc.SaveConfig(ctx, "index.attractiveness", &user, user, mine); err != nil {
		t.Fatal(err)
	}
	got, own, err := svc.ConfigFor(ctx, "index.attractiveness", user)
	if err != nil || !own || got.Normalization != NormZScore || got.Weights[LevelDistrict]["territory.warehouse_density"] != 0 {
		t.Errorf("свои веса: %+v %v %v", got, own, err)
	}
	if other, _, _ := svc.ConfigFor(ctx, "index.attractiveness", uuid.New()); other.Normalization != NormMinMax {
		t.Errorf("чужие веса видны другому: %+v", other)
	}
	if err := svc.ResetConfig(ctx, "index.attractiveness", user); err != nil {
		t.Fatal(err)
	}
	if got, own, _ := svc.ConfigFor(ctx, "index.attractiveness", user); own || got.Normalization != NormMinMax {
		t.Errorf("сброс: %+v %v", got, own)
	}
	if _, err := svc.Compute(ctx, "index.attractiveness", LevelSegment, cfg); !errors.Is(err, ErrUnknown) {
		t.Errorf("уровень без индекса: %v", err)
	}
}
