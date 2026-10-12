// Package config загружает конфигурацию приложения из переменных окружения.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env/v11"
)

// Config — настройки приложения. Имена переменных окружения начинаются с FF_.
type Config struct {
	Env         string `env:"FF_ENV" envDefault:"dev"`
	HTTPAddr    string `env:"FF_HTTP_ADDR" envDefault:":8080"`
	BaseURL     string `env:"FF_BASE_URL" envDefault:"http://localhost:8080"`
	MetricsAddr string `env:"FF_METRICS_ADDR" envDefault:"127.0.0.1:9090"`
	DevViteURL  string `env:"FF_DEV_VITE_URL" envDefault:"http://localhost:5173"`

	DatabaseURL string `env:"FF_DATABASE_URL,required"`
	DataDir     string `env:"FF_DATA_DIR" envDefault:"./var"`

	SessionKey    string `env:"FF_SESSION_KEY"`
	EncryptionKey string `env:"FF_ENCRYPTION_KEY"`

	SMTP SMTP

	TelegramBotToken string `env:"FF_TELEGRAM_BOT_TOKEN"`
	MosDataAPIKey    string `env:"FF_MOSDATA_API_KEY"`
	RuCAFile         string `env:"FF_RU_CA_FILE"`

	ValhallaURL string `env:"FF_VALHALLA_URL" envDefault:"http://localhost:8002"`
	PDFCDPURL   string `env:"FF_PDF_CDP_URL" envDefault:"ws://localhost:9222"`

	LogLevel     string `env:"FF_LOG_LEVEL" envDefault:"info"`
	LogFormat    string `env:"FF_LOG_FORMAT" envDefault:"text"`
	OTLPEndpoint string `env:"FF_OTLP_ENDPOINT"`
}

// SMTP — параметры почтового сервера.
type SMTP struct {
	Host     string `env:"FF_SMTP_HOST" envDefault:"localhost"`
	Port     int    `env:"FF_SMTP_PORT" envDefault:"1025"`
	User     string `env:"FF_SMTP_USER"`
	Password string `env:"FF_SMTP_PASSWORD"`
	From     string `env:"FF_SMTP_FROM" envDefault:"FreightFlow <noreply@freightflow.local>"`
}

// IsDev сообщает, запущено ли приложение в режиме разработки.
func (c Config) IsDev() bool { return c.Env == "dev" }

// Load читает конфигурацию. Файл dotenvPath, если он существует, дополняет окружение:
// уже заданные переменные не перезаписываются.
func Load(dotenvPath string) (Config, error) {
	if dotenvPath != "" {
		if err := loadDotenv(dotenvPath); err != nil {
			return Config{}, err
		}
	}
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Env != "dev" && c.Env != "prod" {
		return fmt.Errorf("FF_ENV must be dev or prod, got %q", c.Env)
	}
	if !c.IsDev() {
		if len(c.SessionKey) < 64 || len(c.EncryptionKey) < 64 {
			return errors.New("FF_SESSION_KEY and FF_ENCRYPTION_KEY must be 32-byte hex values in prod")
		}
	}
	return nil
}

// loadDotenv переносит пары КЛЮЧ=ЗНАЧЕНИЕ из файла в окружение процесса.
// Поддерживаются комментарии (#) и значения в кавычках.
func loadDotenv(path string) error {
	f, err := os.Open(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return sc.Err()
}
