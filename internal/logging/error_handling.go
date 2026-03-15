package logging

import (
	"fmt"
	"io"
	"log/slog"
)

// SafeCloseWithLogging closes a resource and logs any errors that occur.
//
// This utility is designed to be used in defer statements to ensure resources
// are properly closed even if errors occur during the close operation.
//
// Example usage:
//
//	resp, err := http.Get(url)
//	if err != nil {
//	    return err
//	}
//	defer SafeCloseWithLogging(resp.Body, logger, "http_response_body")
//
//	// Or with database rows:
//	rows, err := db.Query("SELECT * FROM table")
//	if err != nil {
//	    return err
//	}
//	defer SafeCloseWithLogging(rows, logger, "database_rows")
func SafeCloseWithLogging(closer io.Closer, logger *slog.Logger, operation string) {
	if closer == nil {
		return
	}

	if err := closer.Close(); err != nil {
		LogError(logger, "failed to close resource", err,
			"operation", operation,
			"component", "resource_management")
	}
}

// SafeRollbackWithLogging rolls back a transaction and logs any errors that occur.
// It ignores "already committed/rolled back" errors as these are expected when using defer.
//
// This utility is designed to be used in defer statements to ensure transactions
// are properly rolled back if they haven't been committed, without generating
// noise for the common "already committed" scenario.
//
// Example usage:
//
//	tx, err := db.Begin()
//	if err != nil {
//	    return err
//	}
//	defer SafeRollbackWithLogging(tx, logger, "user_creation")
//
//	// Perform database operations...
//	if err := doSomething(tx); err != nil {
//	    return err // Transaction will be rolled back by defer
//	}
//
//	return tx.Commit() // Successful commit, defer rollback will be ignored
func SafeRollbackWithLogging(tx interface{ Rollback() error }, logger *slog.Logger, operation string) {
	if tx == nil {
		return
	}

	if err := tx.Rollback(); err != nil {
		// Ignore the common "already committed or rolled back" error
		// This happens when defer rollback is called after successful commit
		errStr := err.Error()
		if errStr == "sql: transaction has already been committed or rolled back" {
			return
		}

		LogError(logger, "failed to rollback transaction", err,
			"operation", operation,
			"component", "database")
	}
}

// HandleDeferredError handles errors from deferred operations and modifies the original error
// to include deferred operation failures.
//
// This utility is designed for functions that need to handle cleanup operations
// in defer statements while preserving the original function's error semantics.
// The original error takes precedence, but deferred errors are logged.
//
// Example usage:
//
//	func processData() (err error) {
//	    file, err := os.Open("data.txt")
//	    if err != nil {
//	        return err
//	    }
//	    defer HandleDeferredError(&err, file.Close, logger, "file_close")
//
//	    // Process file...
//	    return nil // Any file.Close() error will be logged and set as err if no other error
//	}
func HandleDeferredError(originalErr *error, deferredOp func() error, logger *slog.Logger, operation string) {
	if deferredOp == nil {
		return
	}

	if err := deferredOp(); err != nil {
		// Log the deferred error
		LogError(logger, "deferred operation failed", err,
			"operation", operation,
			"component", "deferred_cleanup")

		// If there was no original error, set this as the error
		if *originalErr == nil {
			*originalErr = fmt.Errorf("deferred %s failed: %w", operation, err)
		}
		// If there was an original error, we keep it but log the deferred error
		// The original error takes precedence as it's usually more important
	}
}
