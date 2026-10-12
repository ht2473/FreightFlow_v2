// Package dbtest выдаёт тестам изолированные базы данных PostgreSQL.
//
// Один раз на запуск создаётся шаблонная база с применёнными миграциями (имя зависит от содержимого
// миграций), каждый тест получает её копию через CREATE DATABASE … TEMPLATE — это быстро и не
// требует повторного применения миграций.
package dbtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/db/migrations"
	"freightflow/internal/platform/db"
	"freightflow/internal/platform/migrate"
)

// DefaultURL — сервер базы данных разработки (docker compose, deploy/compose.dev.yaml).
const DefaultURL = "postgres://freightflow:freightflow@localhost:55442/postgres?sslmode=disable" //nolint:gosec // учётные данные локальной базы разработки

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// New возвращает пул соединений к новой пустой базе со схемой приложения.
// Если сервер недоступен и FF_TEST_REQUIRE_DB не задан, тест пропускается.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	adminURL := serverURL()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		if os.Getenv("FF_TEST_REQUIRE_DB") != "" {
			t.Fatalf("connect to test database server: %v", err)
		}
		t.Skipf("PostgreSQL недоступен (%v): запустите `task up`", err)
	}
	defer admin.Close(ctx)

	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(ctx, admin, adminURL) })
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	name := "ffv2_t_" + randomSuffix()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, templateName)); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	pool, err := db.Open(ctx, withDatabase(adminURL, name))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(cctx, adminURL)
		if err != nil {
			t.Logf("drop test database %s: %v", name, err)
			return
		}
		defer conn.Close(cctx)
		if _, err := conn.Exec(cctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})
	return pool
}

func serverURL() string {
	if u := os.Getenv("FF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return DefaultURL
}

// ensureTemplate создаёт шаблонную базу для текущего набора миграций, если её ещё нет.
func ensureTemplate(ctx context.Context, admin *pgx.Conn, adminURL string) (string, error) {
	hash, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := "ffv2_tpl_" + hash[:12]

	// Блокировка не даёт параллельным пакетам тестов создавать шаблон одновременно.
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(7710001)`); err != nil {
		return "", fmt.Errorf("lock: %w", err)
	}
	defer func() { _, _ = admin.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(7710001)`) }()

	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return "", fmt.Errorf("check template: %w", err)
	}
	if exists {
		return name, nil
	}

	building := name + "_build"
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, building)); err != nil {
		return "", fmt.Errorf("drop stale template: %w", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, building)); err != nil {
		return "", fmt.Errorf("create template: %w", err)
	}
	pool, err := db.Open(ctx, withDatabase(adminURL, building))
	if err != nil {
		return "", err
	}
	m, err := migrate.New(pool)
	if err != nil {
		pool.Close()
		return "", err
	}
	err = m.Up(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_ = m.Close()
	pool.Close()
	if err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`ALTER DATABASE %s RENAME TO %s`, building, name)); err != nil {
		return "", fmt.Errorf("finalize template: %w", err)
	}
	return name, nil
}

func migrationsHash() (string, error) {
	var names []string
	if err := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, path)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			return "", fmt.Errorf("read migration %s: %w", n, err)
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func withDatabase(rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Path = "/" + name
	return u.String()
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
