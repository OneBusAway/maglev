//go:build !purego

package gtfsdb

import (
	"strconv"

	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
)

const DriverName = "sqlite3"

// connectionParams returns the mattn/go-sqlite3 DSN parameters that apply the
// connection settings, with a page cache of cacheKiB KiB.
func connectionParams(cacheKiB int) string {
	return "_foreign_keys=on&_recursive_triggers=on&_cache_size=" + strconv.Itoa(-cacheKiB) + "&_busy_timeout=" + strconv.Itoa(connectionBusyTimeoutMs)
}
