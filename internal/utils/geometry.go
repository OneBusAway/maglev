// Geometry lives in internal/geo because internal/utils imports gtfsdb, and
// gtfsdb must be able to use geometry at import time. This file re-exports it
// so existing utils call sites keep compiling unchanged.

package utils

import "maglev.onebusaway.org/internal/geo"

// RadiusOfEarthInMeters is RADIUS_OF_EARTH_IN_KM * 1000.
const RadiusOfEarthInMeters = geo.RadiusOfEarthInMeters

// GeoJSON geometry types accepted for GTFS-Flex locations.
const (
	GeoJSONPolygon      = geo.GeoJSONPolygon
	GeoJSONMultiPolygon = geo.GeoJSONMultiPolygon
)

// Display-geometry simplification bounds; see geo.SimplifyPolygons.
const (
	SimplifyInitialToleranceMeters = geo.SimplifyInitialToleranceMeters
	SimplifyMaxRingPoints          = geo.SimplifyMaxRingPoints
)

// CoordinateBounds represents a bounding box with min/max latitude and longitude.
type CoordinateBounds = geo.CoordinateBounds

// SimplifiedPolygons is the result of SimplifyPolygons.
type SimplifiedPolygons = geo.SimplifiedPolygons

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

// ParseGeoJSONPolygons parses a GeoJSON Polygon or MultiPolygon geometry.
func ParseGeoJSONPolygons(raw []byte) (string, [][][][2]float64, error) {
	return geo.ParseGeoJSONPolygons(raw)
}

// EncodeGeoJSONGeometry is the inverse of ParseGeoJSONPolygons.
func EncodeGeoJSONGeometry(geometryType string, polygons [][][][2]float64) ([]byte, error) {
	return geo.EncodeGeoJSONGeometry(geometryType, polygons)
}

// PolygonsBounds returns the bounding box over every vertex of every ring.
func PolygonsBounds(polygons [][][][2]float64) CoordinateBounds {
	return geo.PolygonsBounds(polygons)
}

// SimplifyPolygons returns display geometry bounded to SimplifyMaxRingPoints per ring.
func SimplifyPolygons(polygons [][][][2]float64) SimplifiedPolygons {
	return geo.SimplifyPolygons(polygons)
}

// UnionBounds returns the smallest box containing both a and b.
func UnionBounds(a, b CoordinateBounds) CoordinateBounds {
	return geo.UnionBounds(a, b)
}

// PointInPolygon reports whether (lat, lon) is inside the geometry, holes excluded.
func PointInPolygon(lat, lon float64, polygons [][][][2]float64) bool {
	return geo.PointInPolygon(lat, lon, polygons)
}

// NearestPointOnBoundary returns the distance to, and location of, the closest ring point.
func NearestPointOnBoundary(lat, lon float64, polygons [][][][2]float64) (distanceMeters, nearestLon, nearestLat float64) {
	return geo.NearestPointOnBoundary(lat, lon, polygons)
}

// PolygonIntersectsBounds reports whether the geometry overlaps the bounding box.
func PolygonIntersectsBounds(polygons [][][][2]float64, bounds CoordinateBounds) bool {
	return geo.PolygonIntersectsBounds(polygons, bounds)
}
