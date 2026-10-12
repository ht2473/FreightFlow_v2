// Package ingest — каркас загрузок: реестр источников, запуски, сырые снимки, проверки
// качества и публикация версий данных (03-architecture.md §6).
//
// Запуск: Fetch сохраняет сырьё в хранилище снимков → снимок с тем же SHA-256, что и
// последний загруженный, не загружается повторно → Load переносит данные в домен →
// версии доменов растут, отправляется событие data.published.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"freightflow/internal/platform/blob"
	"freightflow/internal/platform/outbound"
)

// Descriptor — описание источника в коде. Таблица ingest.source — его зеркало и настройки.
type Descriptor struct {
	Code        string
	Title       string
	Publisher   string
	URL         string
	License     string
	Attribution string
	// Domains — домены данных, версии которых растут после успешной загрузки.
	Domains []string
	// Interval — период автоматической загрузки; 0 — только вручную.
	Interval time.Duration
	// Hosts — разрешённые хосты загрузчика; пусто — источник не ходит в сеть.
	Hosts []string
	// RuNetwork — российский государственный ресурс: прямое соединение и сертификат Минцифры.
	RuNetwork bool
	// Secrets — обязательные настройки (имена переменных окружения).
	Secrets []string
	// Queue — очередь задач; пусто — общая очередь загрузок.
	Queue string
	// Tools — внешние программы загрузчика (osm2pgsql, osmium); есть только в образе worker.
	Tools []string
	// Timeout — предел всего запуска; 0 — 2 часа.
	Timeout time.Duration
	// RequestTimeout — предел одного HTTP-запроса вместе с телом; 0 — 60 секунд.
	// Источникам с большими файлами нужен запас на всю загрузку.
	RequestTimeout time.Duration
}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Source — загрузчик источника.
type Source interface {
	Descriptor() Descriptor
	// Fetch получает сырьё и сохраняет его через env.Store. Если источник сообщает, что
	// данные не менялись (условный запрос), возвращает Fetched{NotModified: true}.
	Fetch(ctx context.Context, env FetchEnv) (Fetched, error)
	// Load переносит снимок в домен. Критические проверки качества (env.Check) должны
	// прерывать загрузку до фиксации изменений: при ошибке домен остаётся прежним.
	Load(ctx context.Context, env LoadEnv) (LoadStats, error)
}

// Snapshot — сохранённый сырой снимок.
type Snapshot struct {
	ID          uuid.UUID
	Path        string
	SHA256      string
	Size        int64
	Format      string
	Version     string
	RecordCount *int64
	Validators  outbound.Validators
	FetchedAt   time.Time
}

// RunLog — журнал запуска, видимый в интерфейсе.
type RunLog interface {
	Info(ctx context.Context, msg string)
	Warn(ctx context.Context, msg string)
}

// FetchEnv — окружение шага получения данных.
type FetchEnv struct {
	// HTTP — клиент с белым списком хостов источника; nil, если хостов нет.
	HTTP *outbound.Client
	// Previous — последний загруженный снимок (валидаторы для условного запроса), если есть.
	Previous *Snapshot
	// Stored возвращает сохранённый снимок этой версии набора, если его файл на месте
	// (например, получен запуском, который не дошёл до загрузки): повторно качать не нужно.
	Stored func(ctx context.Context, version string) (*Snapshot, error)
	// Secret возвращает значение обязательной настройки.
	Secret func(name string) string
	// Store сохраняет сырьё в каталог снимков источника.
	Store func(ctx context.Context, ext string, r io.Reader) (blob.Object, error)
	// TempDir — рабочий каталог запуска; удаляется после завершения.
	TempDir string
	// WorkDir — постоянный каталог источника между запусками (недокачанные файлы).
	WorkDir string
	Log     RunLog
}

// Fetched — результат получения данных.
type Fetched struct {
	NotModified bool
	Object      blob.Object
	Format      string
	// Version — версия набора у источника (номер, дата, ETag).
	Version     string
	RecordCount *int64
	Validators  outbound.Validators
	// Schema — описание полей набора: изменение схемы отмечается проверкой.
	Schema json.RawMessage
}

// LoadEnv — окружение шага загрузки.
type LoadEnv struct {
	Pool     *pgxpool.Pool
	Blobs    *blob.Store
	Snapshot Snapshot
	RunID    uuid.UUID
	TempDir  string
	Log      RunLog
	// Check записывает результат проверки качества.
	Check func(ctx context.Context, c Check)
}

// LoadStats — итоговые счётчики загрузки (записей добавлено, обновлено и т. п.).
type LoadStats map[string]int64

// Severity — важность проверки качества.
type Severity string

// Важность проверок: критическая останавливает загрузку.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Check — результат проверки качества.
type Check struct {
	Code     string
	Title    string
	Severity Severity
	Passed   bool
	Observed any
	Expected any
	Message  string
}

// Trigger — что запустило загрузку.
type Trigger string

// Виды запуска.
const (
	TriggerSchedule Trigger = "schedule"
	TriggerManual   Trigger = "manual"
	TriggerCLI      Trigger = "cli"
)

// Состояния запуска.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusUnchanged = "unchanged"
	StatusFailed    = "failed"
)

// Ошибки модуля.
var (
	ErrUnknownSource = errors.New("unknown data source")
	ErrRunActive     = errors.New("data source already has an active run")
	ErrQualityFailed = errors.New("critical quality check failed")
	ErrMissingSecret = errors.New("required setting is not configured")
	ErrMissingTool   = errors.New("required program is not installed (run the source in the worker container)")
	ErrRunNotFound   = errors.New("run not found")
)
