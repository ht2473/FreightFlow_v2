// Package app собирает зависимости приложения и выполняет подкоманды.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/config"
	"freightflow/internal/platform/db"
	fflog "freightflow/internal/platform/log"
	"freightflow/internal/platform/migrate"
)

// Version задаётся при сборке через -ldflags.
var Version = "dev"

// ErrUsage означает неверные аргументы командной строки.
var ErrUsage = errors.New("usage")

// Env — общие зависимости подкоманд.
type Env struct {
	Config config.Config
	Log    *slog.Logger
	Stdout io.Writer
	Stderr io.Writer
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, env *Env, args []string) error
}

func commands() []command {
	return []command{
		{"serve", "веб-сервер, API и тайлы [-worker]", runServe},
		{"worker", "фоновые задачи: загрузки, расчёты, уведомления, обслуживание", runWorker},
		{"etl", "загрузки данных: list | run [-force] <источник> | regions", runETL},
		{"migrate", "миграции базы данных: up | down | status", runMigrate},
		{"admin", "администрирование: create-user | reset-2fa", runAdmin},
		{"version", "версия сборки", runVersion},
	}
}

// Run разбирает аргументы и выполняет подкоманду.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("freightflow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	envFile := fs.String("env-file", ".env", "файл с переменными окружения")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr)
		return ErrUsage
	}

	var cmd *command
	for _, c := range commands() {
		if c.name == rest[0] {
			cmd = &c
			break
		}
	}
	if cmd == nil {
		fmt.Fprintf(stderr, "неизвестная команда %q\n\n", rest[0])
		usage(stderr)
		return ErrUsage
	}
	if cmd.name == "version" {
		return cmd.run(ctx, &Env{Stdout: stdout, Stderr: stderr}, rest[1:])
	}

	cfg, err := config.Load(*envFile)
	if err != nil {
		return err
	}
	env := &Env{
		Config: cfg,
		Log:    fflog.New(cfg.LogLevel, cfg.LogFormat).With("build", Version),
		Stdout: stdout,
		Stderr: stderr,
	}
	return cmd.run(ctx, env, rest[1:])
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Использование: freightflow [-env-file .env] <команда> [аргументы]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Команды:")
	for _, c := range commands() {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
}

func runVersion(_ context.Context, env *Env, _ []string) error {
	_, err := fmt.Fprintln(env.Stdout, Version)
	return err
}

func runMigrate(ctx context.Context, env *Env, args []string) error {
	if len(args) != 1 {
		fmt.Fprintln(env.Stderr, "Использование: freightflow migrate up | down | status")
		return ErrUsage
	}
	pool, err := db.Open(ctx, env.Config.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	m, err := migrate.New(pool)
	if err != nil {
		return err
	}
	defer m.Close()

	switch strings.ToLower(args[0]) {
	case "up":
		return m.Up(ctx, env.Log)
	case "down":
		return m.Down(ctx, env.Log)
	case "status":
		return m.Status(ctx, env.Stdout)
	default:
		fmt.Fprintln(env.Stderr, "Использование: freightflow migrate up | down | status")
		return ErrUsage
	}
}

// openDatabase подключается к базе и проверяет, что схема актуальна и расширения установлены.
func openDatabase(ctx context.Context, env *Env) (*pgxpool.Pool, error) {
	pool, err := db.Open(ctx, env.Config.DatabaseURL)
	if err != nil {
		return nil, err
	}
	m, err := migrate.New(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	defer m.Close()

	pending, err := m.Pending(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if pending {
		if !env.Config.IsDev() {
			pool.Close()
			return nil, errors.New("database schema is outdated: run `freightflow migrate up`")
		}
		env.Log.Info("applying pending migrations (dev)")
		if err := m.Up(ctx, env.Log); err != nil {
			pool.Close()
			return nil, err
		}
	}

	missing, err := db.MissingExtensions(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	if len(missing) > 0 {
		pool.Close()
		return nil, fmt.Errorf("missing database extensions: %s", strings.Join(missing, ", "))
	}
	return pool, nil
}

// Exit переводит ошибку подкоманды в код завершения процесса.
func Exit(err error) {
	switch {
	case err == nil:
		os.Exit(0)
	case errors.Is(err, ErrUsage):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}
