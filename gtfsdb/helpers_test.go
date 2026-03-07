package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPerformDatabaseMigration_Idempotency(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	assert.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	ctx := context.Background()

	// 1. First run should succeed and create tables
	err = performDatabaseMigration(ctx, db)
	assert.NoError(t, err, "First migration should succeed")

	// 2. Second run should also succeed without error (idempotent IF NOT EXISTS clauses)
	err = performDatabaseMigration(ctx, db)
	assert.NoError(t, err, "Second migration should be idempotent and succeed")
}

func TestPerformDatabaseMigration_ErrorHandling(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	assert.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("failed to close database: %v", err)
		}
	})

	// This test mutates the package-level ddl variable.
	// Do NOT add t.Parallel() to this test or any test that calls performDatabaseMigration.
	originalDDL := ddl
	defer func() { ddl = originalDDL }()

	// Inject malformed SQL to simulate a corrupted migration file
	ddl = "CREATE TABLE valid_table (id INT); -- migrate\n THIS IS INVALID SQL;"

	ctx := context.Background()
	err = performDatabaseMigration(ctx, db)

	assert.Error(t, err, "Migration should fail on invalid SQL")
	assert.Contains(t, err.Error(), "error executing DDL statement", "Error should wrap the failing context")
}
