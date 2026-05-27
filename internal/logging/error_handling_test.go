package logging

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeClose(t *testing.T) {
	t.Run("closes response body safely with error logging", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Create a test server that returns a response
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("test response"))
		}))
		defer server.Close()

		// Make a request
		resp, err := http.Get(server.URL)
		require.NoError(t, err)

		// Use safe close
		SafeCloseWithLogging(resp.Body, logger, "test_operation")

		// Check that no error was logged (successful close)
		output := buf.String()
		if output != "" {
			assert.NotContains(t, output, `"level":"ERROR"`)
		}
	})

	t.Run("logs error when close fails", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Create a closer that always returns an error
		errorCloser := &errorCloser{err: assert.AnError}

		SafeCloseWithLogging(errorCloser, logger, "test_operation")

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"failed to close resource"`)
		assert.Contains(t, output, `"operation":"test_operation"`)
	})
}

func TestSafeRollback(t *testing.T) {
	t.Run("handles rollback errors gracefully", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Create a mock transaction that fails on rollback
		mockTx := &mockTransaction{rollbackErr: assert.AnError}

		SafeRollbackWithLogging(mockTx, logger, "test_operation")

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"failed to rollback transaction"`)
		assert.Contains(t, output, `"operation":"test_operation"`)
	})

	t.Run("ignores already committed/rolled back errors", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Create a mock transaction that returns the expected error
		mockTx := &mockTransaction{rollbackErr: &CommittedError{}}

		SafeRollbackWithLogging(mockTx, logger, "test_operation")

		// Should not log anything for this expected error
		output := buf.String()
		assert.Empty(t, output)
	})

	t.Run("handles successful rollback silently", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		// Create a mock transaction that succeeds on rollback
		mockTx := &mockTransaction{rollbackErr: nil}

		SafeRollbackWithLogging(mockTx, logger, "test_operation")

		// Should not log anything for successful rollback
		output := buf.String()
		assert.Empty(t, output)
	})
}

func TestHandleDeferredError(t *testing.T) {
	t.Run("handles deferred errors in return statements", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		testFunc := func() (err error) {
			defer HandleDeferredError(&err, func() error {
				return assert.AnError
			}, logger, "cleanup_operation")

			return nil // Original function succeeds
		}

		err := testFunc()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deferred cleanup_operation")

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"deferred operation failed"`)
	})

	t.Run("preserves original error when deferred operation also fails", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewStructuredLogger(&buf, slog.LevelInfo)

		originalError := assert.AnError
		deferredError := assert.AnError

		testFunc := func() (err error) {
			defer HandleDeferredError(&err, func() error {
				return deferredError
			}, logger, "cleanup_operation")

			return originalError // Original function fails
		}

		err := testFunc()
		assert.Error(t, err)
		// Should still return the original error
		assert.Contains(t, err.Error(), originalError.Error())

		output := buf.String()
		assert.Contains(t, output, `"level":"ERROR"`)
		assert.Contains(t, output, `"msg":"deferred operation failed"`)
	})
}

// Mock types for testing
type errorCloser struct {
	err error
}

type CommittedError struct{}

func (e *CommittedError) Error() string {
	return "sql: transaction has already been committed or rolled back"
}

func (e *errorCloser) Close() error {
	return e.err
}

type mockTransaction struct {
	rollbackErr error
}

func (m *mockTransaction) Rollback() error {
	return m.rollbackErr
}

func (m *mockTransaction) Commit() error {
	return nil
}

func (m *mockTransaction) Exec(query string, args ...any) (sql.Result, error) {
	return nil, nil
}

func (m *mockTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return nil, nil
}

func (m *mockTransaction) Prepare(query string) (*sql.Stmt, error) {
	return nil, nil
}

func (m *mockTransaction) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return nil, nil
}

func (m *mockTransaction) Query(query string, args ...any) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockTransaction) QueryRow(query string, args ...any) *sql.Row {
	return nil
}

func (m *mockTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return nil
}

// Helper functions are now implemented in error_handling.go
