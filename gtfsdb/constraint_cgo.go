//go:build !purego

package gtfsdb

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func isConstraintErr(err error) bool {
	var e sqlite3.Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == sqlite3.ErrConstraint
}
