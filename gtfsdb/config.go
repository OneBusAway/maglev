package gtfsdb

import "maglev.onebusaway.org/internal/appconf"

// Config holds configuration options for the Client
type Config struct {
	// Database configuration
	DBPath string              // Path to SQLite database file
	Env    appconf.Environment // Environment name: development, test, production.
}

func NewConfig(dbPath string, env appconf.Environment, verbose bool) Config {
	return Config{
		DBPath:  dbPath,
		Env:     env,
		verbose: verbose,
	}
}
