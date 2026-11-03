package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"freightflow/internal/alerting"
	"freightflow/internal/identity"
	"freightflow/internal/platform/dbtest"
	"freightflow/internal/platform/events"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/secret"
)

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

// fakeBot — подставной Bot API: отвечает getMe, отдаёт заданные обновления, копит сообщения.
type fakeBot struct {
	mu      sync.Mutex
	updates []map[string]any
	sent    []map[string]any
}

func (b *fakeBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var args map[string]any
	_ = json.NewDecoder(r.Body).Decode(&args)
	var result any = true
	switch {
	case strings.HasSuffix(r.URL.Path, "/getMe"):
		result = map[string]any{"username": "ff_test_bot"}
	case strings.HasSuffix(r.URL.Path, "/getUpdates"):
		result = b.updates
		b.updates = nil
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		b.sent = append(b.sent, args)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// hook — получатель вебхука: проверяет подпись и копит тела; status — код ответа.
type hook struct {
	mu     sync.Mutex
	secret []byte
	status int
	bodies []webhookBody
	bad    int
}

func (h *hook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	if !VerifySignature(h.secret, r.Header.Get("X-FreightFlow-Timestamp"), body, r.Header.Get("X-FreightFlow-Signature"),
		time.Now(), 5*time.Minute) {
		h.bad++
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var b webhookBody
	_ = json.Unmarshal(body, &b)
	h.bodies = append(h.bodies, b)
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
}

type env struct {
	pool      *pgxpool.Pool
	svc       *Service
	alerts    *alerting.Service
	mailer    *memMailer
	bot       *fakeBot
	owner, op uuid.UUID
	consumer  events.Consumer
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := t.Context()
	pool := dbtest.New(t)
	box, err := secret.NewBox(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.NewService(pool, box).SyncBuiltinRoles(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{pool: pool, mailer: &memMailer{}, bot: &fakeBot{}, owner: uuid.New(), op: uuid.New()}
	for _, q := range []string{
		`INSERT INTO core.app_user (id, email, name, status) VALUES
			($1, 'owner@example.com', 'Аналитик', 'active'), ($2, 'op@example.com', 'Оператор', 'active')`,
		`INSERT INTO core.user_role (user_id, role_code) VALUES ($1, 'analyst'), ($2, 'operator')`,
	} {
		if _, err := pool.Exec(ctx, q, e.owner, e.op); err != nil {
			t.Fatal(err)
		}
	}
	bot := httptest.NewServer(e.bot)
	t.Cleanup(bot.Close)
	log := slog.New(slog.DiscardHandler)
	e.alerts = alerting.New(pool, nil, log)
	e.svc, err = New(ctx, pool, nil, box, e.mailer, e.alerts, Config{
		BaseURL: "https://ff.example", TelegramToken: "123:abc", TelegramAPI: bot.URL, AllowPrivateWebhooks: true,
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	e.consumer = e.svc.Consumer()
	if err := e.consumer.Register(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// consume обрабатывает события, пока не появится want уведомлений (граница снимка может
// ждать транзакций параллельных тестов).
func (e *env) consume(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for e.count(t, `SELECT count(*) FROM notify.notification`) < want {
		if _, err := e.consumer.Step(t.Context(), e.pool); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("notifications: %d, want %d", e.count(t, `SELECT count(*) FROM notify.notification`), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// deliverAll выполняет задачи всех ожидающих доставок, как это сделала бы очередь.
func (e *env) deliverAll(t *testing.T) {
	t.Helper()
	rows, err := e.pool.Query(t.Context(), `SELECT id FROM notify.delivery WHERE status = 'pending' AND scheduled_at <= now()`)
	if err != nil {
		t.Fatal(err)
	}
	var list []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		list = append(list, id)
	}
	w := &DeliverWorker{Service: e.svc, Log: slog.New(slog.DiscardHandler)}
	for _, id := range list {
		job := &river.Job[DeliverArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: deliverAttempts}, Args: DeliverArgs{ID: id}}
		_ = w.Work(t.Context(), job)
	}
}

const corridor = `{"type":"LineString","coordinates":[[37.620,55.755],[37.640,55.757]]}`

// raise — новая работа в коридоре и проверка правила: алерт и событие alert.created.
func (e *env) raise(t *testing.T, r *alerting.Rule, gid int, kind string) {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO operations.road_work (id, mos_dataset, mos_global_id, kind, title, place, geom, content_hash, source_code)
		VALUES ($1, 1, $2, $3, 'Ремонт теплосети', 'улица Варварка, 10', ST_SetSRID(ST_MakePoint(37.63, 55.756), 4326), 'h', 'test')`,
		id, gid, kind)
	e.exec(t, `INSERT INTO operations.road_work_event (work_id, type) VALUES ($1, 'appeared')`, id)
	if _, err := e.alerts.CheckRule(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAlertReachesAllChannels(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := t.Context()

	// Telegram: код из профиля, «/start код» боту — чат привязан.
	link, err := e.svc.LinkTelegram(ctx, e.owner)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link.URL, "https://t.me/ff_test_bot?start=") {
		t.Fatalf("link = %s", link.URL)
	}
	e.bot.updates = []map[string]any{{"update_id": 10, "message": map[string]any{
		"text": "/start " + link.Code, "chat": map[string]any{"id": 555, "type": "private", "username": "analyst"},
	}}}
	if err := e.svc.PollStep(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.TelegramStatus(ctx, e.owner); !st.Bound || st.Username == nil || *st.Username != "analyst" {
		t.Fatalf("telegram status: %+v", st)
	}
	if n := e.count(t, `SELECT offset_id FROM notify.telegram_state`); n != 11 {
		t.Fatalf("offset = %d", n)
	}

	// Вебхук: секрет выдаётся один раз, получатель проверяет подпись.
	h := &hook{}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	wh, plain, err := e.svc.CreateWebhook(ctx, e.owner, "Диспетчерская", srv.URL+"/ff")
	if err != nil {
		t.Fatal(err)
	}
	h.secret = []byte(plain)
	if e.count(t, `SELECT count(*) FROM notify.webhook WHERE secret LIKE '%whsec%'`) != 0 {
		t.Fatal("webhook secret stored in plain text")
	}

	// Оператор подписан на все алерты от «предупреждения».
	if _, err := e.svc.SetPreferences(ctx, e.op, true, Preferences{
		EmailMode: "instant", DigestHour: 9, MinSeverity: "info",
		SubscribeAll: true, SubscribeSeverity: "warning",
	}); err != nil {
		t.Fatal(err)
	}

	r, err := e.alerts.CreateRule(ctx, alerting.Actor{UserID: e.owner}, alerting.RuleInput{
		Name: "Варварка", Kind: alerting.KindRoadworks, Enabled: true,
		Scope:    alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(corridor)},
		Channels: []string{"in_app", "email", "telegram", "webhook"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.raise(t, r, 1, "emergency")
	e.consume(t, 2)

	page, err := e.svc.List(ctx, e.owner, false, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Unread != 1 || page.Items[0].Severity != "critical" ||
		!strings.HasPrefix(page.Items[0].Title, "Аварийная работа") || *page.Items[0].Link != "/alerts?alert="+page.Items[0].AlertID.String() {
		t.Fatalf("owner notifications: %+v", page)
	}
	// Владельцу — почта, Telegram, вебхук; оператору — почта (Telegram не привязан).
	if got := e.count(t, `SELECT count(*) FROM notify.delivery WHERE user_id = $1`, e.owner); got != 3 {
		t.Fatalf("owner deliveries = %d", got)
	}
	if got := e.count(t, `SELECT count(*) FROM notify.delivery WHERE user_id = $1 AND channel = 'email'`, e.op); got != 1 {
		t.Fatalf("operator deliveries = %d", got)
	}
	if n := e.count(t, `SELECT count(*) FROM notify.outbox WHERE type = 'notification.created'`); n != 2 {
		t.Fatalf("notification events = %d", n)
	}
	// Повторная обработка тех же событий невозможна: позиция потребителя сдвинута.
	if _, err := e.consumer.Step(ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM notify.notification`); n != 2 {
		t.Fatalf("notifications after second step = %d", n)
	}

	e.deliverAll(t)
	if n := e.count(t, `SELECT count(*) FROM notify.delivery WHERE status = 'sent'`); n != 4 {
		t.Fatalf("sent = %d, statuses: %v", n, e.statuses(t))
	}
	if len(e.mailer.sent) != 2 || !strings.HasPrefix(e.mailer.sent[0].Subject, "[Критично] Аварийная работа") ||
		!strings.Contains(e.mailer.sent[0].HTML, "Открыть алерт") || !strings.Contains(e.mailer.sent[0].Text, "https://ff.example/alerts?alert=") {
		t.Fatalf("mail: %+v", e.mailer.sent)
	}
	// Ответ бота на /start и сообщение об алерте.
	if len(e.bot.sent) != 2 || e.bot.sent[1]["chat_id"] != float64(555) || !strings.Contains(e.bot.sent[1]["text"].(string), "<b>Критично</b>") {
		t.Fatalf("telegram: %+v", e.bot.sent)
	}
	if h.bad != 0 || len(h.bodies) != 1 || h.bodies[0].Event != "alert.created" {
		t.Fatalf("webhook: bad %d, bodies %+v", h.bad, h.bodies)
	}
	var data alertPayload
	if err := json.Unmarshal(h.bodies[0].Data, &data); err != nil || data.Alert.RuleName != "Варварка" {
		t.Fatalf("webhook data: %s (%v)", h.bodies[0].Data, err)
	}
	log, err := e.svc.Deliveries(ctx, e.owner, wh.ID, 10)
	if err != nil || len(log) != 1 || log[0].Status != "sent" || *log[0].ResponseCode != 200 {
		t.Fatalf("deliveries log: %+v (%v)", log, err)
	}
	if _, err := e.svc.Deliveries(ctx, e.op, wh.ID, 10); err != ErrNotFound { //nolint:errorlint // своя ошибка
		t.Fatalf("other user reads webhook log: %v", err)
	}

	// Получатель отвечает 410 — доставка прекращена; 503 — будет повтор.
	h.status = http.StatusGone
	e.raise(t, r, 2, "repair")
	e.consume(t, 4)
	e.deliverAll(t)
	if n := e.count(t, `SELECT count(*) FROM notify.delivery WHERE channel = 'webhook' AND status = 'failed' AND response_code = 410`); n != 1 {
		t.Fatalf("410 failures = %d, statuses: %v", n, e.statuses(t))
	}
	h.status = http.StatusServiceUnavailable
	d, err := e.svc.TestWebhook(ctx, e.owner, wh.ID)
	if err != nil || d.Status != "failed" || *d.ResponseCode != 503 {
		t.Fatalf("test webhook: %+v (%v)", d, err)
	}

	// Прочитать все.
	if err := e.svc.MarkRead(ctx, e.owner, nil, true); err != nil {
		t.Fatal(err)
	}
	if page, _ = e.svc.List(ctx, e.owner, true, 10, ""); page.Unread != 0 || len(page.Items) != 0 {
		t.Fatalf("after mark read: %+v", page)
	}
}

func (e *env) statuses(t *testing.T) []string {
	t.Helper()
	rows, err := e.pool.Query(t.Context(), `SELECT channel || ':' || status || ':' || coalesce(last_error, '') FROM notify.delivery`)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestQuietHoursDigestAndAssignment(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := t.Context()
	// 23:30 по Москве; тихие часы 23–7, почта дайджестом в 9.
	e.svc.now = func() time.Time { return time.Date(2026, 11, 3, 23, 30, 0, 0, moscow) }
	from, to := 23, 7
	if _, err := e.svc.SetPreferences(ctx, e.owner, false, Preferences{
		EmailMode: "instant", DigestHour: 9, MinSeverity: "info",
		QuietFrom: &from, QuietTo: &to, SubscribeAll: true, SubscribeSeverity: "info",
	}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.Preferences(ctx, e.owner); p.SubscribeAll {
		t.Fatal("analyst subscribed to all alerts without alerts.manage")
	}
	r, err := e.alerts.CreateRule(ctx, alerting.Actor{UserID: e.owner}, alerting.RuleInput{
		Name: "Варварка", Kind: alerting.KindRoadworks, Enabled: true,
		Scope:    alerting.Scope{Type: alerting.ScopeCorridor, Line: json.RawMessage(corridor)},
		Channels: []string{"in_app", "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.raise(t, r, 1, "repair")
	e.consume(t, 1)
	var at time.Time
	if err := e.pool.QueryRow(ctx, `SELECT scheduled_at FROM notify.delivery`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 4, 7, 0, 0, 0, moscow); !at.Equal(want) {
		t.Fatalf("quiet delivery at %s, want %s", at.In(moscow), want)
	}
	// Критичная работа — сразу, несмотря на тихие часы.
	e.raise(t, r, 2, "emergency")
	e.consume(t, 2)
	if n := e.count(t, `SELECT count(*) FROM notify.delivery WHERE scheduled_at = '2026-11-03 23:30:00+03'`); n != 1 {
		t.Fatalf("critical not immediate: %v", e.statuses(t))
	}

	// Дайджест: письма копятся и уходят одним в час дайджеста.
	if _, err := e.svc.SetPreferences(ctx, e.owner, false, Preferences{
		EmailMode: "digest", DigestHour: 9, MinSeverity: "info",
		SubscribeSeverity: "critical",
	}); err != nil {
		t.Fatal(err)
	}
	e.raise(t, r, 3, "repair")
	e.raise(t, r, 4, "closure")
	e.consume(t, 4)
	if n := e.count(t, `SELECT count(*) FROM notify.delivery WHERE status = 'digest'`); n != 2 {
		t.Fatalf("digest deliveries = %d: %v", n, e.statuses(t))
	}
	if n, _ := e.svc.SendDigests(ctx); n != 0 {
		t.Fatalf("digest sent at 23:30: %d", n)
	}
	e.svc.now = func() time.Time { return time.Date(2026, 11, 4, 9, 5, 0, 0, moscow) }
	if n, err := e.svc.SendDigests(ctx); err != nil || n != 1 {
		t.Fatalf("digests = %d (%v)", n, err)
	}
	if len(e.mailer.sent) != 1 || e.mailer.sent[0].Subject != "FreightFlow: алерты за сутки (2)" {
		t.Fatalf("digest mail: %+v", e.mailer.sent)
	}

	// Назначение — уведомление ответственному.
	list, err := e.alerts.List(ctx, alerting.Actor{UserID: e.owner}, alerting.Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.alerts.Act(ctx, alerting.Actor{UserID: e.op, Manage: true}, list.Items[0].ID,
		alerting.Change{Action: alerting.ActionAssign, Assignee: &e.op}); err != nil {
		t.Fatal(err)
	}
	e.consume(t, 5)
	page, err := e.svc.List(ctx, e.op, true, 10, "")
	if err != nil || page.Unread != 1 || !strings.HasPrefix(page.Items[0].Title, "Вам назначен алерт: ") {
		t.Fatalf("assignee notifications: %+v (%v)", page, err)
	}
}

func TestWebhookURLValidation(t *testing.T) {
	t.Parallel()
	s := &Service{cfg: Config{}}
	for _, raw := range []string{
		"http://example.com/h", "https://127.0.0.1/h", "https://10.0.0.5/h", "https://localhost/h",
		"https://user:pw@example.com/h", "ftp://example.com", "не адрес",
	} {
		if _, err := s.checkURL(raw); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
	if u, err := s.checkURL(" https://hooks.example.com/ff#x "); err != nil || u != "https://hooks.example.com/ff" {
		t.Fatalf("valid url: %q (%v)", u, err)
	}
	sig := Sign([]byte("k"), "100", []byte("{}"))
	if !VerifySignature([]byte("k"), "100", []byte("{}"), sig, time.Unix(150, 0), time.Minute) {
		t.Fatal("signature rejected")
	}
	if VerifySignature([]byte("k"), "100", []byte("{}"), sig, time.Unix(1000, 0), time.Minute) {
		t.Fatal("old timestamp accepted")
	}
	if VerifySignature([]byte("other"), "100", []byte("{}"), sig, time.Unix(150, 0), time.Minute) {
		t.Fatal("wrong secret accepted")
	}
}
