package utils

import (
	"database/sql"

	"github.com/OneBusAway/go-gtfs"
)

// NullStringOrEmpty returns the string value if valid, otherwise returns an empty string
func NullStringOrEmpty(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// NullInt64OrDefault returns the int64 value if valid, otherwise returns the default value
func NullInt64OrDefault(ni sql.NullInt64, defaultValue int64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return defaultValue
}

// NullWheelchairBoardingOrUnknown returns the wheelchair boarding value if valid, otherwise returns NotSpecified
func NullWheelchairBoardingOrUnknown(ni sql.NullInt64) gtfs.WheelchairBoarding {
	if ni.Valid {
		return gtfs.WheelchairBoarding(ni.Int64)
	}
	return gtfs.WheelchairBoarding_NotSpecified
}
