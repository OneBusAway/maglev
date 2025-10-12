package gtfsdb

import "maglev.onebusaway.org/internal/appconf"

const (
	// DefaultBulkInsertBatchSize is the default batch size for multi-row INSERTs.
	// SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999, so we use 1000 records
	// with 10 fields per record = ~10,000 variables per batch.
	DefaultBulkInsertBatchSize = 1000
)

// Config holds configuration options for the Client
type Config struct {
	// Database configuration
	DBPath  string              // Path to SQLite database file
	Env     appconf.Environment // Environment name: development, test, production.
	verbose bool                // Enable verbose logging

	// Performance tuning
	// BulkInsertBatchSize controls how many records are inserted per multi-row INSERT statement.
	// Default is 1000. Larger values can improve performance but may hit SQLite's
	// SQLITE_MAX_VARIABLE_NUMBER limit (default 999).
	// Set to 0 to use the default value.
	BulkInsertBatchSize int
}

func NewConfig(dbPath string, env appconf.Environment, verbose bool) Config {
	return Config{
		DBPath:              dbPath,
		Env:                 env,
		verbose:             verbose,
		BulkInsertBatchSize: DefaultBulkInsertBatchSize,
	}
}

// GetBulkInsertBatchSize returns the configured batch size, or the default if not set
func (c Config) GetBulkInsertBatchSize() int {
	if c.BulkInsertBatchSize <= 0 {
		return DefaultBulkInsertBatchSize
	}
	return c.BulkInsertBatchSize
}
