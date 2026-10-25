package search

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Area — округ или район для разбора названий в запросе.
type Area struct {
	ID        uuid.UUID
	Kind      string // okrug, district
	Name      string
	ShortName string
}

// Point — точка WGS 84.
type Point struct{ Lon, Lat float64 }

// Chip — разобранный фильтр для показа: поле и подпись.
type Chip struct {
	Field string
	Label string
}

// Parsed — запрос, разобранный в фильтры реестра; Text — остаток для поиска по словам.
type Parsed struct {
	Text             string
	Types            []string
	Okrugs           []uuid.UUID
	Districts        []uuid.UUID
	Rings            []string
	MinArea, MaxArea *float64
	MaxMkadMin       *float64
	MinConfidence    *float64
	NearRestrictions *bool
	HasPermit        *bool
	// NearText — место из «рядом с …», «в N км от …»: точку находит поиск, радиус — NearKm.
	NearText string
	NearKm   float64
	// Coordinates — запрос целиком оказался координатами.
	Coordinates *Point
	Chips       []Chip
}

// Facility — запрос относится к объектам реестра (есть хотя бы один фильтр объектов).
func (p Parsed) Facility() bool {
	return len(p.Types) > 0 || len(p.Okrugs) > 0 || len(p.Districts) > 0 || len(p.Rings) > 0 || p.MinArea != nil ||
		p.MaxArea != nil || p.MaxMkadMin != nil || p.MinConfidence != nil || p.NearRestrictions != nil ||
		p.HasPermit != nil || p.NearText != ""
}

// typeStems — начала слов, указывающие на тип объекта (после замены сокращений).
var typeStems = []struct {
	stems []string
	code  string
	label string
}{
	{[]string{"холодильн", "хладокомбинат"}, "cold_storage", "Холодильный склад"},
	{[]string{"склад"}, "warehouse", "Склад"},
	{[]string{"логистическ", "тлц", "рц", "логопарк", "распределительн"}, "logistics_center", "Логистический центр"},
	{[]string{"терминал"}, "terminal", "Грузовой терминал"},
	{[]string{"сортировочн"}, "sorting_center", "Сортировочный центр"},
	{[]string{"оптов", "плодоовощн"}, "wholesale", "Оптовая торговля"},
	{[]string{"речн", "причал"}, "port", "Речной порт"},
}

// typeWords — типы, узнаваемые только по точной форме слова (у начала «порт» — «портал»).
var typeWords = map[string]string{"порт": "port", "порты": "port", "портов": "port", "порту": "port"}

// typeTail — слова, которые после указания типа ничего не добавляют («логистический центр»).
var typeTail = map[string]bool{
	"центр": true, "центры": true, "центров": true, "комплекс": true, "комплексы": true, "комплексов": true,
	"парк": true, "парки": true, "база": true, "базы": true, "баз": true, "торговля": true,
}

var (
	coordsRe = regexp.MustCompile(`^\s*(-?\d{1,3}(?:[.,]\d+)?)\s*[,; ]\s*(-?\d{1,3}(?:[.,]\d+)?)\s*$`)
	numberRe = regexp.MustCompile(`^\d+(?:[.,]\d+)?$`)
)

// comparators — слова сравнения: «больше» → нижняя граница, «меньше» → верхняя.
var comparators = map[string]string{
	"больше": "min", "более": "min", "свыше": "min", "от": "min", ">": "min", ">=": "min", "не": "",
	"меньше": "max", "менее": "max", "до": "max", "<": "max", "<=": "max",
}

// areaUnits — единицы площади и множитель к квадратным метрам.
var areaUnits = map[string]float64{
	"м²": 1, "м2": 1, "кв.м": 1, "кв": 1, "метров": 1, "квадратных": 1, "тыс": 1000, "тысяч": 1000, "га": 10000,
	"гектар": 10000, "гектаров": 10000,
}

// ringWords — отношения к кольцам: пояса реестра.
var ringRelations = []struct {
	words []string
	rings []string
	label string
}{
	{[]string{"внутри садового кольца", "в пределах садового кольца", "внутри ск"}, []string{"sk"}, "Внутри Садового кольца"},
	{[]string{"внутри ттк", "в пределах ттк"}, []string{"sk", "ttk"}, "Внутри ТТК"},
	{[]string{"внутри мкад", "в пределах мкад", "в мкад"}, []string{"sk", "ttk", "mkad"}, "Внутри МКАД"},
	{[]string{"за мкад", "вне мкад", "за пределами мкад"}, []string{"ckad", "outside"}, "За МКАД"},
}

// nearMkad — «у МКАД»: до МКАД не дольше 10 минут грузовиком.
var nearMkad = []string{"у мкад", "рядом с мкад", "возле мкад", "около мкад", "вблизи мкад", "близко к мкад"}

// signs — признаки объектов.
var signs = []struct {
	words []string
	apply func(p *Parsed)
}{
	{[]string{"без ограничений", "без ограничений рядом", "без ограничений по высоте", "без ограничений проезда"}, func(p *Parsed) {
		p.NearRestrictions = ptr(false)
		p.Chips = append(p.Chips, Chip{"restr", "Без ограничений рядом"})
	}},
	{[]string{"с ограничениями", "с ограничениями рядом"}, func(p *Parsed) {
		p.NearRestrictions = ptr(true)
		p.Chips = append(p.Chips, Chip{"restr", "Ограничения рядом"})
	}},
	{[]string{"с разрешением на ввод", "новые", "новостройки", "введенные"}, func(p *Parsed) {
		p.HasPermit = ptr(true)
		p.Chips = append(p.Chips, Chip{"permit", "Есть разрешение на ввод"})
	}},
	{[]string{"достоверные", "высокой достоверности", "с высокой достоверностью"}, func(p *Parsed) {
		p.MinConfidence = ptr(0.7)
		p.Chips = append(p.Chips, Chip{"conf", "Достоверность высокая"})
	}},
}

func ptr[T any](v T) *T { return &v }

// Parser разбирает запросы; знает названия округов и районов.
type Parser struct {
	areas []areaKey
}

type areaKey struct {
	words []string
	area  Area
}

// NewParser — разборщик со словарём территорий: полные и краткие названия, у районов — без
// слова «район», у округов — без «административный».
func NewParser(areas []Area) *Parser {
	p := &Parser{}
	for _, a := range areas {
		names := []string{a.Name}
		if a.ShortName != "" {
			names = append(names, a.ShortName)
		}
		n := strings.ToLower(a.Name)
		switch a.Kind {
		case "district":
			core := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(n, "район "), " район"))
			names = append(names, core, "район "+core)
		case "okrug":
			names = append(names, strings.Replace(n, " административный округ", " округ", 1))
		}
		for _, name := range names {
			if w := tokens(name); len(w) > 0 {
				p.areas = append(p.areas, areaKey{words: w, area: a})
			}
		}
	}
	return p
}

// wordMatch — слово запроса совпадает со словом названия с точностью до окончания: «хамовниках»
// и «хамовники», «нижегородском» и «нижегородский», «роще» и «роща». Слова короче четырёх букв
// (сокращения) — только целиком.
func wordMatch(q, name string) bool {
	if q == name {
		return true
	}
	r := []rune(name)
	cut := 2
	switch {
	case len(r) < 4:
		return false
	case len(r) < 6:
		cut = 1
	}
	stem := string(r[:len(r)-cut])
	return strings.HasPrefix(q, stem) && len([]rune(q))-len(r) <= 3
}

// Parse разбирает запрос. Названия территорий становятся фильтрами, только если в запросе есть
// тип объекта или другой фильтр объектов: одно «Хамовники» — поиск самой территории.
func (ps *Parser) Parse(q string) Parsed {
	var p Parsed
	if m := coordsRe.FindStringSubmatch(q); m != nil {
		a, _ := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		b, _ := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", "."), 64)
		if pt, ok := coordinates(a, b); ok {
			p.Coordinates = &pt
			return p
		}
	}
	words := expandAbbreviations(tokens(q))
	used := make([]bool, len(words))
	phrase := func(i, n int) string { return strings.Join(words[i:i+n], " ") }
	// mark отмечает слова фразы, если она стоит в позиции i.
	findPhrase := func(list []string) (int, int) {
		for i := range words {
			for _, f := range list {
				n := len(strings.Fields(f))
				if i+n <= len(words) && !anyUsed(used[i:i+n]) && phrase(i, n) == f {
					return i, n
				}
			}
		}
		return -1, 0
	}
	mark := func(i, n int) {
		for k := i; k < i+n; k++ {
			used[k] = true
		}
	}

	// Отношения к кольцам и признаки — фразами.
	for _, r := range ringRelations {
		if i, n := findPhrase(r.words); i >= 0 {
			mark(i, n)
			p.Rings = r.rings
			p.Chips = append(p.Chips, Chip{"ring", r.label})
			break
		}
	}
	if i, n := findPhrase(nearMkad); i >= 0 {
		mark(i, n)
		p.MaxMkadMin = ptr(10.0)
		p.Chips = append(p.Chips, Chip{"mkad", "До МКАД ≤ 10 мин"})
	}
	for _, s := range signs {
		if i, n := findPhrase(s.words); i >= 0 {
			mark(i, n)
			s.apply(&p)
		}
	}

	// Числа с единицами: площадь и минуты до МКАД.
	ps.numbers(words, used, &p)

	// «рядом с …», «в N км от …» — место до конца запроса.
	ps.near(words, used, &p)

	// Типы объектов.
	for i, w := range words {
		if used[i] {
			continue
		}
		for _, t := range typeStems {
			if hasStem(w, t.stems) || typeWords[w] == t.code {
				used[i] = true
				if !contains(p.Types, t.code) {
					p.Types = append(p.Types, t.code)
					p.Chips = append(p.Chips, Chip{"type", t.label})
				}
				// «логистический центр», «холодильные склады» — тип одним словом.
				if i+1 < len(words) && (typeTail[words[i+1]] || t.code == "cold_storage" && strings.HasPrefix(words[i+1], "склад")) {
					used[i+1] = true
				}
				break
			}
		}
	}

	// Территории — самые длинные совпадения.
	type hit struct {
		i, n int
		a    Area
	}
	var hits []hit
	for i := 0; i < len(words); i++ {
		if used[i] {
			continue
		}
		best := hit{n: 0}
		for _, k := range ps.areas {
			n := len(k.words)
			if n <= best.n || i+n > len(words) || anyUsed(used[i:i+n]) {
				continue
			}
			ok := true
			for j, w := range k.words {
				if !wordMatch(words[i+j], w) {
					ok = false
					break
				}
			}
			if ok {
				best = hit{i: i, n: n, a: k.area}
			}
		}
		if best.n > 0 {
			hits = append(hits, best)
			i += best.n - 1
		}
	}
	if len(hits) > 0 && p.Facility() {
		for _, h := range hits {
			mark(h.i, h.n)
			if h.a.Kind == "okrug" {
				if !containsID(p.Okrugs, h.a.ID) {
					p.Okrugs = append(p.Okrugs, h.a.ID)
					p.Chips = append(p.Chips, Chip{"okrug", label(h.a)})
				}
			} else if !containsID(p.Districts, h.a.ID) {
				p.Districts = append(p.Districts, h.a.ID)
				p.Chips = append(p.Chips, Chip{"district", label(h.a)})
			}
		}
	}

	var rest []string
	for i, w := range words {
		if !used[i] && !fillers[w] {
			rest = append(rest, w)
		}
	}
	p.Text = strings.Join(rest, " ")
	return p
}

// fillers — служебные слова, которые без фильтров ничего не значат.
var fillers = map[string]bool{
	"в": true, "на": true, "и": true, "с": true, "со": true, "площадью": true,
	"площадь": true, "площади": true, "застройки": true, "округе": false, "все": true, "найти": true, "покажи": true,
}

func label(a Area) string {
	if a.Kind == "okrug" && a.ShortName != "" {
		return a.ShortName
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(a.Name, "район "), " район"))
}

// numbers находит «больше 10 000 м²», «до 2 га», «до 30 мин до МКАД».
func (ps *Parser) numbers(words []string, used []bool, p *Parsed) {
	for i := 0; i < len(words); i++ {
		if used[i] || !numberRe.MatchString(words[i]) {
			continue
		}
		// Число с разрядами через пробел: «10 000».
		end := i + 1
		num := words[i]
		for end < len(words) && len(words[end]) == 3 && numberRe.MatchString(words[end]) && !strings.ContainsAny(num, ".,") {
			num += words[end]
			end++
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", "."), 64)
		if err != nil {
			continue
		}
		cmp, start := "min", i
		if i > 0 && !used[i-1] {
			if c, ok := comparators[words[i-1]]; ok && c != "" {
				cmp, start = c, i-1
				if i > 1 && words[i-2] == "не" {
					// «не более» — верхняя, «не менее» — нижняя граница.
					start = i - 2
					if c == "min" {
						cmp = "max"
					} else {
						cmp = "min"
					}
				}
			}
		}
		// Единица: площадь или минуты.
		unit, mult := "", 1.0
		stop := end
		for stop < len(words) && stop < end+2 {
			if m, ok := areaUnits[words[stop]]; ok {
				unit, mult = "area", mult*m
				stop++
				continue
			}
			break
		}
		if unit == "" && end < len(words) && (words[end] == "мин" || words[end] == "минут" || words[end] == "минуты") {
			unit, stop = "minutes", end+1
			// «до МКАД» после минут необязательно.
			if stop+1 < len(words) && (words[stop] == "до" || words[stop] == "от") && words[stop+1] == "мкад" {
				stop += 2
			}
		}
		switch unit {
		case "area":
			v *= mult
			if cmp == "max" {
				p.MaxArea = ptr(v)
				p.Chips = append(p.Chips, Chip{"area", fmt.Sprintf("Площадь ≤ %s м²", group(v))})
			} else {
				p.MinArea = ptr(v)
				p.Chips = append(p.Chips, Chip{"area", fmt.Sprintf("Площадь ≥ %s м²", group(v))})
			}
		case "minutes":
			p.MaxMkadMin = ptr(v)
			p.Chips = append(p.Chips, Chip{"mkad", fmt.Sprintf("До МКАД ≤ %s мин", group(v))})
		default:
			continue
		}
		for k := start; k < stop; k++ {
			used[k] = true
		}
		i = stop - 1
	}
}

// near находит «рядом с X», «около X», «в N км от X»: X — остаток запроса после отношения.
func (ps *Parser) near(words []string, used []bool, p *Parsed) {
	for i := 0; i < len(words); i++ {
		if used[i] {
			continue
		}
		start, from, km := -1, 0, 2.0
		switch {
		case i+1 < len(words) && (words[i] == "рядом" || words[i] == "возле" || words[i] == "вблизи") &&
			(words[i+1] == "с" || words[i+1] == "со"):
			start, from = i, i+2
		case words[i] == "около" || words[i] == "возле" || words[i] == "вблизи":
			start, from = i, i+1
		case words[i] == "в" && i+3 < len(words) && numberRe.MatchString(words[i+1]) && words[i+2] == "км" && words[i+3] == "от":
			v, err := strconv.ParseFloat(strings.ReplaceAll(words[i+1], ",", "."), 64)
			if err != nil || v <= 0 || v > 50 {
				continue
			}
			start, from, km = i, i+4, v
		}
		if start < 0 || from >= len(words) {
			continue
		}
		var place []string
		for k := from; k < len(words) && !used[k]; k++ {
			place = append(place, words[k])
		}
		if len(place) == 0 {
			continue
		}
		for k := start; k < from+len(place); k++ {
			used[k] = true
		}
		p.NearText, p.NearKm = strings.Join(place, " "), km
		p.Chips = append(p.Chips, Chip{"near", fmt.Sprintf("В %s км от «%s»", group(km), p.NearText)})
		return
	}
}

// coordinates принимает «широта, долгота» и «долгота, широта» в окрестностях Москвы.
func coordinates(a, b float64) (Point, bool) {
	inLat := func(v float64) bool { return v >= 54 && v <= 57.5 }
	inLon := func(v float64) bool { return v >= 35 && v <= 40.5 }
	switch {
	case inLat(a) && inLon(b):
		return Point{Lon: b, Lat: a}, true
	case inLon(a) && inLat(b):
		return Point{Lon: a, Lat: b}, true
	}
	return Point{}, false
}

// group — число с разрядами: 10000 → «10 000», 1.5 → «1,5».
func group(v float64) string {
	if v != float64(int64(v)) {
		return strings.ReplaceAll(strconv.FormatFloat(v, 'f', -1, 64), ".", ",")
	}
	s := strconv.FormatInt(int64(v), 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasStem(w string, stems []string) bool {
	for _, s := range stems {
		if strings.HasPrefix(w, s) {
			return true
		}
	}
	return false
}

func anyUsed(b []bool) bool {
	for _, v := range b {
		if v {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsID(list []uuid.UUID, v uuid.UUID) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
