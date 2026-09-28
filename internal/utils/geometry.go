// Geometry lives in internal/geo because internal/utils imports gtfsdb, and
// gtfsdb must be able to use geometry at import time. This file re-exports the
// helpers utils always had so existing call sites keep compiling unchanged;
// new geometry callers import internal/geo directly.

package utils

import "maglev.onebusaway.org/internal/geo"

// RadiusOfEarthInMeters is RADIUS_OF_EARTH_IN_KM * 1000.
const RadiusOfEarthInMeters = geo.RadiusOfEarthInMeters

// CoordinateBounds represents a bounding box with min/max latitude and longitude.
type CoordinateBounds = geo.CoordinateBounds

// BoundsContain reports whether (lat, lon) falls within bounds, inclusive.
func BoundsContain(bounds CoordinateBounds, lat, lon float64) bool {
	return geo.BoundsContain(bounds, lat, lon)
}

// Distance calculates the distance in meters between two points on the Earth.
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
	return geo.Distance(lat1, lon1, lat2, lon2)
}

// CalculateBounds returns the bounding box extending distance meters around a point.
func CalculateBounds(lat, lon, distance float64) CoordinateBounds {
	return geo.CalculateBounds(lat, lon, distance)
}

// CalculateBoundsFromSpan calculates a bounding box from lat/lon offsets.
func CalculateBoundsFromSpan(lat, lon, latOffset, lonOffset float64) CoordinateBounds {
	return geo.CalculateBoundsFromSpan(lat, lon, latOffset, lonOffset)
}

// IsOutOfBounds returns true only if the inner bounds have no overlap with the outer bounds.
func IsOutOfBounds(inner, outer CoordinateBounds) bool {
	return geo.IsOutOfBounds(inner, outer)
}
