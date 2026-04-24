//go:build purego

package gtfsdb

import (
	"errors"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func isConstraintErr(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	// Match primary SQLITE_CONSTRAINT (19) regardless of extended code.
	return e.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}
