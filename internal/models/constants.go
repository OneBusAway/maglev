package models

// Common constants used across the application
const (
	// UnknownValue is the fallback value when data is unavailable or calculation fails
	UnknownValue = "UNKNOWN"
	// Accessible indicates wheelchair boarding is possible (GTFS wheelchair_boarding = 1)
	Accessible = "ACCESSIBLE"
	// NotAccessible indicates wheelchair boarding is not possible (GTFS wheelchair_boarding = 2)
	NotAccessible = "NOT_ACCESSIBLE"
)

const (
	DefaultSearchRadiusInMeters = 600
	QuerySearchRadiusInMeters   = 10000
	MaxSearchRadiusInMeters     = 20000
)

// Cache durations (in seconds) for different API data types.
const (
	CacheDurationLong  = 300
	CacheDurationShort = 30
	CacheDurationNone  = 0
)

const (
	DefaultMaxCountForStops = 100
	MaxAllowedCount         = 250
	// DefaultMaxCountForRoutesForLocation is the routes-for-location default when the
	// caller omits maxCount; MaxCountForRoutesForLocation is the ceiling it is clamped to.
	DefaultMaxCountForRoutesForLocation = 10
	// MaxCountForRoutesForLocation is the routes-for-location ceiling; the endpoint
	// silently clamps larger requests instead of rejecting them.
	MaxCountForRoutesForLocation = 50
)

// RangeSearchBufferMeters provides a 50m tolerance for GPS inaccuracy and curve approximation.
const RangeSearchBufferMeters = 50.0

// APIVersion is the standard version returned in all response envelopes
const APIVersion = 2
