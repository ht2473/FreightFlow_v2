package rosstat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"freightflow/etl"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/outbound"
)

// FreightCode — код источника показателей грузовых перевозок из ЕМИСС.
const FreightCode = "emiss_freight"

const emiss = "https://www.fedstat.ru"

// freightIndicators — показатели ЕМИСС одной методики (организации всех видов деятельности без
// субъектов малого предпринимательства): перевозка грузов и грузооборот автомобильного транспорта.
var freightIndicators = []string{"57791", "57804"}

// moscowOKATO — код ОКАТО Москвы в рядах SDMX.
const moscowOKATO = "45000000000"

// emissFilter — измерение показателя на странице ЕМИСС и его значения.
type emissFilter struct {
	ID     string
	Title  string
	Values map[string]string
}

// emissPage — разбор страницы показателя: название, единица, измерения, раскладка таблицы,
// токен формы выгрузки.
type emissPage struct {
	Title, Unit, Token string
	Filters            []emissFilter
	Left, Top          []string
}

var (
	reTitle   = regexp.MustCompile(`(?m)^\s*title: '((?:[^'\\]|\\.)*)',\s*\n\s*unit: '((?:[^'\\]|\\.)*)',`)
	reFilter  = regexp.MustCompile(`(?s)(\d+): \{\s*title: '((?:[^'\\]|\\.)*)',\s*all: (?:true|false),\s*values: \{(.*?)\},\s*indicator: (?:true|false)`)
	reValue   = regexp.MustCompile(`(?s)(\d+): \{\s*title: '((?:[^'\\]|\\.)*)',\s*order: \d+,`)
	reColumns = regexp.MustCompile(`(?s)(left|top)_columns: \[\s*([\d,\s]*)\]`)
	reToken   = regexp.MustCompile(`name="token" value="([^"]+)"`)
	reSeries  = regexp.MustCompile(`(?s)<generic:Series>(.*?)</generic:Series>`)
	reKey     = regexp.MustCompile(`<generic:Value concept="([^"]+)" value="([^"]*)"`)
	reObs     = regexp.MustCompile(`(?s)<generic:Time>([^<]*)</generic:Time>\s*<generic:ObsValue value="([^"]*)"`)
)

// unescapeJS раскрывает \uXXXX и \' в строке из JavaScript страницы.
func unescapeJS(s string) string {
	var out string
	if err := json.Unmarshal([]byte(`"`+strings.ReplaceAll(s, `\'`, `'`)+`"`), &out); err != nil {
		return s
	}
	return out
}

func parseEmissPage(page string) (emissPage, error) {
	var p emissPage
	if m := reTitle.FindStringSubmatch(page); m != nil {
		p.Title, p.Unit = unescapeJS(m[1]), unescapeJS(m[2])
	}
	if m := reToken.FindStringSubmatch(page); m != nil {
		p.Token = m[1]
	}
	for _, m := range reFilter.FindAllStringSubmatch(page, -1) {
		f := emissFilter{ID: m[1], Title: unescapeJS(m[2]), Values: map[string]string{}}
		for _, v := range reValue.FindAllStringSubmatch(m[3], -1) {
			f.Values[v[1]] = unescapeJS(v[2])
		}
		p.Filters = append(p.Filters, f)
	}
	for _, m := range reColumns.FindAllStringSubmatch(page, -1) {
		var ids []string
		for _, id := range strings.Split(m[2], ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if m[1] == "left" {
			p.Left = ids
		} else {
			p.Top = ids
		}
	}
	if len(p.Filters) == 0 || p.Token == "" || len(p.Left)+len(p.Top) == 0 {
		return p, fmt.Errorf("emiss page: %d filters, token %v", len(p.Filters), p.Token != "")
	}
	return p, nil
}

// query — тело выгрузки: все значения измерений, территория — только Москва.
func (p emissPage) query(id string) (string, error) {
	v := url.Values{"id": {id}, "title": {p.Title}, "struts.token.name": {"token"}, "token": {p.Token}}
	v["lineObjectIds"] = p.Left
	v["columnObjectIds"] = p.Top
	moscow := false
	for _, f := range p.Filters {
		for vid, title := range f.Values {
			if strings.Contains(f.Title, "ОКАТО") {
				if !strings.Contains(title, "Москва") || strings.Contains(title, "область") {
					continue
				}
				moscow = true
			}
			v.Add("selectedFilterIds", f.ID+"_"+vid)
		}
	}
	if !moscow {
		return "", fmt.Errorf("emiss indicator %s: Moscow is not among territories", id)
	}
	return v.Encode(), nil
}

// emissObs — значение показателя ЕМИСС за год.
type emissObs struct {
	Indicator string  `json:"indicator"`
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Year      int     `json:"year"`
	Value     float64 `json:"value"`
}

// parseSDMX выбирает годовые значения Москвы из ответа SDMX.
func parseSDMX(doc, indicator, name, unit string) []emissObs {
	var out []emissObs
	for _, s := range reSeries.FindAllStringSubmatch(doc, -1) {
		keys := map[string]string{}
		for _, k := range reKey.FindAllStringSubmatch(s[1], -1) {
			keys[k[1]] = k[2]
		}
		if keys["s_OKATO"] != moscowOKATO {
			continue
		}
		for _, o := range reObs.FindAllStringSubmatch(s[1], -1) {
			y, err1 := strconv.Atoi(strings.TrimSpace(o[1]))
			v, err2 := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(o[2], " ", ""), ",", "."), 64)
			if err1 != nil || err2 != nil {
				continue
			}
			u := unit
			if keys["EI"] != "" {
				u = keys["EI"]
			}
			out = append(out, emissObs{Indicator: indicator, Name: name, Unit: u, Year: y, Value: v})
		}
	}
	return out
}

// Freight — загрузчик показателей грузовых перевозок по Москве из ЕМИСС.
type Freight struct{}

// NewFreight создаёт загрузчик.
func NewFreight() *Freight { return &Freight{} }

// Descriptor описывает источник.
func (f *Freight) Descriptor() ingest.Descriptor {
	return ingest.Descriptor{
		Code:        FreightCode,
		Title:       "Перевозки грузов и грузооборот автомобильного транспорта по Москве",
		Publisher:   "ЕМИСС (fedstat.ru), показатели " + strings.Join(freightIndicators, ", "),
		URL:         emiss + "/indicator/" + freightIndicators[0],
		License:     "Официальная статистика, общедоступная информация (149-ФЗ), с указанием источника",
		Attribution: "Росстат, ЕМИСС",
		Domains:     []string{"statistics"},
		Interval:    30 * 24 * time.Hour,
		Hosts:       []string{"www.fedstat.ru"},
		RuNetwork:   true,
		Cookies:     true,
		Timeout:     20 * time.Minute,
		// Выгрузка формируется сервером на лету.
		RequestTimeout: 3 * time.Minute,
	}
}

// Fetch выгружает каждый показатель: страница (сессия и токен формы), затем SDMX по Москве.
func (f *Freight) Fetch(ctx context.Context, env ingest.FetchEnv) (ingest.Fetched, error) {
	var all []emissObs
	for _, id := range freightIndicators {
		page, err := get(ctx, env.HTTP, emiss+"/indicator/"+id)
		if err != nil {
			return ingest.Fetched{}, fmt.Errorf("страница показателя %s: %w", id, err)
		}
		p, err := parseEmissPage(page)
		if err != nil {
			return ingest.Fetched{}, err
		}
		body, err := p.query(id)
		if err != nil {
			return ingest.Fetched{}, err
		}
		resp, err := env.HTTP.Post(ctx, emiss+"/indicator/downloadData.do?format=sdmx", formType, []byte(body))
		if err != nil {
			return ingest.Fetched{}, fmt.Errorf("выгрузка показателя %s: %w", id, err)
		}
		doc, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		resp.Body.Close()
		if err != nil {
			return ingest.Fetched{}, err
		}
		obs := parseSDMX(string(doc), id, p.Title, p.Unit)
		if len(obs) == 0 {
			return ingest.Fetched{}, fmt.Errorf("показатель %s: нет значений по Москве в выгрузке", id)
		}
		env.Log.Info(ctx, fmt.Sprintf("показатель %s: %d значений, %s", id, len(obs), p.Unit))
		all = append(all, obs...)
	}
	b, err := json.Marshal(all)
	if err != nil {
		return ingest.Fetched{}, err
	}
	obj, err := env.Store(ctx, "json", bytes.NewReader(b))
	if err != nil {
		return ingest.Fetched{}, err
	}
	n := int64(len(all))
	return ingest.Fetched{Object: obj, Format: "json", RecordCount: &n}, nil
}

func get(ctx context.Context, c *outbound.Client, u string) (string, error) {
	resp, err := c.Get(ctx, u, outbound.Validators{})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// Load переносит значения в statistics (etl/rosstat/emiss.sql).
func (f *Freight) Load(ctx context.Context, env ingest.LoadEnv) (ingest.LoadStats, error) {
	file, err := env.Blobs.Open(env.Snapshot.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var obs []emissObs
	if err := json.NewDecoder(file).Decode(&obs); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	script, err := etl.FS.ReadFile("rosstat/emiss.sql")
	if err != nil {
		return nil, err
	}
	var stats ingest.LoadStats
	err = db.InTx(ctx, env.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE emiss_stage (indicator text, name text, unit text, year int, value numeric) ON COMMIT DROP`); err != nil {
			return err
		}
		rows := make([][]any, len(obs))
		for i, o := range obs {
			rows[i] = []any{o.Indicator, o.Name, o.Unit, o.Year, o.Value}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"emiss_stage"}, []string{"indicator", "name", "unit", "year", "value"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("stage emiss: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('ff.source', $1, true), set_config('ff.snapshot', $2, true)`,
			FreightCode, env.Snapshot.ID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script)); err != nil {
			return fmt.Errorf("transform emiss: %w", err)
		}
		stats, err = loadStats(ctx, tx)
		return err
	})
	return stats, err
}
