// Package logger настраивает структурное логирование.
//
// Всё пишется в stdout как поток событий — фактор XI. Ни файлов, ни ротации,
// ни путей к логам в конфиге: собирать и хранить — забота среды исполнения,
// а не приложения.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New возвращает логгер: JSON в проде, читаемый текст в разработке.
func New(level string, production bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if production {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
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
