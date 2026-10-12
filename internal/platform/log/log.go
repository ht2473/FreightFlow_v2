// Package log настраивает структурированный журнал приложения.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

// New создаёт журнал: формат json для серверов, text — цветной вывод для разработки.
func New(level, format string) *slog.Logger {
	return NewWriter(os.Stdout, level, format)
}

// NewWriter создаёт журнал с выводом в w.
func NewWriter(w io.Writer, level, format string) *slog.Logger {
	lvl := parseLevel(level)
	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	} else {
		h = tint.NewTextHandler(w, &tint.Options{Level: lvl, TimeFormat: time.TimeOnly})
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
