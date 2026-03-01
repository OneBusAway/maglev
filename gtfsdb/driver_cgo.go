//go:build !purego

package gtfsdb

import _ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver

const DriverName = "sqlite3"
