package gocamel

import (
	"log/slog"
	"os"
)

// defaultLogger is the package-level structured logger used by gocamel.
// It can be overridden with SetLogger() for custom configuration.
var defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

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
		defaultLogger = logger
	}
}

// GetLogger returns the current slog.Logger used by gocamel.
func GetLogger() *slog.Logger {
	return defaultLogger
}

// logDebug logs a debug message with optional attributes.
func logDebug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// logInfo logs an info message with optional attributes.
func logInfo(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// logWarn logs a warning message with optional attributes.
func logWarn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// logError logs an error message with optional attributes.
func logError(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}
