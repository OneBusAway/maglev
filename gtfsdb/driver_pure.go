//go:build purego

package gtfsdb

import (
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

const DriverName = "sqlite"

// DSN returns the connection string for path with foreign key enforcement and
// the page cache size set. Both are connection-scoped and not persistent, so
// setting them via PRAGMA on a single connection leaves the rest of the pool
// without them; they have to ride the DSN. busy_timeout matches the 5 s that
// the CGo driver sets by default.
func DSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=foreign_keys(1)&_pragma=cache_size(" + strconv.Itoa(-connectionCacheKiB) + ")&_pragma=busy_timeout(5000)"
}
