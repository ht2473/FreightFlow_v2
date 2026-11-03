package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"freightflow/internal/analytics"
	"freightflow/internal/ingest"
)

// HelpData — данные справки: каталог показателей и реестр источников.
type HelpData interface {
	Defs(ctx context.Context) ([]analytics.Def, error)
	ListSources(ctx context.Context) ([]ingest.SourceStatus, error)
}

// helpData — адаптер сервисов аналитики и загрузок.
type helpData struct {
	a *analytics.Service
	i *ingest.Service
}

func (d helpData) Defs(ctx context.Context) ([]analytics.Def, error) { return d.a.Defs(ctx) }

func (d helpData) ListSources(ctx context.Context) ([]ingest.SourceStatus, error) {
	return d.i.ListSources(ctx)
}

// NewHelpData — данные справки из сервисов модулей.
func NewHelpData(a *analytics.Service, i *ingest.Service) HelpData { return helpData{a: a, i: i} }

// helpPage — раздел справки в боковом меню.
type helpPage struct {
	Path  string
	Title string
}

var helpPages = []helpPage{
	{"/help/guide", "Руководство по разделам"},
	{"/help/methods", "Методики показателей"},
	{"/help/sources", "Источники и лицензии"},
	{"/help/glossary", "Словарь"},
	{"/help/about", "О системе"},
}

// helpAuth — справка доступна после входа, как и рабочее пространство.
func (h *handler) helpAuth(w http.ResponseWriter, r *http.Request) bool {
	if _, _, ok := h.Sessions.UserID(r.Context()); ok {
		return true
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
	return false
}

func (h *handler) helpIndex(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/help/guide", http.StatusSeeOther)
}

func (h *handler) helpGuide(w http.ResponseWriter, r *http.Request) {
	if !h.helpAuth(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, helpGuidePage(h.page(r, "Руководство"), "/help/guide"))
}

func (h *handler) helpGlossary(w http.ResponseWriter, r *http.Request) {
	if !h.helpAuth(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, helpGlossaryPage(h.page(r, "Словарь"), "/help/glossary"))
}

func (h *handler) helpAbout(w http.ResponseWriter, r *http.Request) {
	if !h.helpAuth(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, helpAboutPage(h.page(r, "О системе"), "/help/about", h.Version))
}

var levelTitles = map[string]string{
	"facility": "Объекты логистики", "district": "Районы", "okrug": "Округа", "segment": "Участки сети",
	"city": "Москва", "h3_8": "Ячейки H3 (≈ 0,7 км²)", "h3_9": "Ячейки H3 (≈ 0,1 км²)",
}

var levelOrder = []string{"city", "okrug", "district", "facility", "segment", "h3_8", "h3_9"}

// methodGroup — показатели одного основного уровня.
type methodGroup struct {
	Title   string
	Metrics []analytics.Def
}

func (h *handler) helpMethods(w http.ResponseWriter, r *http.Request) {
	if !h.helpAuth(w, r) {
		return
	}
	defs, err := h.Help.Defs(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	groups := map[string]*methodGroup{}
	for _, d := range defs {
		level := "city"
		if len(d.Levels) > 0 {
			level = d.Levels[0]
		}
		g := groups[level]
		if g == nil {
			g = &methodGroup{Title: levelTitles[level]}
			groups[level] = g
		}
		g.Metrics = append(g.Metrics, d)
	}
	var out []methodGroup
	for _, l := range levelOrder {
		if g := groups[l]; g != nil {
			slices.SortFunc(g.Metrics, func(a, b analytics.Def) int { return strings.Compare(a.Title, b.Title) })
			out = append(out, *g)
		}
	}
	h.render(w, r, http.StatusOK, helpMethodsPage(h.page(r, "Методики показателей"), "/help/methods", out))
}

func (h *handler) helpSources(w http.ResponseWriter, r *http.Request) {
	if !h.helpAuth(w, r) {
		return
	}
	list, err := h.Help.ListSources(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	slices.SortFunc(list, func(a, b ingest.SourceStatus) int { return strings.Compare(a.Title, b.Title) })
	h.render(w, r, http.StatusOK, helpSourcesPage(h.page(r, "Источники и лицензии"), "/help/sources", list))
}

func levelsText(levels []string) string {
	out := make([]string, 0, len(levels))
	for _, l := range levels {
		out = append(out, strings.ToLower(levelTitles[l]))
	}
	return strings.Join(out, ", ")
}

func dateText(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.In(moscowTZ).Format("02.01.2006")
}

func intervalText(d time.Duration) string {
	switch {
	case d <= 0:
		return "по запросу"
	case d < 48*time.Hour:
		return "ежедневно"
	case d < 14*24*time.Hour:
		return "еженедельно"
	default:
		return "ежемесячно"
	}
}

var moscowTZ = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}()

// mdBlock — блок методики: абзац или список; текст разбит на обычные куски и `код`.
type mdBlock struct {
	List  bool
	Items [][]mdSpan
}

type mdSpan struct {
	Text string
	Code bool
}

// markdown — небольшой разбор Markdown методик: абзацы, маркированные списки, `код`, **полужирный**
// без выделения (текст сохраняется).
func markdown(src string) []mdBlock {
	var out []mdBlock
	for _, para := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n\n") {
		lines := strings.Split(strings.TrimSpace(para), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		list := true
		for _, l := range lines {
			if !strings.HasPrefix(strings.TrimSpace(l), "- ") {
				list = false
			}
		}
		b := mdBlock{List: list}
		if list {
			for _, l := range lines {
				b.Items = append(b.Items, spans(strings.TrimPrefix(strings.TrimSpace(l), "- ")))
			}
		} else {
			b.Items = append(b.Items, spans(strings.Join(lines, " ")))
		}
		out = append(out, b)
	}
	return out
}

func spans(text string) []mdSpan {
	text = strings.ReplaceAll(text, "**", "")
	parts := strings.Split(text, "`")
	out := make([]mdSpan, 0, len(parts))
	for i, p := range parts {
		if p != "" {
			out = append(out, mdSpan{Text: p, Code: i%2 == 1})
		}
	}
	return out
}

// groupID — якорь группы показателей.
func groupID(title string) string {
	for i, l := range levelOrder {
		if levelTitles[l] == title {
			return "level-" + strconv.Itoa(i)
		}
	}
	return "level"
}
