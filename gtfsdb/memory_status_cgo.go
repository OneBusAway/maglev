//go:build !purego

package gtfsdb

/*
// go-sqlite3 compiles SQLite into its own package without exporting the
// header, so declare what is used here.
int sqlite3_config(int, ...);
long long sqlite3_memory_used(void);

#define SQLITE_CONFIG_MEMSTATUS 9

// sqlite3_config is variadic, which cgo cannot call directly.
static int turn_off_memory_status(void) {
	return sqlite3_config(SQLITE_CONFIG_MEMSTATUS, 0);
}
*/
import "C"

import (
	"fmt"
	"log/slog"
)

// disableMemoryStatusErr records why turning off SQLite's memory status
// failed, or nil when it is off.
var disableMemoryStatusErr = disableMemoryStatus()

// disableMemoryStatus turns off SQLite's memory statistics, which
// go-sqlite3 compiles in on by default. While they are on, every SQLite
// allocation in the process takes one global mutex, so concurrent queries
// queue on it no matter how many connections the pool holds, and
// allocation-heavy queries get slower in aggregate as requests are added.
// Maglev reads none of those statistics. The pure-Go driver is already built
// with them off.
//
// SQLite accepts this only before it initializes, which happens on the first
// open. go-sqlite3's init opens nothing, so running at package
// initialization, before any client exists, is early enough.
func disableMemoryStatus() error {
	if rc := C.turn_off_memory_status(); rc != 0 {
		// This runs before main installs the app's slog handler, so the
		// warning goes through Go's default logger.
		err := fmt.Errorf("sqlite3_config(SQLITE_CONFIG_MEMSTATUS, 0) returned %d", int(rc))
		slog.Warn("SQLite memory status left on; concurrent queries will contend on its mutex",
			slog.String("error", err.Error()))
		return err
	}
	return nil
}

// sqliteMemoryUsed returns the bytes SQLite reports in use, which stays zero
// while memory status is off.
func sqliteMemoryUsed() int64 {
	return int64(C.sqlite3_memory_used())
}
