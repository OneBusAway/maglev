package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// Logger is an alias for slog.Logger to avoid direct slog imports in handlers
type Logger = slog.Logger

// loggerKey is used to store the logger in context
type loggerKey struct{}

// NewStructuredLogger creates a new structured logger with JSON output
func NewStructuredLogger(w io.Writer, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
	}
	handler := slog.NewJSONHandler(w, opts)
	return slog.New(handler)
}

// LogError logs an error with structured context
func LogError(logger *slog.Logger, message string, err error, args ...any) {
	if logger == nil {
		slog.Default().Error("nil logger provided to LogError", "message", message, "error", err)
		return
	}

	finalArgs := make([]any, 0, len(args)+2)
	finalArgs = append(finalArgs, "error", err.Error())
	finalArgs = append(finalArgs, args...)

	logger.Error(message, finalArgs...)
}

// LogOperation logs an operation with structured context
func LogOperation(logger *slog.Logger, operation string, args ...any) {
	if logger == nil {
		slog.Default().Info("nil logger provided to LogOperation", "operation", operation)
		return
	}

	logger.Info(operation, args...)
}

// LogHTTPRequest logs HTTP request details
func LogHTTPRequest(logger *slog.Logger, method, path string, status int, durationMs float64, args ...any) {
	if logger == nil {
		slog.Default().Info("nil logger provided to LogHTTPRequest", "path", path)
		return
	}

	finalArgs := make([]any, 0, len(args)+8)
	finalArgs = append(finalArgs,
		"method", method,
		"path", path,
		"status", status,
		"duration_ms", durationMs,
	)
	finalArgs = append(finalArgs, args...)

	logger.Info("http_request", finalArgs...)
}

// WithLogger adds a logger to the context
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext retrieves a logger from the context, or returns a default logger
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}

	// Return a default logger if none is found
	return slog.Default()
}

// ReplaceLogPrint replaces log.Print calls with structured logging
func ReplaceLogPrint(logger *slog.Logger, message string) {
	if logger == nil {
		slog.Default().Info("nil logger provided to ReplaceLogPrint", "message", message)
		return
	}
	logger.Info(message)
}

// ReplaceLogFatal replaces log.Fatal calls with error logging and returns an error
func ReplaceLogFatal(logger *slog.Logger, message string, err error) error {
	wrappedErr := fmt.Errorf("%s: %w", message, err)

	if logger != nil {
		LogError(logger, message, err)
	}

	return wrappedErr
}
