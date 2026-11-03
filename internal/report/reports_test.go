package report

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/mail"
)

// fakeRenderer — подставная служба pdf: запоминает адреса и токены, отдаёт заданную страницу.
type fakeRenderer struct {
	mu     sync.Mutex
	paths  []string
	tokens []string
	page   Page
	err    error
}

func (f *fakeRenderer) Render(_ context.Context, path, token string, pdf bool) (*Rendered, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, path)
	f.tokens = append(f.tokens, token)
	if f.err != nil {
		return nil, f.err
	}
	out := &Rendered{Page: f.page}
	if pdf {
		out.PDF = []byte("%PDF-1.7 тест")
	}
	return out, nil
}

type memMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *memMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func TestTemplateParams(t *testing.T) {
	t.Parallel()
	tpl, _ := TemplateByCode("passability")
	if _, err := tpl.ValidateParams(map[string]string{"territoryId": uuid.NewString()}); err == nil {
		t.Fatal("missing profile accepted")
	}
	got, err := tpl.ValidateParams(map[string]string{"territoryId": uuid.NewString(), "profile": "truck_20", "extra": "x"})
	if err != nil || len(got) != 2 || got["extra"] != "" {
		t.Fatalf("params: %v (%v)", got, err)
	}
	rw, _ := TemplateByCode("roadworks")
	for _, bad := range []string{"3", "200", "семь"} {
		if _, err := rw.ValidateParams(map[string]string{"days": bad}); err == nil {
			t.Errorf("days %q accepted", bad)
		}
	}
	if _, err := rw.ValidateParams(map[string]string{}); err != nil {
		t.Fatalf("optional params: %v", err)
	}
	f, _ := TemplateByCode("facility")
	if _, err := f.ValidateParams(map[string]string{"facilityId": "не id"}); err == nil {
		t.Fatal("bad uuid accepted")
	}
}

func TestScheduleNextRun(t *testing.T) {
	t.Parallel()
	// Среда, 4 ноября 2026, 10:30 по Москве.
	now := time.Date(2026, 11, 4, 10, 30, 0, 0, moscow)
	wd, md := 1, 3
	cases := []struct {
		in   ScheduleInput
		want time.Time
	}{
		{ScheduleInput{Frequency: "daily", Hour: 8}, time.Date(2026, 11, 5, 8, 0, 0, 0, moscow)},
		{ScheduleInput{Frequency: "daily", Hour: 11}, time.Date(2026, 11, 4, 11, 0, 0, 0, moscow)},
		{ScheduleInput{Frequency: "weekly", Weekday: &wd, Hour: 9}, time.Date(2026, 11, 9, 9, 0, 0, 0, moscow)},
		{ScheduleInput{Frequency: "monthly", MonthDay: &md, Hour: 7}, time.Date(2026, 12, 3, 7, 0, 0, 0, moscow)},
	}
	for _, c := range cases {
		if got := c.in.NextRun(now); !got.Equal(c.want) {
			t.Errorf("%s: %s, want %s", c.in.Frequency, got.In(moscow), c.want)
		}
	}
	bad := ScheduleInput{Template: "quality", Format: "pdf", Frequency: "weekly", Hour: 8}
	if err := bad.Validate(); err == nil {
		t.Fatal("weekly without weekday accepted")
	}
	in := ScheduleInput{
		Template: "quality", Format: "xlsx", Frequency: "daily", Hour: 8,
		Recipients: []string{"Boss@Example.com", "boss@example.com", "  ops@example.com "},
	}
	if err := in.Validate(); err != nil || strings.Join(in.Recipients, ",") != "boss@example.com,ops@example.com" || in.Title != "Качество данных" {
		t.Fatalf("validate: %+v (%v)", in, err)
	}
	in.Recipients = []string{"не почта"}
	if err := in.Validate(); err == nil {
		t.Fatal("bad recipient accepted")
	}
}

func TestWorkbookFromPage(t *testing.T) {
	t.Parallel()
	b, err := workbook(Page{
		Sheets: []Sheet{
			{Name: "Доступность: [грузовик]", Columns: []string{"Профиль", "Минуты"}, Rows: [][]any{{"Грузовик 20 т", 11.5}, {"Фургон", nil}}},
			{Name: "Доступность: [грузовик]", Columns: []string{"Ещё"}, Rows: [][]any{{1}}},
		},
		Sources: []PageSource{{Title: "OSM", Attribution: "© участники OpenStreetMap", License: "ODbL 1.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	names := f.GetSheetList()
	if strings.Join(names, "|") != "Доступность   грузовик |Доступность   грузовик  2|Источники" {
		t.Fatalf("sheets: %q", names)
	}
	if v, _ := f.GetCellValue(names[0], "B2"); v != "11.5" {
		t.Fatalf("B2 = %q", v)
	}
	if v, _ := f.GetCellValue("Источники", "C2"); v != "ODbL 1.0" {
		t.Fatalf("license = %q", v)
	}
}

func TestRenderReportNotifyAndMail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := dbtest.New(t)
	blobs, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	user := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.app_user (id, email, name, status) VALUES ($1, 'r@example.com', 'Аналитик', 'active')`, user); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRenderer{page: Page{Title: "Паспорт объекта: Склад", Sheets: []Sheet{{Name: "Лист", Columns: []string{"a"}, Rows: [][]any{{1}}}}}}
	mailer := &memMailer{}
	s := New(pool, nil, blobs, nil)
	s.SetEngine(Engine{
		Renderer: fr, Mailer: mailer, BaseURL: "https://ff.example", Log: slog.New(slog.DiscardHandler),
		Tokens: func(uid uuid.UUID, _ time.Duration) string { return "ffp_" + uid.String() },
	})
	fid := uuid.NewString()
	rep, err := s.CreateReport(ctx, user, "facility", "pdf", map[string]string{"facilityId": fid})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.render(ctx, rep.ID); err != nil {
		t.Fatal(err)
	}
	if fr.paths[0] != "/print/facility?facilityId="+fid || fr.tokens[0] != "ffp_"+user.String() {
		t.Fatalf("render call: %v %v", fr.paths, fr.tokens)
	}
	got, err := s.Report(ctx, user, rep.ID)
	if err != nil || got.Status != "done" || got.Title != "Паспорт объекта: Склад" || got.FileSize == nil {
		t.Fatalf("report: %+v (%v)", got, err)
	}
	f, name, typ, _, err := s.ReportFile(ctx, user, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if !strings.HasPrefix(name, "report-facility-") || typ != "application/pdf" {
		t.Fatalf("file: %s %s", name, typ)
	}
	if _, _, _, _, err := s.ReportFile(ctx, uuid.New(), rep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user file: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notify.notification WHERE user_id = $1 AND kind = 'report_ready'`, user).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ready notifications = %d (%v)", n, err)
	}

	// Расписание: отчёт ставится в срок и рассылается письмом с файлом.
	wd := 3
	sc, err := s.SaveSchedule(ctx, user, nil, ScheduleInput{
		Template: "quality", Format: "xlsx", Frequency: "weekly", Weekday: &wd,
		Hour: 8, Recipients: []string{"ops@example.com"}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if queued, err := s.RunDue(ctx, sc.NextRunAt.Add(-time.Minute)); err != nil || queued != 0 {
		t.Fatalf("early run: %d (%v)", queued, err)
	}
	if queued, err := s.RunDue(ctx, sc.NextRunAt.Add(time.Minute)); err != nil || queued != 1 {
		t.Fatalf("due run: %d (%v)", queued, err)
	}
	list, err := s.Reports(ctx, user, 10)
	if err != nil || len(list) != 2 || list[0].ScheduleID == nil {
		t.Fatalf("reports: %+v (%v)", list, err)
	}
	if err := s.render(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 || mailer.sent[0].To != "ops@example.com" || len(mailer.sent[0].Attachments) != 1 ||
		!strings.HasSuffix(mailer.sent[0].Attachments[0].Name, ".xlsx") {
		t.Fatalf("mail: %+v", mailer.sent)
	}
	next, _ := s.Schedules(ctx, user)
	if !next[0].NextRunAt.After(sc.NextRunAt) || next[0].LastRunAt == nil {
		t.Fatalf("schedule after run: %+v", next[0])
	}

	// Ошибка страницы — отчёт failed и уведомление.
	fr.err = errors.New("страница не загрузилась")
	bad, err := s.CreateReport(ctx, user, "quality", "pdf", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.render(ctx, bad.ID)
	if got, _ := s.Report(ctx, user, bad.ID); got.Status != "failed" || got.Error == nil {
		t.Fatalf("failed report: %+v", got)
	}
	if _, err := s.CreateReport(ctx, user, "nope", "pdf", nil); err == nil {
		t.Fatal("unknown template accepted")
	}
}
