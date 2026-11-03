package api

import (
	"context"
	"errors"
	"net/http"

	"freightflow/internal/api/gen"
	"freightflow/internal/identity"
	"freightflow/internal/notify"
)

func (s *Server) ListNotifications(ctx context.Context, params gen.ListNotificationsParams) (*gen.NotificationPage, error) {
	page, err := s.notify.List(ctx, principal(ctx).UserID, params.Unread.Or(false), params.Limit.Or(20), params.Cursor.Or(""))
	if err != nil {
		return nil, err
	}
	out := &gen.NotificationPage{Unread: page.Unread, Items: make([]gen.Notification, len(page.Items))}
	for i, n := range page.Items {
		g := gen.Notification{
			ID: n.ID, Kind: gen.NotificationKind(n.Kind), Severity: gen.AlertSeverity(n.Severity), Title: n.Title, Body: n.Body,
			CreatedAt: n.CreatedAt,
		}
		if n.Link != nil {
			g.Link = gen.NewOptString(*n.Link)
		}
		if n.AlertID != nil {
			g.AlertId = gen.NewOptUUID(*n.AlertID)
		}
		if n.ReadAt != nil {
			g.ReadAt = gen.NewOptDateTime(*n.ReadAt)
		}
		out.Items[i] = g
	}
	if page.Next != "" {
		out.Next = gen.NewOptString(page.Next)
	}
	return out, nil
}

func (s *Server) MarkNotificationsRead(ctx context.Context, req *gen.NotificationsRead) error {
	return s.notify.MarkRead(ctx, principal(ctx).UserID, req.Ids, req.All.Or(false))
}

func toSettings(p notify.Preferences) *gen.NotificationSettings {
	out := &gen.NotificationSettings{
		EmailMode: gen.NotificationSettingsEmailMode(p.EmailMode), DigestHour: p.DigestHour,
		MinSeverity: gen.AlertSeverity(p.MinSeverity), SubscribeAll: p.SubscribeAll,
		SubscribeSeverity: gen.AlertSeverity(p.SubscribeSeverity),
	}
	if p.QuietFrom != nil && p.QuietTo != nil {
		out.QuietFrom, out.QuietTo = gen.NewOptInt(*p.QuietFrom), gen.NewOptInt(*p.QuietTo)
	}
	return out
}

func (s *Server) GetNotificationSettings(ctx context.Context) (*gen.NotificationSettings, error) {
	p, err := s.notify.Preferences(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return toSettings(p), nil
}

func (s *Server) UpdateNotificationSettings(ctx context.Context, req *gen.NotificationSettings) (*gen.NotificationSettings, error) {
	p := notify.Preferences{
		EmailMode: string(req.EmailMode), DigestHour: req.DigestHour, MinSeverity: string(req.MinSeverity),
		SubscribeAll: req.SubscribeAll, SubscribeSeverity: string(req.SubscribeSeverity),
	}
	if v, ok := req.QuietFrom.Get(); ok {
		p.QuietFrom = &v
	}
	if v, ok := req.QuietTo.Get(); ok {
		p.QuietTo = &v
	}
	pr := principal(ctx)
	saved, err := s.notify.SetPreferences(ctx, pr.UserID, pr.Has(identity.PermAlertsManage), p)
	if err != nil {
		return nil, err
	}
	return toSettings(saved), nil
}

func (s *Server) GetTelegram(ctx context.Context) (*gen.TelegramStatus, error) {
	st, err := s.notify.TelegramStatus(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := &gen.TelegramStatus{Configured: st.Configured, Bound: st.Bound}
	if st.Username != nil {
		out.Username = gen.NewOptString(*st.Username)
	}
	if st.BoundAt != nil {
		out.BoundAt = gen.NewOptDateTime(*st.BoundAt)
	}
	return out, nil
}

func (s *Server) LinkTelegram(ctx context.Context) (*gen.TelegramLink, error) {
	l, err := s.notify.LinkTelegram(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	return &gen.TelegramLink{Code: l.Code, URL: l.URL, ExpiresAt: l.ExpiresAt}, nil
}

func (s *Server) UnlinkTelegram(ctx context.Context) error {
	return s.notify.UnlinkTelegram(ctx, principal(ctx).UserID)
}

func toWebhook(w notify.Webhook) gen.Webhook {
	out := gen.Webhook{ID: w.ID, Name: w.Name, URL: w.URL, Enabled: w.Enabled, CreatedAt: w.CreatedAt}
	if w.LastStatus != nil {
		out.LastStatus = gen.NewOptWebhookLastStatus(gen.WebhookLastStatus(*w.LastStatus))
	}
	if w.LastAt != nil {
		out.LastAt = gen.NewOptDateTime(*w.LastAt)
	}
	return out
}

func toDelivery(d notify.Delivery) gen.WebhookDelivery {
	out := gen.WebhookDelivery{
		ID: d.ID, Event: d.Event, Status: gen.WebhookDeliveryStatus(d.Status), Attempts: d.Attempts,
		CreatedAt: d.CreatedAt,
	}
	if d.ResponseCode != nil {
		out.ResponseCode = gen.NewOptInt(*d.ResponseCode)
	}
	if d.LastError != nil {
		out.LastError = gen.NewOptString(*d.LastError)
	}
	if d.SentAt != nil {
		out.SentAt = gen.NewOptDateTime(*d.SentAt)
	}
	return out
}

func (s *Server) ListWebhooks(ctx context.Context) ([]gen.Webhook, error) {
	list, err := s.notify.Webhooks(ctx, principal(ctx).UserID)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Webhook, len(list))
	for i, w := range list {
		out[i] = toWebhook(w)
	}
	return out, nil
}

func (s *Server) CreateWebhook(ctx context.Context, req *gen.WebhookInput) (*gen.WebhookCreated, error) {
	p := principal(ctx)
	w, plain, err := s.notify.CreateWebhook(ctx, p.UserID, req.Name, req.URL)
	if err != nil {
		return nil, err
	}
	if err := s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "webhook.created", EntityType: "webhook", EntityID: w.ID.String(), Details: map[string]any{"url": w.URL},
	}, meta(ctx)); err != nil {
		return nil, err
	}
	return &gen.WebhookCreated{Webhook: toWebhook(*w), Secret: plain}, nil
}

func (s *Server) UpdateWebhook(ctx context.Context, req *gen.WebhookUpdate, params gen.UpdateWebhookParams) error {
	return s.notify.SetWebhookEnabled(ctx, principal(ctx).UserID, params.WebhookId, req.Enabled)
}

func (s *Server) DeleteWebhook(ctx context.Context, params gen.DeleteWebhookParams) error {
	p := principal(ctx)
	if err := s.notify.DeleteWebhook(ctx, p.UserID, params.WebhookId); err != nil {
		return err
	}
	return s.identity.Audit(ctx, &p, identity.AuditEntry{
		Action: "webhook.deleted", EntityType: "webhook", EntityID: params.WebhookId.String(),
	}, meta(ctx))
}

func (s *Server) TestWebhook(ctx context.Context, params gen.TestWebhookParams) (*gen.WebhookDelivery, error) {
	d, err := s.notify.TestWebhook(ctx, principal(ctx).UserID, params.WebhookId)
	if err != nil {
		return nil, err
	}
	out := toDelivery(*d)
	return &out, nil
}

func (s *Server) ListWebhookDeliveries(ctx context.Context, params gen.ListWebhookDeliveriesParams) ([]gen.WebhookDelivery, error) {
	list, err := s.notify.Deliveries(ctx, principal(ctx).UserID, params.WebhookId, 50)
	if err != nil {
		return nil, err
	}
	out := make([]gen.WebhookDelivery, len(list))
	for i, d := range list {
		out[i] = toDelivery(d)
	}
	return out, nil
}

// notifyProblem переводит ошибки модуля уведомлений в ответы API.
func notifyProblem(err error) (status int, code, title string, ok bool) {
	var inv *notify.ErrInvalid
	switch {
	case errors.As(err, &inv):
		return http.StatusUnprocessableEntity, "invalid_notification", inv.Reason, true
	case errors.Is(err, notify.ErrNotFound):
		return http.StatusNotFound, "not_found", "Не найдено", true
	case errors.Is(err, notify.ErrTelegramDisabled):
		return http.StatusConflict, "telegram_disabled", "Telegram-бот не настроен на сервере", true
	}
	return 0, "", "", false
}
