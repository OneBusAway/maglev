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
	DBPath  string              // Path to SQLite database file
	Env     appconf.Environment // Environment name: development, test, production.
	verbose bool                // Enable verbose logging
}

func NewConfig(dbPath string, env appconf.Environment, verbose bool) Config {
	return Config{
		DBPath:  dbPath,
		Env:     env,
		verbose: verbose,
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
