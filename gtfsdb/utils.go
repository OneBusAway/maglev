package gtfsdb

import (
	"database/sql"
)

// NullStringOrEmpty returns the string value if valid, otherwise returns an empty string
// This duplicates utils.NullStringOrEmpty to avoid cyclic dependencies.
// TODO: move utils.NullStringOrEmpty into another package (maybe this one) so it can share its implementation.
func NullStringOrEmpty(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
