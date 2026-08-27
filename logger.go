package gocamel

import (
	"log/slog"
	"os"
	"sync/atomic"
)

// defaultLogger is the package-level structured logger used by gocamel.
// Stored atomically so SetLogger can race with Log* from other goroutines.
var defaultLogger atomic.Pointer[slog.Logger]

func init() {
	defaultLogger.Store(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}

// SetLogger replaces the default logger with a custom slog.Logger.
// This is typically called at application startup to configure
// log format, level, and output.
//
// Example:
//
//	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
//	gocamel.SetLogger(logger)
func SetLogger(logger *slog.Logger) {
	if logger != nil {
		defaultLogger.Store(logger)
	}
}

// GetLogger returns the current slog.Logger used by gocamel.
func GetLogger() *slog.Logger {
	return defaultLogger.Load()
}

// LogDebug logs a debug message with optional attributes.
func LogDebug(msg string, args ...any) {
	defaultLogger.Load().Debug(msg, args...)
}

// LogInfo logs an info message with optional attributes.
func LogInfo(msg string, args ...any) {
	defaultLogger.Load().Info(msg, args...)
}

// LogWarn logs a warning message with optional attributes.
func LogWarn(msg string, args ...any) {
	defaultLogger.Load().Warn(msg, args...)
}

// LogError logs an error message with optional attributes.
func LogError(msg string, args ...any) {
	defaultLogger.Load().Error(msg, args...)
}
