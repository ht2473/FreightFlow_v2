package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"freightflow/internal/identity/sqlc"
	"freightflow/internal/platform/ids"
)

// AuditEntry — событие для журнала аудита.
type AuditEntry struct {
	Action     string
	EntityType string
	EntityID   string
	Details    map[string]any
}

// AuditRecord — запись журнала аудита.
type AuditRecord struct {
	ID         uuid.UUID
	At         time.Time
	ActorID    *uuid.UUID
	ActorLabel string
	Action     string
	EntityType string
	EntityID   string
	IP         *netip.Addr
	UserAgent  string
	Details    json.RawMessage
}

// AuditFilter — отбор записей журнала; Before — курсор (id последней записи предыдущей страницы).
type AuditFilter struct {
	ActorID *uuid.UUID
	Action  string
	Before  *uuid.UUID
	Limit   int
}

// Audit записывает событие в журнал. Используется и другими модулями через свои интерфейсы.
func (s *Service) Audit(ctx context.Context, actor *Principal, e AuditEntry, meta RequestMeta) error {
	return s.audit(ctx, s.q, actor, e, meta)
}

func (s *Service) audit(ctx context.Context, q *sqlc.Queries, actor *Principal, e AuditEntry, meta RequestMeta) error {
	details := json.RawMessage(`{}`)
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		details = b
	}
	p := sqlc.InsertAuditParams{
		ID: ids.New(), Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID,
		UserAgent: truncate(meta.UserAgent, 512), Details: details,
	}
	if actor != nil {
		id := actor.UserID
		p.ActorID = &id
		p.ActorLabel = actor.Label()
	}
	if meta.IP.IsValid() {
		ip := meta.IP
		p.Ip = &ip
	}
	if err := q.InsertAudit(ctx, p); err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

// ListAudit возвращает записи журнала от новых к старым.
func (s *Service) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRecord, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var action *string
	if f.Action != "" {
		action = &f.Action
	}
	rows, err := s.q.ListAudit(ctx, sqlc.ListAuditParams{ActorID: f.ActorID, Action: action, BeforeID: f.Before, Lim: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	out := make([]AuditRecord, len(rows))
	for i, r := range rows {
		out[i] = AuditRecord{
			ID: r.ID, At: r.At, ActorID: r.ActorID, ActorLabel: r.ActorLabel, Action: r.Action,
			EntityType: r.EntityType, EntityID: r.EntityID, IP: r.Ip, UserAgent: r.UserAgent, Details: r.Details,
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
