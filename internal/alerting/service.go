package alerting

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"freightflow/internal/platform/jobs"
)

// Service — правила и алерты.
type Service struct {
	pool *pgxpool.Pool
	jobs *jobs.Client
	log  *slog.Logger
}

// New создаёт сервис; client может быть nil в процессе без постановки задач (проверка правил
// после их изменения тогда пройдёт по расписанию).
func New(pool *pgxpool.Pool, client *jobs.Client, log *slog.Logger) *Service {
	return &Service{pool: pool, jobs: client, log: log}
}

// enqueueCheck ставит проверку правил в транзакции изменения.
func (s *Service) enqueueCheck(ctx context.Context, tx pgx.Tx) error {
	if s.jobs == nil {
		return nil
	}
	_, err := s.jobs.InsertTx(ctx, tx, CheckArgs{}, &river.InsertOpts{Queue: jobs.QueueAlerts})
	return err
}
