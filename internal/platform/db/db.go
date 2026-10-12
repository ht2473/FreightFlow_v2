// Package db управляет подключением к PostgreSQL и транзакциями.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RequiredExtensions — расширения, без которых приложение не работает.
var RequiredExtensions = []string{"postgis", "h3", "h3_postgis", "pg_trgm", "unaccent", "btree_gist"}

// Open создаёт пул соединений и проверяет доступность базы.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = "freightflow"
	cfg.ConnConfig.RuntimeParams["timezone"] = "Europe/Moscow"
	// Трассы и метрики запросов берут глобальные провайдеры OpenTelemetry (platform/telemetry).
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithSpanNameCtxFunc(queryName),
		otelpgx.WithMetricOperationName(queryName),
		otelpgx.WithDisableSQLStatementInAttributes(),
	)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := otelpgx.RecordStats(pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pool metrics: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// queryName даёт короткое имя запроса для трасс и метрик: имя запроса sqlc
// («-- name: GetUserByID :one») или вид операции (SELECT, UPDATE…).
func queryName(ctx context.Context, sql string) string {
	if rest, ok := strings.CutPrefix(sql, "-- name: "); ok {
		if name, _, ok := strings.Cut(rest, " "); ok {
			return name
		}
	}
	return otelpgx.SQLOperationName(ctx, sql)
}

// MissingExtensions возвращает обязательные расширения, которые не установлены в базе.
func MissingExtensions(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT extname FROM pg_extension`)
	if err != nil {
		return nil, fmt.Errorf("list extensions: %w", err)
	}
	installed, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read extensions: %w", err)
	}
	have := make(map[string]bool, len(installed))
	for _, e := range installed {
		have[e] = true
	}
	var missing []string
	for _, e := range RequiredExtensions {
		if !have[e] {
			missing = append(missing, e)
		}
	}
	return missing, nil
}

// InTx выполняет fn в транзакции: фиксирует при успехе, откатывает при ошибке или панике.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
