//go:build purego

package gtfsdb

import (
	"strconv"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

const DriverName = "sqlite"

// connectionParams returns the modernc.org/sqlite DSN parameters that apply
// the connection settings, with a page cache of cacheKiB KiB.
func connectionParams(cacheKiB int) string {
	return "_pragma=foreign_keys(1)&_pragma=recursive_triggers(1)&_pragma=cache_size(" + strconv.Itoa(-cacheKiB) + ")&_pragma=busy_timeout(" + strconv.Itoa(connectionBusyTimeoutMs) + ")"
}
