// Package api реализует операции OpenAPI (api/openapi.yaml) поверх сервисов модулей.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	spec "freightflow/api"
	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/ingest"
	"freightflow/internal/platform/httpx"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/session"
	"freightflow/internal/regulation"
	"freightflow/internal/routing"
	"freightflow/internal/tiles"
)

// Mailer отправляет письма.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Deps — зависимости API.
type Deps struct {
	Identity *identity.Service
	Ingest   *ingest.Service
	Layers   *tiles.Service
	// Regulation — зоны, правила и статистика регулирования.
	Regulation *regulation.Service
	// Routing — маршруты грузовиков.
	Routing  *routing.Service
	Sessions *session.Manager
	Mailer   Mailer
	BaseURL  string
	Log      *slog.Logger
	// Meters и Tracers — метрики и трассы операций; nil — глобальные провайдеры OpenTelemetry.
	Meters  metric.MeterProvider
	Tracers trace.TracerProvider
}

// Server реализует обработчики и проверку безопасности ogen.
type Server struct {
	identity   *identity.Service
	ingest     *ingest.Service
	layers     *tiles.Service
	regulation *regulation.Service
	routing    *routing.Service
	sessions   *session.Manager
	mailer     Mailer
	baseURL    string
	log        *slog.Logger
	perms      map[string]string
}

var _ gen.Handler = (*Server)(nil)

// New создаёт HTTP-обработчик API с префиксом /api/v1.
func New(d Deps) (http.Handler, error) {
	ops, err := Operations(spec.Spec)
	if err != nil {
		return nil, err
	}
	s := &Server{
		identity: d.Identity, ingest: d.Ingest, layers: d.Layers, regulation: d.Regulation, routing: d.Routing, sessions: d.Sessions, mailer: d.Mailer,
		baseURL: strings.TrimRight(d.BaseURL, "/"), log: d.Log, perms: permissionIndex(ops),
	}
	opts := []gen.ServerOption{
		gen.WithPathPrefix("/api/v1"),
		gen.WithErrorHandler(s.errorHandler),
		gen.WithNotFound(s.notFound),
		gen.WithMethodNotAllowed(s.methodNotAllowed),
	}
	if d.Meters != nil {
		opts = append(opts, gen.WithMeterProvider(d.Meters))
	}
	if d.Tracers != nil {
		opts = append(opts, gen.WithTracerProvider(d.Tracers))
	}
	srv, err := gen.NewServer(s, s, opts...)
	if err != nil {
		return nil, fmt.Errorf("create api server: %w", err)
	}
	return srv, nil
}

func meta(ctx context.Context) identity.RequestMeta {
	c := httpx.ClientFrom(ctx)
	return identity.RequestMeta{IP: c.IP, UserAgent: c.UserAgent}
}

// --- Текущий пользователь ---

func (s *Server) GetMe(ctx context.Context) (*gen.Me, error) {
	p := principal(ctx)
	u, err := s.identity.GetUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return toMe(p, u), nil
}

func (s *Server) UpdateMe(ctx context.Context, req *gen.UpdateProfile) (*gen.Me, error) {
	p := principal(ctx)
	u, err := s.identity.UpdateProfile(ctx, p, p.UserID, req.Name, meta(ctx))
	if err != nil {
		return nil, err
	}
	p.Name = u.Name
	return toMe(p, u), nil
}

func (s *Server) GetSettings(ctx context.Context) (*gen.Settings, error) {
	st, err := s.identity.GetSettings(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return &gen.Settings{Theme: gen.SettingsTheme(st.Theme)}, nil
}

func (s *Server) UpdateSettings(ctx context.Context, req *gen.Settings) (*gen.Settings, error) {
	st, err := s.identity.UpdateSettings(ctx, principal(ctx).UserID, identity.Settings{Theme: string(req.Theme)})
	if err != nil {
		return nil, err
	}
	return &gen.Settings{Theme: gen.SettingsTheme(st.Theme)}, nil
}

func (s *Server) ChangePassword(ctx context.Context, req *gen.ChangePassword) error {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return err
	}
	if err := s.identity.ChangePassword(ctx, p.UserID, req.CurrentPassword, req.NewPassword, meta(ctx)); err != nil {
		return err
	}
	// Смена пароля завершает прочие сессии; текущая продолжается с новым временем входа.
	return s.sessions.Login(ctx, p.UserID)
}

func (s *Server) BeginTotpSetup(ctx context.Context) (*gen.TotpSetup, error) {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return nil, err
	}
	setup, err := s.identity.BeginTOTPSetup(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return &gen.TotpSetup{Secret: setup.Secret, URL: setup.URL}, nil
}

func (s *Server) ConfirmTotp(ctx context.Context, req *gen.TotpCode) (*gen.RecoveryCodes, error) {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return nil, err
	}
	codes, err := s.identity.ConfirmTOTP(ctx, p.UserID, req.Code, meta(ctx))
	if err != nil {
		return nil, err
	}
	return &gen.RecoveryCodes{Codes: codes}, nil
}

func (s *Server) DisableTotp(ctx context.Context, req *gen.PasswordConfirmation) error {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return err
	}
	if p.Has(identity.PermUsersManage) {
		return fmt.Errorf("%w", errAdminTOTPRequired)
	}
	if err := s.identity.CheckPassword(ctx, p.UserID, req.Password); err != nil {
		return err
	}
	return s.identity.DisableTOTP(ctx, &p, p.UserID, meta(ctx))
}

func (s *Server) ListPermissions(context.Context) ([]gen.PermissionInfo, error) {
	out := make([]gen.PermissionInfo, len(identity.Permissions))
	for i, p := range identity.Permissions {
		out[i] = gen.PermissionInfo{Code: string(p.Code), Title: p.Title}
	}
	return out, nil
}

// --- Токены API ---

func (s *Server) ListTokens(ctx context.Context) ([]gen.ApiToken, error) {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return nil, err
	}
	tokens, err := s.identity.ListAPITokens(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]gen.ApiToken, len(tokens))
	for i, t := range tokens {
		out[i] = toAPIToken(t)
	}
	return out, nil
}

func (s *Server) CreateToken(ctx context.Context, req *gen.CreateToken) (*gen.CreatedToken, error) {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return nil, err
	}
	scopes := make([]identity.Permission, len(req.Scopes))
	for i, sc := range req.Scopes {
		scopes[i] = identity.Permission(sc)
	}
	var expires *time.Time
	if req.ExpiresAt.Set {
		e := req.ExpiresAt.Value
		expires = &e
	}
	plain, t, err := s.identity.CreateAPIToken(ctx, p, req.Name, scopes, expires, meta(ctx))
	if err != nil {
		return nil, err
	}
	return &gen.CreatedToken{Token: toAPIToken(t), Value: plain}, nil
}

func (s *Server) RevokeToken(ctx context.Context, params gen.RevokeTokenParams) error {
	p := principal(ctx)
	if err := requireSession(p); err != nil {
		return err
	}
	return s.identity.RevokeAPIToken(ctx, p, params.TokenId, meta(ctx))
}

// --- Администрирование ---

func (s *Server) ListUsers(ctx context.Context) ([]gen.User, error) {
	users, err := s.identity.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.User, len(users))
	for i, u := range users {
		out[i] = toUser(u)
	}
	return out, nil
}

func (s *Server) CreateUser(ctx context.Context, req *gen.CreateUser) (*gen.User, error) {
	p := principal(ctx)
	u, err := s.identity.CreateUser(ctx, &p, identity.CreateUserInput{
		Email: req.Email, Name: req.Name, Password: req.Password, Roles: req.Roles,
	}, meta(ctx))
	if err != nil {
		return nil, err
	}
	out := toUser(u)
	return &out, nil
}

func (s *Server) GetUser(ctx context.Context, params gen.GetUserParams) (*gen.User, error) {
	u, err := s.identity.GetUser(ctx, params.UserId)
	if err != nil {
		return nil, err
	}
	out := toUser(u)
	return &out, nil
}

func (s *Server) UpdateUser(ctx context.Context, req *gen.UpdateUser, params gen.UpdateUserParams) (*gen.User, error) {
	p := principal(ctx)
	m := meta(ctx)
	if req.Name.Set {
		if _, err := s.identity.UpdateProfile(ctx, p, params.UserId, req.Name.Value, m); err != nil {
			return nil, err
		}
	}
	if req.Roles != nil {
		if err := s.identity.SetUserRoles(ctx, p, params.UserId, req.Roles, m); err != nil {
			return nil, err
		}
	}
	if req.Status.Set {
		if err := s.identity.SetUserStatus(ctx, p, params.UserId, string(req.Status.Value), m); err != nil {
			return nil, err
		}
	}
	return s.GetUser(ctx, gen.GetUserParams(params))
}

func (s *Server) ResetUserTotp(ctx context.Context, params gen.ResetUserTotpParams) error {
	p := principal(ctx)
	return s.identity.DisableTOTP(ctx, &p, params.UserId, meta(ctx))
}

func (s *Server) CreateInvite(ctx context.Context, req *gen.CreateInvite) (*gen.InviteCreated, error) {
	p := principal(ctx)
	token, err := s.identity.CreateInvite(ctx, p, req.Email, req.Role, meta(ctx))
	if err != nil {
		return nil, err
	}
	email, _ := identity.NormalizeEmail(req.Email)
	link := s.baseURL + "/invite/" + token
	msg := mail.Message{
		To:      email,
		Subject: "Приглашение в FreightFlow",
		Text: "Здравствуйте!\n\n" + p.Name + " приглашает вас в FreightFlow — систему мониторинга и анализа " +
			"логистической инфраструктуры Москвы.\n\nЧтобы создать учётную запись, перейдите по ссылке " +
			"(действует 72 часа):\n" + link + "\n",
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return nil, fmt.Errorf("send invite: %w", err)
	}
	return &gen.InviteCreated{Email: email, ExpiresInHours: 72}, nil
}

func (s *Server) ListRoles(ctx context.Context) ([]gen.Role, error) {
	roles, err := s.identity.ListRoles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Role, len(roles))
	for i, r := range roles {
		perms := make([]string, len(r.Permissions))
		for j, p := range r.Permissions {
			perms[j] = string(p)
		}
		out[i] = gen.Role{Code: r.Code, Name: r.Name, Description: r.Description, Builtin: r.Builtin, Permissions: perms}
	}
	return out, nil
}

func (s *Server) ListAudit(ctx context.Context, params gen.ListAuditParams) (*gen.AuditPage, error) {
	f := identity.AuditFilter{Limit: params.Limit.Or(50)}
	if params.ActorId.Set {
		id := params.ActorId.Value
		f.ActorID = &id
	}
	if params.Action.Set {
		f.Action = params.Action.Value
	}
	if params.Cursor.Set {
		c := params.Cursor.Value
		f.Before = &c
	}
	records, err := s.identity.ListAudit(ctx, f)
	if err != nil {
		return nil, err
	}
	page := &gen.AuditPage{Items: make([]gen.AuditRecord, len(records))}
	for i, r := range records {
		page.Items[i] = toAuditRecord(r)
	}
	if len(records) == f.Limit && len(records) > 0 {
		page.NextCursor = gen.NewOptUUID(records[len(records)-1].ID)
	}
	return page, nil
}

// --- Преобразования ---

func toMe(p identity.Principal, u identity.User) *gen.Me {
	perms := make([]string, len(p.Permissions))
	for i, perm := range p.Permissions {
		perms[i] = string(perm)
	}
	return &gen.Me{ID: u.ID, Email: u.Email, Name: u.Name, Roles: u.Roles, Permissions: perms, TotpEnabled: u.TOTPEnabled}
}

func toUser(u identity.User) gen.User {
	out := gen.User{
		ID: u.ID, Email: u.Email, Name: u.Name, Status: gen.UserStatus(u.Status),
		Roles: u.Roles, TotpEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt,
	}
	if out.Roles == nil {
		out.Roles = []string{}
	}
	if u.LastLoginAt != nil {
		out.LastLoginAt = gen.NewOptDateTime(*u.LastLoginAt)
	}
	return out
}

func toAPIToken(t identity.APIToken) gen.ApiToken {
	scopes := make([]string, len(t.Scopes))
	for i, s := range t.Scopes {
		scopes[i] = string(s)
	}
	out := gen.ApiToken{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: scopes, CreatedAt: t.CreatedAt}
	if t.ExpiresAt != nil {
		out.ExpiresAt = gen.NewOptDateTime(*t.ExpiresAt)
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = gen.NewOptDateTime(*t.LastUsedAt)
	}
	if t.RevokedAt != nil {
		out.RevokedAt = gen.NewOptDateTime(*t.RevokedAt)
	}
	return out
}

func toAuditRecord(r identity.AuditRecord) gen.AuditRecord {
	out := gen.AuditRecord{
		ID: r.ID, At: r.At, ActorLabel: r.ActorLabel, Action: r.Action,
		EntityType: r.EntityType, EntityId: r.EntityID, Details: gen.AuditRecordDetails{},
	}
	if r.ActorID != nil {
		out.ActorId = gen.NewOptUUID(*r.ActorID)
	}
	if r.IP != nil {
		out.IP = gen.NewOptString(r.IP.String())
	}
	if r.UserAgent != "" {
		out.UserAgent = gen.NewOptString(r.UserAgent)
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal(r.Details, &details); err == nil {
		for k, v := range details {
			out.Details[k] = jx.Raw(v)
		}
	}
	return out
}
