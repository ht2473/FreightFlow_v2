// Package migrate применяет встроенные SQL-миграции.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"freightflow/db/migrations"
)

// Migrator управляет версиями схемы базы данных.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// New создаёт мигратор поверх пула соединений.
func New(pool *pgxpool.Pool) (*Migrator, error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return &Migrator{db: sqlDB, provider: p}, nil
}

// Close освобождает соединение мигратора.
func (m *Migrator) Close() error { return m.db.Close() }

// Up применяет все новые миграции.
func (m *Migrator) Up(ctx context.Context, log *slog.Logger) error {
	results, err := m.provider.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down откатывает последнюю миграцию.
func (m *Migrator) Down(ctx context.Context, log *slog.Logger) error {
	r, err := m.provider.Down(ctx)
	if r != nil {
		log.Info("migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	}
	if err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Status печатает состояние миграций.
func (m *Migrator) Status(ctx context.Context, w io.Writer) error {
	statuses, err := m.provider.Status(ctx)
	if err != nil {
		return fmt.Errorf("migration status: %w", err)
	}
	for _, s := range statuses {
		applied := "ожидает"
		if s.State == goose.StateApplied {
			applied = "применена " + s.AppliedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%d  %-60s  %s\n", s.Source.Version, s.Source.Path, applied)
	}
	return nil
}

// Pending сообщает, есть ли неприменённые миграции.
func (m *Migrator) Pending(ctx context.Context) (bool, error) {
	return m.provider.HasPending(ctx)
}
