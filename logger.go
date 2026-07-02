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

// LogDebug logs a debug message with optional attributes.
func LogDebug(msg string, args ...any) {
	defaultLogger.Debug(msg, args...)
}

// LogInfo logs an info message with optional attributes.
func LogInfo(msg string, args ...any) {
	defaultLogger.Info(msg, args...)
}

// LogWarn logs a warning message with optional attributes.
func LogWarn(msg string, args ...any) {
	defaultLogger.Warn(msg, args...)
}

// LogError logs an error message with optional attributes.
func LogError(msg string, args ...any) {
	defaultLogger.Error(msg, args...)
}
