package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

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
func LogError(logger *slog.Logger, message string, err error, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	
	args := make([]any, 0, len(attrs)+2)
	args = append(args, slog.String("error", err.Error()))
	
	for _, attr := range attrs {
		args = append(args, attr)
	}
	
	logger.Error(message, args...)
}

// LogOperation logs an operation with structured context
func LogOperation(logger *slog.Logger, operation string, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		// Skip zero-value durations
		if attr.Key == "duration" && attr.Value.Duration() == 0 {
			continue
		}
		args = append(args, attr)
	}
	
	logger.Info(operation, args...)
}

// LogHTTPRequest logs HTTP request details
func LogHTTPRequest(logger *slog.Logger, method, path string, status int, durationMs float64, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	
	args := make([]any, 0, len(attrs)+4)
	args = append(args,
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", status),
		slog.Float64("duration_ms", durationMs),
	)
	
	for _, attr := range attrs {
		args = append(args, attr)
	}
	
	logger.Info("http_request", args...)
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