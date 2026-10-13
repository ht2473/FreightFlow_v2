// Package mosdata — загрузчики наборов портала открытых данных Москвы (apidata.mos.ru,
// CC BY 4.0): паспорт и схема набора, версия, постраничная выгрузка записей в снимок,
// перенос в домен SQL-преобразованием набора.
package mosdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"freightflow/internal/platform/outbound"
)

// Host — адрес API портала.
const Host = "apidata.mos.ru"

const base = "https://" + Host + "/v1"

// PageSize — записей на страницу (максимум API).
const PageSize = 1000

// Client — клиент API портала.
type Client struct {
	http *outbound.Client
	key  string
	base string
	// Pause — пауза между страницами, чтобы не нагружать портал.
	Pause time.Duration
	// retryPauses — паузы перед повторами страницы после ответа 5xx.
	retryPauses []time.Duration
}

// NewClient создаёт клиент с ключом API.
func NewClient(c *outbound.Client, key string) *Client {
	return &Client{http: c, key: key, base: base, Pause: 250 * time.Millisecond, retryPauses: pageRetries}
}

// Column — атрибут набора; у табличных атрибутов есть вложенные.
type Column struct {
	Name       string   `json:"Name"`
	Caption    string   `json:"Caption"`
	Type       string   `json:"Type"`
	Visible    bool     `json:"Visible"`
	SubColumns []Column `json:"SubColumns,omitempty"`
}

// Passport — паспорт набора.
type Passport struct {
	ID              int      `json:"Id"`
	Caption         string   `json:"Caption"`
	Description     string   `json:"Description"`
	Department      string   `json:"DepartmentCaption"`
	ContainsGeodata bool     `json:"ContainsGeodata"`
	IsArchive       bool     `json:"IsArchive"`
	VersionNumber   Text     `json:"VersionNumber"`
	ReleaseNumber   Text     `json:"ReleaseNumber"`
	LastUpdateDate  string   `json:"LastUpdateDate"`
	Columns         []Column `json:"Columns"`
}

// Version — версия и релиз набора: меняются при каждом обновлении данных.
type Version struct {
	VersionNumber Int `json:"VersionNumber"`
	ReleaseNumber Int `json:"ReleaseNumber"`
}

// Text — значение, которое API отдаёт то строкой, то числом.
type Text string

// UnmarshalJSON принимает "1.47" и 1.47.
func (t *Text) UnmarshalJSON(b []byte) error {
	*t = Text(strings.Trim(string(b), `"`))
	if *t == "null" {
		*t = ""
	}
	return nil
}

// Int — целое, которое API отдаёт то числом, то строкой.
type Int int64

// UnmarshalJSON принимает 12 и "12".
func (n *Int) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("integer %s: %w", b, err)
	}
	*n = Int(v)
	return nil
}

// String — «версия.релиз».
func (v Version) String() string { return fmt.Sprintf("%d.%d", v.VersionNumber, v.ReleaseNumber) }

// Row — запись набора; Cells — атрибуты в исходном виде.
type Row struct {
	GlobalID int64           `json:"global_id"`
	Number   int64           `json:"Number"`
	Cells    json.RawMessage `json:"Cells"`
}

func (c *Client) url(path string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_key", c.key)
	return c.base + path + "?" + q.Encode()
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, v any) error {
	resp, err := c.http.Get(ctx, c.url(path, q), outbound.Validators{})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// Passport возвращает паспорт и схему набора.
func (c *Client) Passport(ctx context.Context, id int) (Passport, error) {
	var p Passport
	err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id), nil, &p)
	return p, err
}

// Version возвращает текущую версию набора.
func (c *Client) Version(ctx context.Context, id int) (Version, error) {
	var v Version
	err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id)+"/version", nil, &v)
	return v, err
}

// Count возвращает число записей набора.
func (c *Client) Count(ctx context.Context, id int) (int64, error) {
	var n int64
	err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id)+"/count", nil, &n)
	return n, err
}

// Rows возвращает страницу записей начиная с skip.
func (c *Client) Rows(ctx context.Context, id int, skip, top int) ([]Row, error) {
	q := url.Values{"$skip": {strconv.Itoa(skip)}, "$top": {strconv.Itoa(top)}}
	var rows []Row
	err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id)+"/rows", q, &rows)
	return rows, err
}

// RowsFields возвращает страницу записей только с полями fields (POST с проекцией).
func (c *Client) RowsFields(ctx context.Context, id int, fields []string, skip, top int) ([]Row, error) {
	q := url.Values{"$skip": {strconv.Itoa(skip)}, "$top": {strconv.Itoa(top)}}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.PostJSON(ctx, c.url("/datasets/"+strconv.Itoa(id)+"/rows", q), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var rows []Row
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode rows %d: %w", id, err)
	}
	return rows, nil
}

// PageFunc получает записи очередной страницы и смещение следующей; вызывается для каждой
// страницы, в том числе пустой, — по нему выгрузку можно продолжить после обрыва.
type PageFunc func(recs []Record, next int) error

// EachRowPage выгружает total записей постранично с записи start (только поля fields, если
// заданы).
func (c *Client) EachRowPage(ctx context.Context, id int, fields []string, total int64, start int, fn PageFunc) error {
	return c.paginate(ctx, total, start, func(skip int) (int, error) {
		var rows []Row
		var err error
		if len(fields) > 0 {
			rows, err = c.RowsFields(ctx, id, fields, skip, PageSize)
		} else {
			rows, err = c.Rows(ctx, id, skip, PageSize)
		}
		if err != nil {
			return 0, fmt.Errorf("rows %d skip %d: %w", id, skip, err)
		}
		recs := make([]Record, len(rows))
		for i, r := range rows {
			recs[i] = Record{ID: r.GlobalID, Cells: r.Cells}
		}
		return len(rows), fn(recs, skip+PageSize)
	})
}

// feature — объект GeoJSON выдачи /features.
type feature struct {
	Geometry   json.RawMessage `json:"geometry"`
	Properties struct {
		Attributes json.RawMessage `json:"attributes"`
	} `json:"properties"`
}

// EachFeaturePage выгружает записи с геометрией постранично с записи start (total — число
// записей набора).
func (c *Client) EachFeaturePage(ctx context.Context, id int, total int64, start int, fn PageFunc) error {
	return c.paginate(ctx, total, start, func(skip int) (int, error) {
		var fc struct {
			Features []feature `json:"features"`
		}
		q := url.Values{"$skip": {strconv.Itoa(skip)}, "$top": {strconv.Itoa(PageSize)}}
		if err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id)+"/features", q, &fc); err != nil {
			return 0, fmt.Errorf("features %d skip %d: %w", id, skip, err)
		}
		recs := make([]Record, 0, len(fc.Features))
		for _, f := range fc.Features {
			var attrs struct {
				GlobalID int64 `json:"global_id"`
			}
			if err := json.Unmarshal(f.Properties.Attributes, &attrs); err != nil {
				return 0, fmt.Errorf("feature attributes: %w", err)
			}
			recs = append(recs, Record{ID: attrs.GlobalID, Cells: f.Properties.Attributes, Geometry: f.Geometry})
		}
		return len(fc.Features), fn(recs, skip+PageSize)
	})
}

// paginate запрашивает страницы с записи start до числа записей total. /features пропускает
// записи без геометрии, поэтому неполная страница не означает конец набора; без total (0) — до
// первой неполной страницы.
func (c *Client) paginate(ctx context.Context, total int64, start int, page func(skip int) (int, error)) error {
	if total > 0 && int64(start) >= total {
		return nil
	}
	for skip := start; ; skip += PageSize {
		if skip > start && c.Pause > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.Pause):
			}
		}
		n, err := c.retryPage(ctx, page, skip)
		if err != nil {
			return err
		}
		if total > 0 && int64(skip+PageSize) >= total || total <= 0 && n < PageSize {
			return nil
		}
	}
}

// pageRetries — паузы перед повторами страницы: портал временно отвечает 500 и 502 на
// отдельные страницы больших наборов (сверх повторов самого клиента).
var pageRetries = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute}

// retryPage запрашивает страницу, повторяя её при ответах 5xx и обрыве ответа. Ошибки
// обработчика страницы не повторяются: он вызывается только после успешного разбора ответа.
func (c *Client) retryPage(ctx context.Context, page func(skip int) (int, error), skip int) (int, error) {
	for attempt := 0; ; attempt++ {
		n, err := page(skip)
		if err == nil || attempt >= len(c.retryPauses) || !transient(ctx, err) {
			return n, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(c.retryPauses[attempt]):
		}
	}
}

// transient — сбой, после которого страницу стоит запросить снова: ответ 5xx, оборванное тело
// ответа (портал закрывает соединение посреди страницы), сетевая ошибка.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var ne net.Error
	return outbound.StatusCode(err) >= http.StatusInternalServerError || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &ne)
}

// FeaturesRaw возвращает страницу записей в GeoJSON (FeatureCollection) без разбора.
func (c *Client) FeaturesRaw(ctx context.Context, id int, skip, top int) (json.RawMessage, error) {
	q := url.Values{"$skip": {strconv.Itoa(skip)}, "$top": {strconv.Itoa(top)}}
	var raw json.RawMessage
	err := c.getJSON(ctx, "/datasets/"+strconv.Itoa(id)+"/features", q, &raw)
	return raw, err
}
