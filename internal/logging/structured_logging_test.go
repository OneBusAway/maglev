package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuredLogger(t *testing.T) {
	t.Run("creates JSON logger with proper configuration", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Log a test message
		logger.Info("test message",
			slog.String("component", "test"),
			slog.Int("count", 42))

		output := buf.String()

		// Verify JSON structure
		assert.Contains(t, output, `"level":"INFO"`)
		assert.Contains(t, output, `"msg":"test message"`)
		assert.Contains(t, output, `"component":"test"`)
		assert.Contains(t, output, `"count":42`)
		assert.Contains(t, output, `"time":`)
	})

	t.Run("respects log level configuration", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelWarn)

		// These should not appear
		logger.Debug("debug message")
		logger.Info("info message")

		// This should appear
		logger.Warn("warning message")

		output := buf.String()
		assert.NotContains(t, output, "debug message")
		assert.NotContains(t, output, "info message")
		assert.Contains(t, output, "warning message")
	})

	t.Run("handles error logging with context", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		err := assert.AnError
		logger.Error("operation failed",
			slog.String("operation", "database_query"),
			slog.String("error", err.Error()),
			slog.String("component", "gtfs_manager"))

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"operation failed"`)
		assert.Contains(t, output, `"operation":"database_query"`)
		assert.Contains(t, output, `"component":"gtfs_manager"`)
	})
}

func TestLoggerHelpers(t *testing.T) {
	t.Run("LogError creates structured error log", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		err := assert.AnError
		LogError(logger, "failed to fetch data", err,
			slog.String("url", "http://example.com"),
			slog.String("component", "http_client"))

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"failed to fetch data"`)
		assert.Contains(t, output, `"error":"assert.AnError general error for testing"`)
		assert.Contains(t, output, `"url":"http://example.com"`)
		assert.Contains(t, output, `"component":"http_client"`)
	})

	t.Run("LogOperation logs structured operation info", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelDebug)

		LogOperation(logger, "gtfs_data_imported",
			slog.String("source", "file.zip"),
			slog.Int("stops_count", 150),
			slog.Duration("duration", 0)) // Will be ignored if zero

		output := buf.String()
		assert.Contains(t, output, `"level":"DEBUG"`)
		assert.Contains(t, output, `"msg":"gtfs_data_imported"`)
		assert.Contains(t, output, `"source":"file.zip"`)
		assert.Contains(t, output, `"stops_count":150`)
	})

	t.Run("LogHTTPRequest logs request details", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		LogHTTPRequest(logger, "GET", "/api/where/stops", 200, 1.5,
			slog.String("user_agent", "test-client"))

		output := buf.String()
		assert.Contains(t, output, `"level":"INFO"`)
		assert.Contains(t, output, `"msg":"http_request"`)
		assert.Contains(t, output, `"method":"GET"`)
		assert.Contains(t, output, `"path":"/api/where/stops"`)
		assert.Contains(t, output, `"status":200`)
		assert.Contains(t, output, `"duration_ms":1.5`)
		assert.Contains(t, output, `"user_agent":"test-client"`)
	})
}

func TestContextLogger(t *testing.T) {
	t.Run("stores and retrieves logger from context", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		ctx := context.Background()
		ctx = WithLogger(ctx, logger)

		retrievedLogger := FromContext(ctx)
		require.NotNil(t, retrievedLogger)

		retrievedLogger.Info("test from context")

		output := buf.String()
		assert.Contains(t, output, "test from context")
	})

	t.Run("returns default logger when not in context", func(t *testing.T) {
		ctx := context.Background()
		logger := FromContext(ctx)

		// Should not panic and should return a usable logger
		require.NotNil(t, logger)
		logger.Info("test message") // Should not panic
	})
}
