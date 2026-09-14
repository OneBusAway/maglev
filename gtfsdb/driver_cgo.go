//go:build !purego

package gtfsdb

import (
	"strings"

	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
)

const DriverName = "sqlite3"

// DSN returns the connection string for path with foreign key enforcement enabled.
// foreign_keys is connection-scoped and not persistent, so setting it via PRAGMA on a
// single connection leaves the rest of the pool unenforced; it has to ride the DSN.
func DSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_foreign_keys=on"
}
