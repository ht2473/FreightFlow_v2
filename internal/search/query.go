package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"freightflow/internal/facility"
	"freightflow/internal/platform/db"
)

// Request — поисковый запрос пользователя.
type Request struct {
	Q      string
	UserID uuid.UUID
	Roles  []string
	// Kinds — виды документов, доступные пользователю.
	Kinds []string
	// Center — центр видимой области карты: близкие документы выше.
	Center *Point
	// PerKind — документов каждого вида, не больше.
	PerKind int
	// NoLog — не писать в журнал запросов (оценка качества).
	NoLog bool
}

// Hit — найденный документ.
type Hit struct {
	Kind     string
	Ref      string
	Title    string
	Subtitle string
	Point    *Point
	Score    float64
}

// Result — ответ поиска: разобранные фильтры, документы по видам, объекты по фильтрам.
type Result struct {
	Parsed Parsed
	// Near — точка места из «рядом с …», если оно найдено.
	Near *Hit
	Hits []Hit
	// Facilities — объекты реестра по фильтрам (если запрос — про объекты).
	Facilities *facility.Page
	// Fuzzy — документы найдены по сходству написания (опечатки).
	Fuzzy bool
	Took  time.Duration
}

// parserTTL — срок словаря территорий.
const parserTTL = 10 * time.Minute

type parserCache struct {
	mu     sync.Mutex
	parser *Parser
	at     time.Time
}

// Parser — разборщик со словарём округов и районов из базы (обновляется раз в 10 минут).
func (s *Service) Parser(ctx context.Context) (*Parser, error) {
	s.parsers.mu.Lock()
	defer s.parsers.mu.Unlock()
	if s.parsers.parser != nil && time.Since(s.parsers.at) < parserTTL {
		return s.parsers.parser, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, kind, name, coalesce(short_name, '') FROM territory.admin_area
		WHERE kind IN ('okrug', 'district')`)
	if err != nil {
		return nil, err
	}
	areas, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Area, error) {
		var a Area
		return a, r.Scan(&a.ID, &a.Kind, &a.Name, &a.ShortName)
	})
	if err != nil {
		return nil, err
	}
	s.parsers.parser, s.parsers.at = NewParser(areas), time.Now()
	return s.parsers.parser, nil
}

// tsQuery — выражение tsquery для слов: термины через И, формы термина через ИЛИ; prefix —
// последнее слово как начало слова (подсказки при вводе). Параметры добавляются в args.
func tsQuery(text string, prefix bool, args *[]any) string {
	ts := terms(expandAbbreviations(tokens(text)))
	parts := make([]string, 0, len(ts))
	for i, t := range ts {
		forms := make([]string, 0, len(t.Forms)+1)
		for _, f := range t.Forms {
			*args = append(*args, f)
			forms = append(forms, fmt.Sprintf("phraseto_tsquery('search.ru', $%d)", len(*args)))
		}
		last := i == len(ts)-1
		if prefix && last && len([]rune(t.Forms[0])) >= 2 && !strings.Contains(t.Forms[0], " ") {
			*args = append(*args, t.Forms[0])
			// Начало слова: лексема без стемминга с пометкой «:*».
			forms = append(forms, fmt.Sprintf(
				"to_tsquery('simple', quote_literal(core.normalize_text($%d)) || ':*')", len(*args)))
		}
		parts = append(parts, "("+strings.Join(forms, " || ")+")")
	}
	return strings.Join(parts, " && ")
}

// visible — условие видимости документа и допустимых видов.
func visible(req Request, args *[]any) string {
	*args = append(*args, req.Kinds, req.UserID, req.Roles)
	n := len(*args)
	return fmt.Sprintf(`d.kind = ANY($%d) AND ((d.owner_id IS NULL AND d.role_code IS NULL) OR d.owner_id = $%d
		OR d.role_code = ANY($%d))`, n-2, n-1, n)
}

// proximity — усиление близких к центру карты документов (до 0,3 в пределах нескольких км).
func proximity(c *Point, args *[]any) string {
	if c == nil {
		return "0"
	}
	*args = append(*args, c.Lon, c.Lat)
	n := len(*args)
	return fmt.Sprintf(`CASE WHEN d.point IS NULL THEN 0 ELSE
		0.3 * exp(-ST_Distance(d.point::geography, ST_SetSRID(ST_MakePoint($%d, $%d), 4326)::geography) / 5000) END`, n-1, n)
}

// documents ищет документы по словам: полнотекстовый поиск с ранжированием; если совпадений
// мало — по сходству написания.
func (s *Service) documents(ctx context.Context, req Request, text string) ([]Hit, bool, error) {
	if strings.TrimSpace(text) == "" {
		return nil, false, nil
	}
	args := []any{core(text)}
	q := tsQuery(text, false, &args)
	if q == "" {
		return nil, false, nil
	}
	vis := visible(req, &args)
	prox := proximity(req.Center, &args)
	args = append(args, houseNumber(text))
	house := strconv.Itoa(len(args))
	args = append(args, req.PerKind)
	// Усиления: точное и начальное совпадение заголовка, запрос в подзаголовке (номер акта,
	// район адреса), номер дома из запроса в адресе.
	// Адресов в ранжирование — не больше maxAddressMatches: запрос «ТАО» совпадает с каждым
	// адресом округа, а ранжирование десятков тысяч совпадений занимает секунды.
	hits, err := s.collect(ctx, `
		WITH q AS (SELECT `+q+` AS tq),
		m AS (
			(SELECT d.kind, d.ref, d.title, d.subtitle, d.point, d.weight, d.title_norm, d.subtitle_norm, d.tsv
			 FROM search.document d, q WHERE d.kind <> 'address' AND d.tsv @@ q.tq AND `+vis+`)
			UNION ALL
			(SELECT d.kind, d.ref, d.title, d.subtitle, d.point, d.weight, d.title_norm, d.subtitle_norm, d.tsv
			 FROM search.document d, q WHERE d.kind = 'address' AND d.tsv @@ q.tq AND `+vis+`
			 LIMIT `+strconv.Itoa(maxAddressMatches)+`)
		)
		SELECT kind, ref, title, subtitle, lon, lat, score FROM (
			SELECT d.kind, d.ref, d.title, d.subtitle, ST_X(d.point) AS lon, ST_Y(d.point) AS lat,
			       ts_rank_cd(d.tsv, q.tq, 1) * (0.5 + d.weight)
			         + CASE WHEN translate(d.title_norm, `+punct+`) = $1 THEN 1
			                WHEN regexp_replace(translate(d.title_norm, `+punct+`), `+generic+`) = $1 THEN 0.8
			                WHEN translate(d.title_norm, `+punct+`) LIKE $1 || '%' THEN 0.3 ELSE 0 END
			         + CASE WHEN length($1) >= 3 AND d.subtitle_norm LIKE '%' || $1 || '%' THEN 0.3 ELSE 0 END
			         + CASE WHEN $`+house+` <> '' AND d.kind = 'address'
			                 AND d.title_norm ~ ('(дом|владение|участок) ' || $`+house+` || '([^0-9а-я]|$)') THEN 0.4 ELSE 0 END
			         + `+prox+` AS score
			FROM m d, q
		) x
		ORDER BY row_number() OVER (PARTITION BY kind ORDER BY score DESC) <= $`+strconv.Itoa(len(args))+` DESC, score DESC
		LIMIT $`+strconv.Itoa(len(args))+` * 6`, args...)
	// Совпадения по словам есть — сходство написания не нужно: его оценки по другой шкале
	// вытеснили бы точный ответ (дом 24 ниже домов 30 и 33), а запрос по сходству медленный.
	if err != nil || len(hits) > 0 {
		return trimPerKind(hits, req.PerKind), false, err
	}
	fuzzy, err := s.fuzzy(ctx, req, text, req.PerKind*4)
	if err != nil {
		return nil, false, err
	}
	return trimPerKind(fuzzy, req.PerKind), len(fuzzy) > 0, nil
}

// fuzzyThreshold — порог сходства написания при поиске с опечатками.
const fuzzyThreshold = "0.45"

// fuzzy — документы, похожие по написанию на запрос (опечатки).
func (s *Service) fuzzy(ctx context.Context, req Request, text string, limit int) ([]Hit, error) {
	// Опечатки: сходство написания с заголовком. Адреса не участвуют: опечатку в названии улицы
	// находит документ улицы, а перебор 500 тыс. адресов по сходству медленный.
	args := []any{core(text)}
	vis := visible(req, &args)
	prox := proximity(req.Center, &args)
	args = append(args, limit)
	var fuzzy []Hit
	// Порог сходства ниже стандартного (0,6): две ошибки в слове дают около 0,5.
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL pg_trgm.word_similarity_threshold = `+fuzzyThreshold); err != nil {
			return err
		}
		var err error
		fuzzy, err = collectHits(ctx, tx, `
			SELECT d.kind, d.ref, d.title, d.subtitle, ST_X(d.point), ST_Y(d.point),
			       word_similarity($1, d.title_norm) * (0.5 + d.weight) + `+prox+` AS score
			FROM search.document d
			WHERE d.kind <> 'address' AND $1 <% d.title_norm AND `+vis+`
			ORDER BY score DESC LIMIT $`+strconv.Itoa(len(args)), args...)
		return err
	})
	return fuzzy, err
}

func (s *Service) collect(ctx context.Context, sql string, args ...any) ([]Hit, error) {
	return collectHits(ctx, s.pool, sql, args...)
}

// querier — пул или транзакция.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func collectHits(ctx context.Context, q querier, sql string, args ...any) ([]Hit, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Hit, error) {
		var h Hit
		var sub *string
		var lon, lat *float64
		if err := r.Scan(&h.Kind, &h.Ref, &h.Title, &sub, &lon, &lat, &h.Score); err != nil {
			return h, err
		}
		if sub != nil {
			h.Subtitle = *sub
		}
		if lon != nil && lat != nil {
			h.Point = &Point{Lon: *lon, Lat: *lat}
		}
		return h, nil
	})
}

// trimPerKind оставляет не больше n документов каждого вида, без повторов.
func trimPerKind(hits []Hit, n int) []Hit {
	seen := map[string]bool{}
	count := map[string]int{}
	out := hits[:0:0]
	for _, h := range hits {
		key := h.Kind + "|" + h.Ref
		if seen[key] || count[h.Kind] >= n {
			continue
		}
		seen[key] = true
		count[h.Kind]++
		out = append(out, h)
	}
	return out
}

// houseNumber — первый номер дома в запросе («10», «26а»), иначе пусто.
func houseNumber(text string) string {
	for _, w := range tokens(text) {
		if w != "" && w[0] >= '0' && w[0] <= '9' && !strings.ContainsAny(w, ".,") {
			return w
		}
	}
	return ""
}

// generic — аргументы regexp_replace: родовое слово в начале или конце заголовка («район
// Капотня», «улица Плющиха», «Нижегородский район») — запрос «Капотня» совпадает с ядром.
const generic = `'^(район|улица|проспект|переулок|шоссе|проезд|площадь|бульвар|набережная) | (район|улица|проспект|переулок|шоссе|проезд|площадь|бульвар|набережная)$', ''`

// punct — аргументы translate: знаки препинания заголовка, которых нет в нормализованном запросе.
const punct = `',.«»"()', ''`

// maxAddressMatches — адресов в ранжировании, не больше.
const maxAddressMatches = 3000

// core — запрос в нормализованном виде для сравнения с заголовками: как core.normalize_text
// (ё → е, нижний регистр), без знаков препинания.
func core(text string) string {
	return strings.Join(tokens(text), " ")
}

// Search выполняет поиск: разбор, документы, объекты по фильтрам; пишет запрос в журнал.
func (s *Service) Search(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()
	if req.PerKind <= 0 {
		req.PerKind = 5
	}
	ps, err := s.Parser(ctx)
	if err != nil {
		return nil, err
	}
	res := &Result{Parsed: ps.Parse(req.Q)}
	p := res.Parsed
	if p.Coordinates != nil {
		hits, err := s.Reverse(ctx, *p.Coordinates, req)
		if err != nil {
			return nil, err
		}
		res.Hits = hits
		res.Took = time.Since(start)
		s.log(ctx, req, "search", res, len(hits))
		return res, nil
	}
	if p.Facility() {
		f := facility.Filter{
			Q: p.Text, Types: p.Types, Okrugs: p.Okrugs, Districts: p.Districts, Rings: p.Rings, MinArea: p.MinArea,
			MaxArea: p.MaxArea, MinConfidence: p.MinConfidence, MaxMkadMin: p.MaxMkadMin,
			NearRestrictions: p.NearRestrictions, HasPermit: p.HasPermit,
		}
		if p.NearText != "" {
			near, _, err := s.documents(ctx, Request{
				UserID: req.UserID, Roles: req.Roles, PerKind: 1, Center: req.Center,
				Kinds: []string{"territory", "facility", "street", "address"},
			}, p.NearText)
			if err != nil {
				return nil, err
			}
			if best := bestPlaced(near); best != nil {
				res.Near = best
				f.Near = &facility.Circle{Lon: best.Point.Lon, Lat: best.Point.Lat, RadiusM: p.NearKm * 1000}
			}
		}
		if p.NearText == "" || res.Near != nil {
			page, err := s.facilities.List(ctx, f, "-area", "", 10)
			if err != nil {
				return nil, err
			}
			res.Facilities = &page
		}
	}
	// Документы — по всему запросу: «Склады у реки» может быть названием сохранённого вида.
	text := p.Text
	if p.Facility() {
		text = req.Q
	}
	res.Hits, res.Fuzzy, err = s.documents(ctx, req, text)
	if err != nil {
		return nil, err
	}
	res.Took = time.Since(start)
	n := len(res.Hits)
	if res.Facilities != nil {
		n += res.Facilities.Total
	}
	s.log(ctx, req, "search", res, n)
	return res, nil
}

// bestPlaced — лучший документ с точкой.
func bestPlaced(hits []Hit) *Hit {
	var best *Hit
	for i := range hits {
		if hits[i].Point != nil && (best == nil || hits[i].Score > best.Score) {
			best = &hits[i]
		}
	}
	return best
}

// Suggest — подсказки при вводе: начало последнего слова, лучшие по важности и сходству.
func (s *Service) Suggest(ctx context.Context, req Request, limit int) ([]Hit, error) {
	start := time.Now()
	text := strings.TrimSpace(req.Q)
	if len([]rune(text)) < 2 {
		return []Hit{}, nil
	}
	args := []any{core(text)}
	q := tsQuery(text, true, &args)
	if q == "" {
		return []Hit{}, nil
	}
	vis := visible(req, &args)
	prox := proximity(req.Center, &args)
	args = append(args, houseNumber(text))
	house := strconv.Itoa(len(args))
	args = append(args, limit)
	// Сначала отбираются кандидаты по индексу, затем сортируются: ранжирование всех совпадений
	// префикса («у…») было бы долгим.
	hits, err := s.collect(ctx, `
		WITH q AS (SELECT `+q+` AS tq),
		cand AS (
			SELECT d.kind, d.ref, d.title, d.subtitle, d.point, d.weight, d.title_norm
			FROM search.document d, q
			WHERE d.tsv @@ q.tq AND `+vis+`
			ORDER BY d.weight DESC
			LIMIT 400
		)
		SELECT d.kind, d.ref, d.title, d.subtitle, ST_X(d.point), ST_Y(d.point),
		       d.weight + similarity($1, d.title_norm)
		         + CASE WHEN translate(d.title_norm, `+punct+`) LIKE $1 || '%' THEN 0.5 ELSE 0 END
		         + CASE WHEN $`+house+` <> '' AND d.kind = 'address'
		                 AND d.title_norm ~ ('(дом|владение|участок) ' || $`+house+` || '([^0-9а-я]|$)') THEN 0.4 ELSE 0 END
		         + `+prox+` AS score
		FROM cand d
		ORDER BY score DESC LIMIT $`+strconv.Itoa(len(args))+` * 3`, args...)
	if err != nil {
		return nil, err
	}
	// Начало слова ничего не нашло — вероятно, опечатка: похожие по написанию.
	if len(hits) == 0 {
		if hits, err = s.fuzzy(ctx, req, text, limit); err != nil {
			return nil, err
		}
	}
	// Адреса не вытесняют улицы, территории и объекты.
	hits = trimPerKind(hits, max(limit/2, 1))
	if len(hits) > limit {
		hits = hits[:limit]
	}
	s.log(ctx, req, "suggest", &Result{Took: time.Since(start)}, len(hits))
	return hits, nil
}

// log пишет запрос в журнал; ошибка записи не мешает ответу.
func (s *Service) log(ctx context.Context, req Request, mode string, res *Result, n int) {
	if req.NoLog {
		return
	}
	chips, _ := json.Marshal(res.Parsed.Chips)
	var user *uuid.UUID
	if req.UserID != uuid.Nil {
		user = &req.UserID
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO search.query_log (user_id, mode, query, filters, results, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)`, user, mode, req.Q, chips, n, res.Took.Milliseconds())
}
