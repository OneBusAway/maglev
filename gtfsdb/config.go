package gtfsdb

import (
	"fmt"

	"maglev.onebusaway.org/internal/appconf"
)

const (
	// sqliteMaxVariables is the hard upper bound on bound parameters per SQLite
	// statement (SQLITE_MAX_VARIABLE_NUMBER).
	sqliteMaxVariables = 32766
)

// Config holds configuration options for the Client
type Config struct {
	// Database configuration
	DBPath string              // Path to SQLite database file
	Env    appconf.Environment // Environment name: development, test, production.
	// Optional recorder for DB query metrics.
	QueryMetricsRecorder DBQueryMetricsRecorder
}

// DBQueryMetricsRecorder is a minimal abstraction used to emit per-query metrics
// without coupling gtfsdb to a specific metrics implementation.
type DBQueryMetricsRecorder interface {
	RecordDBQuery(queryName, op string, err error)
}

func NewConfig(dbPath string, env appconf.Environment) Config {
	return Config{
		DBPath: dbPath,
		Env:    env,
	}
}

// SafeBatchSize returns the maximum safe number of rows per multi-row INSERT given
// the number of parameters bound per row. SQLite enforces a hard upper bound of
// SQLITE_MAX_VARIABLE_NUMBER (32766) bound parameters per statement, so the safe
// maximum is 32766 / fieldsPerRow.
// Panics if fieldsPerRow <= 0 as this always indicates a programming error —
// fieldsPerRow is a compile-time constant that must match the INSERT column list.
func (c Config) SafeBatchSize(fieldsPerRow int) int {
	if fieldsPerRow <= 0 {
		panic(fmt.Sprintf("SafeBatchSize: fieldsPerRow must be > 0, got %d", fieldsPerRow))
	}
	return sqliteMaxVariables / fieldsPerRow
}

// connectionCacheKiB is the SQLite page cache of each pooled connection, in
// KiB. The pool has up to 25 connections (configureConnectionPool), so this
// bounds SQLite's page cache at 25 times this value, about 50 MB. It equals
// SQLite's built-in default and is set explicitly so that the bound doesn't
// depend on how a driver's SQLite was compiled. A larger cache didn't lower
// latency under the k6 load test, because the operating system already caches
// the database file.
const connectionCacheKiB = 2000

// connectionBusyTimeoutMs is how long, in milliseconds, a connection waits for
// a lock held by another connection before it returns SQLITE_BUSY. The CGo
// driver sets this value by default; the pure-Go driver sets it in its DSN so
// that both drivers behave the same.
const connectionBusyTimeoutMs = 5000
