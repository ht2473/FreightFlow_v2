package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/xuri/excelize/v2"

	"freightflow/internal/identity"
	"freightflow/internal/notify"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/ids"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/mail"
)

// Форматы отчётов.
const (
	FormatPDF = "pdf"
)

// Param — параметр шаблона отчёта.
type Param struct {
	Name     string
	Title    string
	Kind     string // uuid, int, profile
	Required bool
	Min, Max int
}

// Template — шаблон отчёта: страница печати /print/<код>, параметры и право.
type Template struct {
	Code        string
	Title       string
	Description string
	Params      []Param
	Permission  identity.Permission
}

// Templates — шаблоны отчётов в порядке показа.
var Templates = []Template{
	{
		"facility", "Паспорт объекта", "Карточка объекта логистики: характеристики, доступность, ограничения и работы рядом, карта",
		[]Param{{Name: "facilityId", Title: "Объект", Kind: "uuid", Required: true}},
		identity.PermMapView,
	},
	{
		"territory", "Сводка по округу или району", "Показатели территории с местом в рейтинге, объекты, работы, карта",
		[]Param{{Name: "territoryId", Title: "Территория", Kind: "uuid", Required: true}},
		identity.PermAnalyticsView,
	},
	{
		"passability", "Проходимость и ограничения", "Ограничения для профиля ТС на территории: запреты, габариты, масса, зоны",
		[]Param{{Name: "territoryId", Title: "Территория", Kind: "uuid", Required: true}, {Name: "profile", Title: "Профиль ТС", Kind: "profile", Required: true}},
		identity.PermRoutingUse,
	},
	{
		"roadworks", "Дорожные работы и влияние", "Идущие и начинающиеся работы, перекрытия, объекты рядом, динамика",
		[]Param{{Name: "territoryId", Title: "Территория", Kind: "uuid"}, {Name: "days", Title: "Период, дней", Kind: "int", Min: 7, Max: 90}},
		identity.PermMapView,
	},
	{
		"scenario", "Результаты сценария", "Итог сценария «что если»: изменения доступности, районы, объекты",
		[]Param{{Name: "scenarioId", Title: "Сценарий", Kind: "uuid", Required: true}},
		identity.PermScenariosManage,
	},
	{
		"site", "Оценка локации", "Кандидаты TOPSIS, веса критериев, чувствительность, карта пригодности",
		[]Param{{Name: "siteRunId", Title: "Сохранённая оценка", Kind: "uuid", Required: true}},
		identity.PermScenariosManage,
	},
	{
		"quality", "Качество данных", "Источники: свежесть, последние загрузки, проверки качества, полнота реестра",
		nil, identity.PermReportsView,
	},
}

// TemplateByCode — шаблон по коду.
func TemplateByCode(code string) (Template, bool) {
	i := slices.IndexFunc(Templates, func(t Template) bool { return t.Code == code })
	if i < 0 {
		return Template{}, false
	}
	return Templates[i], true
}

// ErrInvalid — неверные параметры отчёта; текст — для пользователя.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return "report: " + e.Reason }

func invalid(format string, a ...any) error { return &ErrInvalid{Reason: fmt.Sprintf(format, a...)} }

// ValidateParams проверяет параметры по шаблону и оставляет только его параметры.
func (t Template) ValidateParams(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range t.Params {
		v := strings.TrimSpace(in[p.Name])
		if v == "" {
			if p.Required {
				return nil, invalid("не задан параметр «%s»", p.Title)
			}
			continue
		}
		switch p.Kind {
		case "uuid":
			if _, err := uuid.Parse(v); err != nil {
				return nil, invalid("параметр «%s» — не идентификатор", p.Title)
			}
		case "int":
			n, err := strconv.Atoi(v)
			if err != nil || n < p.Min || n > p.Max {
				return nil, invalid("параметр «%s» — от %d до %d", p.Title, p.Min, p.Max)
			}
		case "profile":
			if len(v) > 50 {
				return nil, invalid("неверный профиль ТС")
			}
		}
		out[p.Name] = v
	}
	return out, nil
}

// Report — отчёт.
type Report struct {
	ID         uuid.UUID
	Template   string
	Format     string
	Params     map[string]string
	Title      string
	Status     string
	FileSize   *int64
	Error      *string
	ScheduleID *uuid.UUID
	CreatedAt  time.Time
	FinishedAt *time.Time
	ExpiresAt  time.Time
}

// Engine — зависимости формирования отчётов: рендеринг страниц, токены печати, почта рассылки.
type Engine struct {
	Renderer Renderer
	Tokens   func(userID uuid.UUID, ttl time.Duration) string
	Mailer   notify.Mailer
	BaseURL  string
	Log      *slog.Logger
}

// SetEngine подключает формирование отчётов (в процессе без службы pdf не вызывается).
func (s *Service) SetEngine(e Engine) { s.engine = &e }

const reportColumns = `id, template, format, params, title, status, file_size, error, schedule_id, created_at, finished_at, expires_at`

func scanReport(r pgx.Row) (*Report, error) {
	var rep Report
	var params []byte
	err := r.Scan(&rep.ID, &rep.Template, &rep.Format, &params, &rep.Title, &rep.Status, &rep.FileSize, &rep.Error,
		&rep.ScheduleID, &rep.CreatedAt, &rep.FinishedAt, &rep.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rep, json.Unmarshal(params, &rep.Params)
}

// CreateReport ставит отчёт в очередь. Право на шаблон проверяет вызывающий.
func (s *Service) CreateReport(ctx context.Context, userID uuid.UUID, template, format string, params map[string]string) (*Report, error) {
	var out *Report
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = s.createReport(ctx, tx, userID, template, format, params, nil)
		return err
	})
	return out, err
}

func (s *Service) createReport(ctx context.Context, tx pgx.Tx, userID uuid.UUID, template, format string,
	params map[string]string, schedule *uuid.UUID,
) (*Report, error) {
	t, ok := TemplateByCode(template)
	if !ok {
		return nil, invalid("неизвестный шаблон отчёта %q", template)
	}
	if format != FormatPDF && format != FormatXLSX {
		return nil, invalid("формат отчёта — pdf или xlsx")
	}
	clean, err := t.ValidateParams(params)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	rep, err := scanReport(tx.QueryRow(ctx, `INSERT INTO report.report (id, user_id, template, format, params, title, schedule_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+reportColumns, ids.New(), userID, template, format, raw, t.Title, schedule))
	if err != nil {
		return nil, err
	}
	if s.jobs != nil {
		if _, err := s.jobs.InsertTx(ctx, tx, RenderArgs{ID: rep.ID}, &river.InsertOpts{Queue: jobs.QueueReports, MaxAttempts: 2}); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// Reports — отчёты пользователя, новые сверху.
func (s *Service) Reports(ctx context.Context, userID uuid.UUID, limit int) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+reportColumns+` FROM report.report
		WHERE user_id = $1 AND expires_at > now() ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Report, error) {
		rep, err := scanReport(r)
		if err != nil {
			return Report{}, err
		}
		return *rep, nil
	})
}

// Report — отчёт пользователя.
func (s *Service) Report(ctx context.Context, userID, id uuid.UUID) (*Report, error) {
	return scanReport(s.pool.QueryRow(ctx, `SELECT `+reportColumns+` FROM report.report WHERE id = $1 AND user_id = $2`, id, userID))
}

// DeleteReport удаляет отчёт (файл удалит очистка, если на него больше никто не ссылается).
func (s *Service) DeleteReport(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `UPDATE report.report SET expires_at = now() WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReportFile — готовый файл отчёта: содержимое, имя, тип, размер.
func (s *Service) ReportFile(ctx context.Context, userID, id uuid.UUID) (io.ReadCloser, string, string, int64, error) {
	var path *string
	var template, format, status, title string
	var created time.Time
	var size *int64
	err := s.pool.QueryRow(ctx, `SELECT file_path, template, format, status, title, created_at, file_size FROM report.report
		WHERE id = $1 AND user_id = $2 AND expires_at > now()`, id, userID).Scan(&path, &template, &format, &status, &title, &created, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", "", 0, ErrNotFound
	}
	if err != nil {
		return nil, "", "", 0, err
	}
	if status != "done" || path == nil || size == nil {
		return nil, "", "", 0, ErrNotReady
	}
	f, err := s.blobs.Open(*path)
	if err != nil {
		return nil, "", "", 0, err
	}
	return f, fileName(template, created, format), reportTypes[format], *size, nil
}

func fileName(template string, created time.Time, format string) string {
	return fmt.Sprintf("report-%s-%s.%s", template, created.Format("2006-01-02-1504"), format)
}

var reportTypes = map[string]string{
	FormatPDF:  "application/pdf",
	FormatXLSX: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

// printPath — адрес страницы печати отчёта в приложении.
func printPath(r *Report) string {
	q := url.Values{}
	for k, v := range r.Params {
		q.Set(k, v)
	}
	path := "/print/" + r.Template
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path
}

// tokenTTL — срок токена печати: страница отчёта открывается за секунды.
const tokenTTL = 15 * time.Minute

// render формирует файл отчёта: страница печати в службе pdf → PDF или XLSX → хранилище →
// уведомление о готовности (и письмо получателям расписания).
func (s *Service) render(ctx context.Context, id uuid.UUID) error {
	if s.engine == nil {
		return errors.New("report: engine is not configured")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE report.report SET status = 'running' WHERE id = $1 AND status IN ('queued', 'running')`, id)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	var userID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM report.report WHERE id = $1`, id).Scan(&userID); err != nil {
		return err
	}
	rep, err := s.Report(ctx, userID, id)
	if err != nil {
		return err
	}
	out, err := s.engine.Renderer.Render(ctx, printPath(rep), s.engine.Tokens(userID, tokenTTL), rep.Format == FormatPDF)
	if err != nil {
		return s.failReport(ctx, rep, userID, err)
	}
	data := out.PDF
	if rep.Format == FormatXLSX {
		if data, err = workbook(out.Page); err != nil {
			return s.failReport(ctx, rep, userID, err)
		}
	}
	obj, err := s.blobs.Put(ctx, "reports", rep.Format, bytes.NewReader(data))
	if err != nil {
		return s.failReport(ctx, rep, userID, err)
	}
	title := rep.Title
	if t := strings.TrimSpace(out.Page.Title); t != "" {
		title = t
	}
	if len([]rune(title)) > 200 {
		title = string([]rune(title)[:200])
	}
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE report.report SET status = 'done', file_path = $2, file_size = $3, title = $4,
			finished_at = now() WHERE id = $1`, id, obj.Path, obj.Size, title); err != nil {
			return err
		}
		return notify.Post(ctx, tx, userID, "report_ready", "info", "Отчёт готов: "+title, "", "/reports?report="+id.String())
	})
	if err != nil {
		return err
	}
	if rep.ScheduleID != nil {
		return s.mailScheduled(ctx, *rep.ScheduleID, rep, title, data)
	}
	return nil
}

func (s *Service) failReport(ctx context.Context, rep *Report, userID uuid.UUID, cause error) error {
	s.engine.Log.WarnContext(ctx, "report failed", "report", rep.ID, "err", cause)
	err := db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE report.report SET status = 'failed', error = $2, finished_at = now() WHERE id = $1`,
			rep.ID, cause.Error()); err != nil {
			return err
		}
		return notify.Post(ctx, tx, userID, "report_failed", "warning", "Отчёт не сформирован: "+rep.Title,
			cause.Error(), "/reports?report="+rep.ID.String())
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	return river.JobCancel(cause)
}

// workbook — XLSX из таблиц страницы отчёта и листа «Источники».
func workbook(p Page) ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	used := map[string]bool{}
	for i, sh := range p.Sheets {
		name := sheetName(sh.Name, i, used)
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		header := make([]any, len(sh.Columns))
		for j, c := range sh.Columns {
			header[j] = c
		}
		if err := f.SetSheetRow(name, "A1", &header); err != nil {
			return nil, err
		}
		for r, row := range sh.Rows {
			cell, err := excelize.CoordinatesToCellName(1, r+2)
			if err != nil {
				return nil, err
			}
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				return nil, err
			}
		}
		if len(sh.Columns) > 0 {
			last, _ := excelize.ColumnNumberToName(len(sh.Columns))
			_ = f.SetColWidth(name, "A", last, 22)
			_ = f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		}
	}
	src := "Источники"
	if len(p.Sheets) == 0 {
		if err := f.SetSheetName("Sheet1", src); err != nil {
			return nil, err
		}
	} else if _, err := f.NewSheet(src); err != nil {
		return nil, err
	}
	_ = f.SetSheetRow(src, "A1", &[]any{"Источник", "Атрибуция", "Лицензия"})
	for i, s := range p.Sources {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		_ = f.SetSheetRow(src, cell, &[]any{s.Title, s.Attribution, s.License})
	}
	_ = f.SetColWidth(src, "A", "C", 40)
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// sheetName — имя листа Excel: до 31 символа, без []:*?/\ и без повторов.
func sheetName(name string, i int, used map[string]bool) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		name = fmt.Sprintf("Таблица %d", i+1)
	}
	if r := []rune(name); len(r) > 28 {
		name = string(r[:28])
	}
	base := name
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	used[name] = true
	return name
}

func (s *Service) mailScheduled(ctx context.Context, scheduleID uuid.UUID, rep *Report, title string, data []byte) error {
	var recipients []string
	if err := s.pool.QueryRow(ctx, `SELECT recipients FROM report.schedule WHERE id = $1`, scheduleID).Scan(&recipients); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	name := fileName(rep.Template, rep.CreatedAt, rep.Format)
	link := strings.TrimRight(s.engine.BaseURL, "/") + "/reports?report=" + rep.ID.String()
	for _, to := range recipients {
		err := s.engine.Mailer.Send(ctx, mail.Message{
			To: to, Subject: "FreightFlow: " + title,
			Text:        "Отчёт «" + title + "» по расписанию — во вложении.\n\nВ системе: " + link + "\n",
			Attachments: []mail.Attachment{{Name: name, Data: data}},
		})
		if err != nil {
			s.engine.Log.WarnContext(ctx, "scheduled report mail failed", "report", rep.ID, "err", err)
		}
	}
	return nil
}
