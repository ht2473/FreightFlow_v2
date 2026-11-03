// Package notify — доставка уведомлений: центр уведомлений в системе, почта (сразу или
// дайджестом), Telegram-бот, вебхуки с подписью; предпочтения пользователя и тихие часы.
// Уведомления создаёт постоянный потребитель событий алертов, доставки по внешним каналам —
// задачи очереди с повторами и журналом.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/alerting"
	"freightflow/internal/platform/jobs"
	"freightflow/internal/platform/mail"
	"freightflow/internal/platform/outbound"
	"freightflow/internal/platform/secret"
)

// Mailer отправляет письма.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Config — параметры каналов.
type Config struct {
	// BaseURL — адрес приложения для ссылок в письмах и сообщениях.
	BaseURL string
	// TelegramToken — токен бота; пусто — Telegram отключён.
	TelegramToken string
	// TelegramAPI — адрес Bot API (в тестах — подставной сервер).
	TelegramAPI string
	// AllowPrivateWebhooks разрешает вебхуки на адреса локальной сети и http (разработка, тесты).
	AllowPrivateWebhooks bool
}

// Service — уведомления и доставка.
type Service struct {
	pool     *pgxpool.Pool
	jobs     *jobs.Client
	box      *secret.Box
	mailer   Mailer
	alerts   *alerting.Service
	telegram *telegram
	webhooks *outbound.Client
	cfg      Config
	log      *slog.Logger
	now      func() time.Time
}

// New создаёт сервис. client может быть nil в процессе без постановки задач.
func New(ctx context.Context, pool *pgxpool.Pool, client *jobs.Client, box *secret.Box, mailer Mailer, alerts *alerting.Service, cfg Config, log *slog.Logger) (*Service, error) {
	wh, err := outbound.New(ctx, outbound.Config{
		AnyHost: true, NoRetry: true, AllowPrivate: cfg.AllowPrivateWebhooks, Timeout: 15 * time.Second,
		UserAgent: "FreightFlow-Webhook/1",
	})
	if err != nil {
		return nil, err
	}
	s := &Service{pool: pool, jobs: client, box: box, mailer: mailer, alerts: alerts, webhooks: wh, cfg: cfg, log: log, now: time.Now}
	if cfg.TelegramToken != "" {
		api := cfg.TelegramAPI
		if api == "" {
			api = "https://api.telegram.org"
		}
		if s.telegram, err = newTelegram(ctx, api, cfg.TelegramToken, cfg.AllowPrivateWebhooks); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Ошибки модуля.
var (
	ErrNotFound         = errors.New("notify: not found")
	ErrTelegramDisabled = errors.New("notify: telegram bot is not configured")
)

// ErrInvalid — неверные параметры; текст — для пользователя.
type ErrInvalid struct{ Reason string }

func (e *ErrInvalid) Error() string { return "notify: " + e.Reason }

func invalid(format string, a ...any) error { return &ErrInvalid{Reason: fmt.Sprintf(format, a...)} }

// Notification — уведомление в центре уведомлений.
type Notification struct {
	ID        uuid.UUID
	Kind      string
	Severity  string
	Title     string
	Body      string
	Link      *string
	AlertID   *uuid.UUID
	ReadAt    *time.Time
	CreatedAt time.Time
}

// moscow — часовой пояс тихих часов и дайджеста.
var moscow = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}()
