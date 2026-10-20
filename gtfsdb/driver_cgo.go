//go:build !purego

package gtfsdb

import (
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
)

const DriverName = "sqlite3"

// DSN returns the connection string for path with foreign key enforcement and
// the page cache size set. Both are connection-scoped and not persistent, so
// setting them via PRAGMA on a single connection leaves the rest of the pool
// without them; they have to ride the DSN.
func DSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_foreign_keys=on&_cache_size=" + strconv.Itoa(-connectionCacheKiB)
}
