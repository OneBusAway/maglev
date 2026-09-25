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

func String(value string) sql.NullString {
	return sql.NullString{
		String: value,
		Valid:  true,
	}
}

// NonEmptyString creates a sql.NullString from the given string, converting empty values to null.
func NonEmptyString(value string) sql.NullString {
	return sql.NullString{
		String: value,
		Valid:  value != "",
	}
}

// Int64 creates a sql.NullInt64 from the given int64.
func Int64(value int64) sql.NullInt64 {
	return sql.NullInt64{
		Int64: value,
		Valid: true,
	}
}

// Float64OrNil returns a pointer to the float64 value if valid, otherwise nil.
func Float64OrNil(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	value := nf.Float64
	return &value
}

// IntOrNil returns a pointer to the value as an int if valid, otherwise nil.
func IntOrNil(ni sql.NullInt64) *int {
	if !ni.Valid {
		return nil
	}
	value := int(ni.Int64)
	return &value
}

// Int64FromPtr maps an optional integer-like value (an int32 count, or a
// time.Duration in nanoseconds) to a sql.NullInt64; nil becomes NULL.
func Int64FromPtr[T ~int32 | ~int64](value *T) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return Int64(int64(*value))
}

// Float64FromPtr maps an optional float64 to a sql.NullFloat64; nil becomes NULL.
func Float64FromPtr(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

// StringFromPtr maps an optional string to a sql.NullString; nil becomes NULL
// and a non-nil empty string stays a valid empty string.
func StringFromPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return String(*value)
}
