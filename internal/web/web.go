// Package web — страницы, которые формирует сервер: вход, второй фактор, приглашение,
// восстановление пароля, а также выдача SPA и статики.
package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/google/uuid"
	"github.com/pquerna/otp"

	"freightflow/internal/identity"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/ratelimit"
	"freightflow/internal/platform/session"
)

// themeCookie хранит выбранную тему для серверных страниц (SPA пишет её при смене темы).
const themeCookie = "ff_theme"

// Mailer отправляет письма.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Deps — зависимости веб-слоя.
type Deps struct {
	Identity *identity.Service
	Sessions *session.Manager
	Mailer   Mailer
	BaseURL  string
	Dev      bool
	DevVite  string
	Log      *slog.Logger
}

type handler struct {
	Deps
	assets      *assets
	loginLimit  *ratelimit.Limiter
	forgotLimit *ratelimit.Limiter
}

// New создаёт обработчик серверных страниц и статики.
func New(d Deps) (http.Handler, error) {
	a, err := newAssets(d.Dev)
	if err != nil {
		return nil, err
	}
	h := &handler{
		Deps:        d,
		assets:      a,
		loginLimit:  ratelimit.New(6*time.Second, 10),
		forgotLimit: ratelimit.New(time.Minute, 3),
	}
	h.BaseURL = strings.TrimRight(h.BaseURL, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("GET /login/2fa", h.secondFactorForm)
	mux.HandleFunc("POST /login/2fa", h.secondFactor)
	mux.HandleFunc("GET /setup-2fa", h.setupTOTPForm)
	mux.HandleFunc("POST /setup-2fa", h.setupTOTP)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /invite/{token}", h.inviteForm)
	mux.HandleFunc("POST /invite/{token}", h.acceptInvite)
	mux.HandleFunc("GET /password/forgot", h.forgotForm)
	mux.HandleFunc("POST /password/forgot", h.forgot)
	mux.HandleFunc("GET /password/reset/{token}", h.resetForm)
	mux.HandleFunc("POST /password/reset/{token}", h.reset)
	mux.HandleFunc("GET /", h.app)
	return mux, nil
}

func (h *handler) page(r *http.Request, title string) page {
	p := page{Title: title, CSS: h.assets.serverCSS()}
	if c, err := r.Cookie(themeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		p.Theme = c.Value
	}
	return p
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		h.Log.ErrorContext(r.Context(), "render page", "error", err)
	}
}

func meta(r *http.Request) identity.RequestMeta {
	c := httpx.ClientFrom(r.Context())
	return identity.RequestMeta{IP: c.IP, UserAgent: c.UserAgent}
}

func clientKey(r *http.Request) string {
	return httpx.ClientFrom(r.Context()).IP.String()
}

// safeNext допускает только относительные адреса внутри приложения (защита от открытого перенаправления).
func safeNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return u.RequestURI()
}

// --- Вход ---

func (h *handler) loginForm(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if _, _, ok := h.Sessions.UserID(r.Context()); ok {
		http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next проверен safeNext
		return
	}
	h.render(w, r, http.StatusOK, loginPage(h.page(r, "Вход"), formState{}, next))
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.PostFormValue("next"))
	f := formState{Values: map[string]string{"email": strings.TrimSpace(r.PostFormValue("email"))}}
	if !h.loginLimit.Allow(clientKey(r)) {
		f.Error = "Слишком много попыток входа. Подождите минуту."
		h.render(w, r, http.StatusTooManyRequests, loginPage(h.page(r, "Вход"), f, next))
		return
	}
	res, err := h.Identity.Authenticate(r.Context(), r.PostFormValue("email"), r.PostFormValue("password"), meta(r))
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		f.Error = "Неверный адрес электронной почты или пароль."
	case errors.Is(err, identity.ErrAccountLocked):
		f.Error = "Слишком много неудачных попыток. Вход временно заблокирован на 15 минут."
	case errors.Is(err, identity.ErrAccountBlocked):
		f.Error = "Учётная запись заблокирована. Обратитесь к администратору."
	case err != nil:
		h.internalError(w, r, err)
		return
	}
	if f.Error != "" {
		h.render(w, r, http.StatusUnprocessableEntity, loginPage(h.page(r, "Вход"), f, next))
		return
	}
	if res.NeedsSecondFactor {
		if err := h.Sessions.RenewToken(r.Context()); err != nil {
			h.internalError(w, r, err)
			return
		}
		h.Sessions.SetPending(r.Context(), res.UserID)
		http.Redirect(w, r, "/login/2fa?next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	h.completeLogin(w, r, res.UserID, next)
}

// completeLogin начинает сессию. Администратор без второго фактора сначала подключает его.
func (h *handler) completeLogin(w http.ResponseWriter, r *http.Request, userID uuid.UUID, next string) {
	if err := h.Sessions.Login(r.Context(), userID); err != nil {
		h.internalError(w, r, err)
		return
	}
	u, err := h.Identity.GetUser(r.Context(), userID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !u.TOTPEnabled && slices.Contains(u.Roles, identity.RoleAdmin) {
		http.Redirect(w, r, "/setup-2fa?next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next проверен safeNext
}

func (h *handler) secondFactorForm(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.Sessions.Pending(r.Context()); !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	h.render(w, r, http.StatusOK, secondFactorPage(h.page(r, "Подтверждение входа"), formState{}, next))
}

func (h *handler) secondFactor(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.PostFormValue("next"))
	uid, ok := h.Sessions.Pending(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	f := formState{}
	if !h.loginLimit.Allow(clientKey(r)) {
		f.Error = "Слишком много попыток. Подождите минуту."
		h.render(w, r, http.StatusTooManyRequests, secondFactorPage(h.page(r, "Подтверждение входа"), f, next))
		return
	}
	err := h.Identity.VerifySecondFactor(r.Context(), uid, r.PostFormValue("code"), meta(r))
	switch {
	case errors.Is(err, identity.ErrInvalidCode):
		f.Errors = map[string]string{"code": "Неверный код. Проверьте время на телефоне или используйте резервный код."}
	case errors.Is(err, identity.ErrAccountLocked):
		f.Error = "Слишком много неудачных попыток. Вход временно заблокирован на 15 минут."
	case errors.Is(err, identity.ErrAccountBlocked):
		f.Error = "Учётная запись заблокирована. Обратитесь к администратору."
	case err != nil:
		h.internalError(w, r, err)
		return
	}
	if f.Error != "" || len(f.Errors) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, secondFactorPage(h.page(r, "Подтверждение входа"), f, next))
		return
	}
	h.Sessions.ClearPending(r.Context())
	h.completeLogin(w, r, uid, next)
}

// --- Подключение второго фактора ---

func (h *handler) setupTOTPForm(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	uid, _, ok := h.Sessions.UserID(r.Context())
	if !ok {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/setup-2fa?next="+next), http.StatusSeeOther)
		return
	}
	setup, err := h.Identity.BeginTOTPSetup(r.Context(), uid)
	if errors.Is(err, identity.ErrTOTPEnabled) {
		http.Redirect(w, r, next, http.StatusSeeOther) //nolint:gosec // next проверен safeNext
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	qr, err := qrDataURI(setup.URL)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, setupTOTPPage(h.page(r, "Двухфакторная защита"), formState{}, qr, setup.Secret, next))
}

func (h *handler) setupTOTP(w http.ResponseWriter, r *http.Request) {
	uid, _, ok := h.Sessions.UserID(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	next := safeNext(r.PostFormValue("next"))
	codes, err := h.Identity.ConfirmTOTP(r.Context(), uid, r.PostFormValue("code"), meta(r))
	if errors.Is(err, identity.ErrInvalidCode) {
		// Секрет уже сохранён: показываем тот же код заново.
		setup, serr := h.Identity.BeginTOTPSetup(r.Context(), uid)
		if serr != nil {
			h.internalError(w, r, serr)
			return
		}
		qr, qerr := qrDataURI(setup.URL)
		if qerr != nil {
			h.internalError(w, r, qerr)
			return
		}
		f := formState{Errors: map[string]string{"code": "Неверный код. Отсканируйте новый QR-код и попробуйте снова."}}
		h.render(w, r, http.StatusUnprocessableEntity, setupTOTPPage(h.page(r, "Двухфакторная защита"), f, qr, setup.Secret, next))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, recoveryCodesPage(h.page(r, "Резервные коды"), codes, next))
}

func qrDataURI(otpURL string) (string, error) {
	key, err := otp.NewKeyFromURL(otpURL)
	if err != nil {
		return "", fmt.Errorf("parse otp url: %w", err)
	}
	img, err := key.Image(360, 360)
	if err != nil {
		return "", fmt.Errorf("render qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encode qr: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Sessions.Logout(r.Context()); err != nil {
		h.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- Приглашение ---

func (h *handler) inviteForm(w http.ResponseWriter, r *http.Request) {
	email, err := h.Identity.InviteEmail(r.Context(), r.PathValue("token"))
	if errors.Is(err, identity.ErrInvalidToken) {
		h.render(w, r, http.StatusNotFound, messagePage(h.page(r, "Приглашение недействительно"),
			"Ссылка устарела или уже использована. Попросите администратора отправить новое приглашение.", "/login", "Ко входу"))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, invitePage(h.page(r, "Приглашение"), formState{}, email))
}

func (h *handler) acceptInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	email, err := h.Identity.InviteEmail(r.Context(), token)
	if errors.Is(err, identity.ErrInvalidToken) {
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther) //nolint:gosec // путь текущего запроса
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	name := r.PostFormValue("name")
	u, err := h.Identity.AcceptInvite(r.Context(), token, name, r.PostFormValue("password"), meta(r))
	var ve *identity.ValidationError
	if errors.As(err, &ve) {
		f := formState{Values: map[string]string{"name": name}, Errors: map[string]string{ve.Field: ve.Message}}
		h.render(w, r, http.StatusUnprocessableEntity, invitePage(h.page(r, "Приглашение"), f, email))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	h.completeLogin(w, r, u.ID, "/")
}

// --- Восстановление пароля ---

func (h *handler) forgotForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, forgotPage(h.page(r, "Восстановление пароля"), formState{}))
}

func (h *handler) forgot(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	f := formState{Values: map[string]string{"email": email}}
	if !h.forgotLimit.Allow(clientKey(r)) {
		f.Error = "Слишком много запросов. Попробуйте позже."
		h.render(w, r, http.StatusTooManyRequests, forgotPage(h.page(r, "Восстановление пароля"), f))
		return
	}
	token, u, ok, err := h.Identity.RequestPasswordReset(r.Context(), email, meta(r))
	var ve *identity.ValidationError
	if errors.As(err, &ve) {
		f.Errors = map[string]string{ve.Field: ve.Message}
		h.render(w, r, http.StatusUnprocessableEntity, forgotPage(h.page(r, "Восстановление пароля"), f))
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if ok {
		link := h.BaseURL + "/password/reset/" + token
		msg := mail.Message{
			To:      u.Email,
			Subject: "Смена пароля в FreightFlow",
			Text: "Здравствуйте, " + u.Name + "!\n\nЧтобы задать новый пароль, перейдите по ссылке (действует 1 час):\n" +
				link + "\n\nЕсли вы не запрашивали смену пароля, просто проигнорируйте это письмо.\n",
		}
		if err := h.Mailer.Send(r.Context(), msg); err != nil {
			h.Log.ErrorContext(r.Context(), "send password reset", "error", err)
		}
	}
	// Ответ одинаков независимо от того, существует ли учётная запись.
	h.render(w, r, http.StatusOK, messagePage(h.page(r, "Проверьте почту"),
		"Если адрес зарегистрирован, на него отправлена ссылка для смены пароля. Ссылка действует 1 час.", "/login", "Ко входу"))
}

func (h *handler) resetForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, resetPage(h.page(r, "Новый пароль"), formState{}))
}

func (h *handler) reset(w http.ResponseWriter, r *http.Request) {
	err := h.Identity.ResetPassword(r.Context(), r.PathValue("token"), r.PostFormValue("password"), meta(r))
	var ve *identity.ValidationError
	switch {
	case errors.As(err, &ve):
		f := formState{Errors: map[string]string{ve.Field: ve.Message}}
		h.render(w, r, http.StatusUnprocessableEntity, resetPage(h.page(r, "Новый пароль"), f))
	case errors.Is(err, identity.ErrInvalidToken):
		h.render(w, r, http.StatusNotFound, messagePage(h.page(r, "Ссылка недействительна"),
			"Ссылка для смены пароля устарела или уже использована. Запросите новую.", "/password/forgot", "Запросить ссылку"))
	case err != nil:
		h.internalError(w, r, err)
	default:
		h.render(w, r, http.StatusOK, messagePage(h.page(r, "Пароль изменён"),
			"Теперь можно войти с новым паролем. Другие сеансы завершены.", "/login", "Войти"))
	}
}

// --- Приложение и статика ---

// app отдаёт статику и SPA. В разработке SPA обслуживает сервер Vite.
func (h *handler) app(w http.ResponseWriter, r *http.Request) {
	if h.assets.serveStatic(w, r) {
		return
	}
	if strings.Contains(lastSegment(r.URL.Path), ".") {
		http.NotFound(w, r)
		return
	}
	if _, _, ok := h.Sessions.UserID(r.Context()); !ok {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	if h.Dev {
		target := h.DevVite
		if target == "" {
			target = "http://localhost:5173"
		}
		// Только в разработке: адрес сервера Vite задан конфигурацией, путь — текущего запроса.
		http.Redirect(w, r, strings.TrimRight(target, "/")+r.URL.RequestURI(), http.StatusTemporaryRedirect) //nolint:gosec // см. выше
		return
	}
	if len(h.assets.index) == 0 {
		http.Error(w, "frontend is not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(h.assets.index)
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (h *handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.Log.ErrorContext(r.Context(), "web handler error", "request_id", httpx.RequestID(r.Context()), "error", err)
	h.render(w, r, http.StatusInternalServerError, messagePage(h.page(r, "Ошибка"),
		"Что-то пошло не так. Попробуйте ещё раз; если ошибка повторится — сообщите администратору.", "/", "На главную"))
}
