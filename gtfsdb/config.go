package gtfsdb

import (
	"fmt"
	"strings"

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

// maxOpenConnections is the most connections the pool of a file database
// opens at once (configureConnectionPool).
const maxOpenConnections = 25

// connectionCacheKiB is the SQLite page cache of each pooled connection, in
// KiB. The pool has up to maxOpenConnections connections, so this bounds
// SQLite's page cache at maxOpenConnections times this value. It equals
// SQLite's built-in default and is set explicitly so that the bound doesn't
// depend on how a driver's SQLite was compiled. A larger cache didn't lower
// latency under the k6 load test, because the operating system already caches
// the database file.
const connectionCacheKiB = 2000

// connectionBusyTimeoutMs is how long, in milliseconds, a connection waits for
// a lock held by another connection before it returns SQLITE_BUSY. Both
// drivers set it in their DSN, so they behave the same even if a driver's own
// default changes.
const connectionBusyTimeoutMs = 5000

// DSN returns the connection string for path with foreign key enforcement,
// the page cache size and the busy timeout set. All three are
// connection-scoped and not persistent, so setting them via PRAGMA on a single
// connection leaves the rest of the pool without them; they have to ride the
// DSN. Each driver file spells the settings in its own connectionParams.
func DSN(path string) string {
	return dsn(path, connectionCacheKiB)
}

// dsn returns the connection string for path with a page cache of cacheKiB
// KiB. Tests use it to set a cache size that differs from SQLite's default.
func dsn(path string, cacheKiB int) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + connectionParams(cacheKiB)
}
