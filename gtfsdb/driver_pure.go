//go:build purego

package gtfsdb

import _ "modernc.org/sqlite" // Pure Go SQLite driver

const DriverName = "sqlite"
