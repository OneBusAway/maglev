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
)

const (
	DefaultMaxCountForRoutes = 50
	DefaultMaxCountForStops  = 100
	MaxAllowedCount          = 250
)
