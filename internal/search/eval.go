package search

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

//go:embed evalset.json
var evalSet []byte

// Case — запрос набора оценки: ожидаемый документ (вид и заголовок или идентификатор) или,
// для запросов с фильтрами, ожидаемые подписи разобранных фильтров.
type Case struct {
	Category string   `json:"category"`
	Q        string   `json:"q"`
	Kind     string   `json:"kind,omitempty"`
	Title    string   `json:"title,omitempty"`
	Ref      string   `json:"ref,omitempty"`
	Chips    []string `json:"chips,omitempty"`
}

// EvalSet — встроенный набор запросов (собран по данным базы разработки и вручную для фильтров).
func EvalSet() ([]Case, error) {
	var cs []Case
	err := json.Unmarshal(evalSet, &cs)
	return cs, err
}

// Metrics — показатели качества группы запросов.
type Metrics struct {
	Queries int
	NDCG10  float64
	MRR     float64
	Recall  float64 // доля запросов с ожидаемым документом в первых 20
	Suggest float64 // доля запросов, где подсказка по началу запроса находит документ в первых 8
}

// Report — итог оценки.
type Report struct {
	Categories map[string]*Metrics
	Total      Metrics
	// Filters — доля запросов с фильтрами, разобранных точно.
	Filters        float64
	FilterQueries  int
	FilterFailures []string
	Misses         []string
	SearchP50      time.Duration
	SearchP95      time.Duration
	SuggestP50     time.Duration
	SuggestP95     time.Duration
	// Slowest — самые медленные запросы поиска.
	Slowest []string
}

// relevant — документ совпадает с ожидаемым.
func relevant(c Case, h Hit) bool {
	if h.Kind != c.Kind {
		return false
	}
	if c.Ref != "" {
		return h.Ref == c.Ref
	}
	return core(h.Title) == core(c.Title)
}

// rank — место ожидаемого документа (с 1), 0 — не найден.
func rank(c Case, hits []Hit) int {
	for i, h := range hits {
		if relevant(c, h) {
			return i + 1
		}
	}
	return 0
}

// prefix — начало запроса для подсказок: около 60 % символов, но не меньше трёх.
func prefix(q string) string {
	r := []rune(q)
	n := max(3, len(r)*6/10)
	return string(r[:min(n, len(r))])
}

// Evaluate прогоняет набор: поиск от имени системы (все виды документов) и подсказки.
func (s *Service) Evaluate(ctx context.Context, cases []Case) (*Report, error) {
	rep := &Report{Categories: map[string]*Metrics{}}
	var searchTimes, suggestTimes []time.Duration
	type timed struct {
		q string
		d time.Duration
	}
	var slow []timed
	var okFilters int
	all := Request{Kinds: AllKinds(), PerKind: 20, NoLog: true}
	for _, c := range cases {
		req := all
		req.Q = c.Q
		start := time.Now()
		res, err := s.Search(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", c.Q, err)
		}
		searchTimes = append(searchTimes, time.Since(start))
		slow = append(slow, timed{c.Q, time.Since(start)})
		if len(c.Chips) > 0 {
			rep.FilterQueries++
			got := make([]string, len(res.Parsed.Chips))
			for i, ch := range res.Parsed.Chips {
				got[i] = ch.Label
			}
			want := slices.Clone(c.Chips)
			slices.Sort(got)
			slices.Sort(want)
			if slices.Equal(got, want) {
				okFilters++
			} else {
				rep.FilterFailures = append(rep.FilterFailures, fmt.Sprintf("%s: %s, ожидалось %s", c.Q,
					strings.Join(got, "; "), strings.Join(want, "; ")))
			}
			continue
		}
		hits := slices.Clone(res.Hits)
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
		r := rank(c, hits)

		start = time.Now()
		sreq := all
		sreq.Q = prefix(c.Q)
		sug, err := s.Suggest(ctx, sreq, 8)
		if err != nil {
			return nil, fmt.Errorf("suggest %q: %w", c.Q, err)
		}
		suggestTimes = append(suggestTimes, time.Since(start))

		m := rep.Categories[c.Category]
		if m == nil {
			m = &Metrics{}
			rep.Categories[c.Category] = m
		}
		for _, x := range []*Metrics{m, &rep.Total} {
			x.Queries++
			if r > 0 && r <= 10 {
				x.NDCG10 += 1 / math.Log2(float64(r)+1)
			}
			if r > 0 {
				x.MRR += 1 / float64(r)
			}
			if r > 0 && r <= 20 {
				x.Recall++
			}
			if rank(c, sug) > 0 {
				x.Suggest++
			}
		}
		if r == 0 || r > 10 {
			rep.Misses = append(rep.Misses, fmt.Sprintf("%s [%s]: место %d", c.Q, c.Category, r))
		}
	}
	for _, m := range append(mapValues(rep.Categories), &rep.Total) {
		if m.Queries > 0 {
			n := float64(m.Queries)
			m.NDCG10, m.MRR, m.Recall, m.Suggest = m.NDCG10/n, m.MRR/n, m.Recall/n, m.Suggest/n
		}
	}
	if rep.FilterQueries > 0 {
		rep.Filters = float64(okFilters) / float64(rep.FilterQueries)
	}
	rep.SearchP50, rep.SearchP95 = percentile(searchTimes, 0.5), percentile(searchTimes, 0.95)
	sort.Slice(slow, func(i, j int) bool { return slow[i].d > slow[j].d })
	for _, x := range slow[:min(5, len(slow))] {
		rep.Slowest = append(rep.Slowest, fmt.Sprintf("%s — %s", x.q, x.d.Round(time.Millisecond)))
	}
	rep.SuggestP50, rep.SuggestP95 = percentile(suggestTimes, 0.5), percentile(suggestTimes, 0.95)
	return rep, nil
}

func mapValues(m map[string]*Metrics) []*Metrics {
	out := make([]*Metrics, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[min(len(s)-1, int(math.Ceil(p*float64(len(s))))-1)]
}

// Markdown — отчёт для папки документации.
func (r *Report) Markdown(date time.Time, documents int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Оценка качества поиска — %s\n\n", date.Format("02.01.2006"))
	fmt.Fprintf(&b, "Набор: %d запросов (`internal/search/evalset.json`), индекс: %d документов. Поиск — от имени системы, все виды документов. "+
		"Ожидаемый документ — сущность, из названия которой составлен запрос (опечатки — перестановка двух букв в самом длинном слове); "+
		"запросы с фильтрами составлены вручную.\n\n", r.Total.Queries+r.FilterQueries, documents)
	b.WriteString("| Группа | Запросов | nDCG@10 | MRR | Recall@20 | Подсказки@8 |\n|---|---:|---:|---:|---:|---:|\n")
	cats := make([]string, 0, len(r.Categories))
	for k := range r.Categories {
		cats = append(cats, k)
	}
	slices.Sort(cats)
	row := func(name string, m *Metrics) {
		fmt.Fprintf(&b, "| %s | %d | %.3f | %.3f | %.3f | %.3f |\n", name, m.Queries, m.NDCG10, m.MRR, m.Recall, m.Suggest)
	}
	for _, k := range cats {
		row(k, r.Categories[k])
	}
	row("**всего**", &r.Total)
	fmt.Fprintf(&b, "\nРазбор фильтров: %d из %d запросов точно (%.0f %%).\n\n", int(math.Round(r.Filters*float64(r.FilterQueries))),
		r.FilterQueries, r.Filters*100)
	fmt.Fprintf(&b, "Задержка поиска: p50 %s, p95 %s; подсказок: p50 %s, p95 %s.\n", r.SearchP50.Round(time.Millisecond),
		r.SearchP95.Round(time.Millisecond), r.SuggestP50.Round(time.Millisecond), r.SuggestP95.Round(time.Millisecond))
	if len(r.Slowest) > 0 {
		b.WriteString("\nСамые медленные запросы: " + strings.Join(r.Slowest, "; ") + ".\n")
	}
	if len(r.Misses) > 0 {
		b.WriteString("\n## Ожидаемый документ ниже 10-го места\n\n")
		for _, m := range r.Misses {
			fmt.Fprintf(&b, "- %s\n", m)
		}
	}
	if len(r.FilterFailures) > 0 {
		b.WriteString("\n## Неточный разбор фильтров\n\n")
		for _, m := range r.FilterFailures {
			fmt.Fprintf(&b, "- %s\n", m)
		}
	}
	return b.String()
}
