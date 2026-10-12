// Package migrate применяет встроенные SQL-миграции приложения и миграции очереди River.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"freightflow/db/migrations"
)

// QueueSchema — схема таблиц River (создаётся миграцией приложения).
const QueueSchema = "queue"

// Migrator управляет версиями схемы базы данных.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
	river    *rivermigrate.Migrator[pgx.Tx]
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
	rm, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
		Schema: QueueSchema,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("create queue migrator: %w", err)
	}
	return &Migrator{db: sqlDB, provider: p, river: rm}, nil
}

// QueueVersion — последняя версия схемы River в подключённой библиотеке.
func QueueVersion() int {
	rm, err := rivermigrate.New[pgx.Tx](riverpgxv5.New(nil), nil)
	if err != nil {
		return 0
	}
	all := rm.AllVersions()
	return all[len(all)-1].Version
}

// Close освобождает соединение мигратора.
func (m *Migrator) Close() error { return m.db.Close() }

// Up применяет все новые миграции приложения, затем миграции очереди.
func (m *Migrator) Up(ctx context.Context, log *slog.Logger) error {
	results, err := m.provider.Up(ctx)
	for _, r := range results {
		log.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	res, err := m.river.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate queue: %w", err)
	}
	for _, v := range res.Versions {
		log.Info("queue migration applied", "version", v.Version, "name", v.Name, "duration", v.Duration)
	}
	return nil
}

// Down откатывает последнюю миграцию приложения. Схему очереди не трогает.
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
	have, want, err := m.queueVersions(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "очередь River: версия схемы %d из %d\n", have, want)
	return nil
}

// Pending сообщает, есть ли неприменённые миграции.
func (m *Migrator) Pending(ctx context.Context) (bool, error) {
	pending, err := m.provider.HasPending(ctx)
	if err != nil || pending {
		return pending, err
	}
	have, want, err := m.queueVersions(ctx)
	if err != nil {
		return false, err
	}
	return have < want, nil
}

func (m *Migrator) queueVersions(ctx context.Context) (have, want int, err error) {
	all := m.river.AllVersions()
	want = all[len(all)-1].Version
	// До первой миграции очереди таблицы версий ещё нет.
	var exists bool
	if err := m.db.QueryRowContext(ctx,
		`SELECT to_regclass($1) IS NOT NULL`, QueueSchema+".river_migration").Scan(&exists); err != nil {
		return 0, 0, fmt.Errorf("check queue schema: %w", err)
	}
	if !exists {
		return 0, want, nil
	}
	existing, err := m.river.ExistingVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("queue versions: %w", err)
	}
	if len(existing) > 0 {
		have = existing[len(existing)-1].Version
	}
	return have, want, nil
}
