package scenario

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"

	"freightflow/internal/analytics"
	"freightflow/internal/platform/dbtest"
)

// fakeAnalytics — значения показателей по ячейкам.
type fakeAnalytics map[string]map[string]float64

func (f fakeAnalytics) Values(_ context.Context, code, _ string, _ bool, _ int) ([]analytics.Value, error) {
	var out []analytics.Value
	for e, v := range f[code] {
		out = append(out, analytics.Value{Code: code, Entity: e, Value: v})
	}
	return out, nil
}

func TestSiteValidate(t *testing.T) {
	t.Parallel()
	ok := SiteParams{Criteria: []SiteCriterion{{Metric: "h3.access_mkad_min", Weight: 1}, {Metric: "h3.population_5km", Benefit: true, Weight: 1}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Resolution != 8 || ok.Weighting != "direct" || ok.ExclusionShare != 50 || ok.Top != 20 {
		t.Errorf("defaults = %+v", ok)
	}
	bad := map[string]SiteParams{
		"один критерий": {Criteria: ok.Criteria[:1]},
		"чужой":         {Criteria: []SiteCriterion{{Metric: "facility.mkad_min", Weight: 1}, ok.Criteria[1]}},
		"дважды":        {Criteria: []SiteCriterion{ok.Criteria[0], ok.Criteria[0]}},
		"нули":          {Criteria: []SiteCriterion{{Metric: "h3.access_mkad_min"}, {Metric: "h3.population_5km"}}},
		"матрица":       {Criteria: ok.Criteria, Weighting: "ahp", Pairwise: [][]float64{{1}}},
		"зона":          {Criteria: ok.Criteria, Exclusions: []string{"desert"}},
		"разрешение":    {Criteria: ok.Criteria, Resolution: 7},
		"порог":         {Criteria: ok.Criteria, ExclusionShare: 120},
	}
	for name, p := range bad {
		var inv *ErrInvalid
		if err := p.Validate(); !errors.As(err, &inv) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestEvaluateSite(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	// Пять ячеек: «e» — жилая застройка (исключается), у «d» нет данных о населении.
	fa := fakeAnalytics{
		"h3.access_mkad_min":   {"a": 5, "b": 10, "c": 20, "d": 8, "e": 1},
		"h3.population_5km":    {"a": 50_000, "b": 200_000, "c": 100_000, "e": 900_000},
		"h3.share_residential": {"a": 0, "b": 10, "c": 40, "d": 0, "e": 90},
	}
	svc := New(pool, nil, &fakeRouting{}, fa)
	ctx := t.Context()
	p := SiteParams{
		Criteria: []SiteCriterion{
			{Metric: "h3.access_mkad_min", Weight: 3},
			{Metric: "h3.population_5km", Benefit: true, Weight: 1},
		},
		Exclusions: []string{"residential"},
		Top:        3,
	}
	r, err := svc.EvaluateSite(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Cells != 5 || r.Candidates != 4 || len(r.Excluded) != 1 || r.Excluded[0] != "e" {
		t.Fatalf("cells %d, candidates %d, excluded %v", r.Cells, r.Candidates, r.Excluded)
	}
	if math.Abs(r.Weights[0]-0.75) > 1e-9 || len(r.Scores) != 4 {
		t.Errorf("weights %v, scores %d", r.Weights, len(r.Scores))
	}
	if len(r.Top) != 3 || r.Top[0].H3 != "a" || r.Top[2].H3 == "c" {
		t.Errorf("top = %+v", r.Top)
	}
	// У «d» нет населения: пропуск — худшее значение, в выдаче — nil.
	for _, c := range r.Top {
		if c.H3 == "d" && c.Values[1] != nil {
			t.Errorf("d population = %v, want nil", *c.Values[1])
		}
		var sum float64
		for _, v := range c.Contributions {
			sum += v
		}
		if sum > 0 && math.Abs(sum-1) > 0.01 {
			t.Errorf("%s contributions sum = %v", c.H3, sum)
		}
	}
	if r.Missing[1] != 0.25 || len(r.Sensitivity) != 2 {
		t.Errorf("missing %v, sensitivity %+v", r.Missing, r.Sensitivity)
	}

	// МАИ: время в 3 раза важнее населения — те же веса 0,75 и 0,25, CR = 0.
	p.Weighting = "ahp"
	p.Pairwise = [][]float64{{1, 3}, {1.0 / 3, 1}}
	ra, err := svc.EvaluateSite(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if ra.AHP == nil || !ra.AHP.Consistent || math.Abs(ra.Weights[0]-0.75) > 1e-6 || ra.Top[0].H3 != r.Top[0].H3 {
		t.Errorf("ahp = %+v weights %v", ra.AHP, ra.Weights)
	}
	p.Pairwise = [][]float64{{1, 3}, {3, 1}}
	var inv *ErrInvalid
	if _, err := svc.EvaluateSite(ctx, p); !errors.As(err, &inv) {
		t.Errorf("non-reciprocal matrix: %v", err)
	}

	// Всё исключено.
	p = SiteParams{Criteria: p.Criteria, Exclusions: []string{"residential"}, ExclusionShare: 1, Weighting: "direct"}
	p.Criteria[0].Weight, p.Criteria[1].Weight = 1, 1
	fa["h3.share_residential"] = map[string]float64{"a": 5, "b": 5}
	if _, err := svc.EvaluateSite(ctx, p); !errors.As(err, &inv) {
		t.Errorf("all excluded: %v", err)
	}
}

func TestSiteRuns(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name) VALUES ($1, 'site@example.com', 'Локация')`, user); err != nil {
		t.Fatal(err)
	}
	fa := fakeAnalytics{
		"h3.access_mkad_min":   {"a": 5, "b": 10},
		"h3.population_5km":    {"a": 1, "b": 2},
		"h3.share_residential": {"a": 0, "b": 0},
	}
	svc := New(pool, nil, &fakeRouting{}, fa)
	p := SiteParams{Criteria: []SiteCriterion{{Metric: "h3.access_mkad_min", Weight: 1}, {Metric: "h3.population_5km", Benefit: true, Weight: 1}}}
	run, err := svc.SaveSite(ctx, user, "Склад на востоке", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Top) != 2 || run.Params.Top != 20 {
		t.Errorf("run = %+v", run)
	}
	list, err := svc.SiteRuns(ctx, user, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if _, err := svc.SiteRunByID(ctx, uuid.New(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign run: %v", err)
	}
	if err := svc.DeleteSiteRun(ctx, user, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSiteRun(ctx, user, run.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}
