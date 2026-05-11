package nulls

import (
	"database/sql"

	"github.com/OneBusAway/go-gtfs"
)

// These helpers are in their own package to avoid internal dependencies, so
// that they can be used across the entire maglev codebase without creating
// dependency cycles.

// StringOrEmpty returns the string value if valid, otherwise returns an empty string
func StringOrEmpty(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func StringOrDefault(ns sql.NullString, defaultValue string) string {
	if ns.Valid {
		return ns.String
	}
	return defaultValue
}

// Int64OrDefault returns the int64 value if valid, otherwise returns the default value
func Int64OrDefault(ni sql.NullInt64, defaultValue int64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return defaultValue
}

// WheelchairBoardingOrUnknown returns the wheelchair boarding value if valid, otherwise returns NotSpecified
func WheelchairBoardingOrUnknown(ni sql.NullInt64) gtfs.WheelchairBoarding {
	if ni.Valid {
		return gtfs.WheelchairBoarding(ni.Int64)
	}
	return gtfs.WheelchairBoarding_NotSpecified
}
