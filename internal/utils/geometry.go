package utils

import "math"

const (
	// RadiusOfEarthInMeters is RADIUS_OF_EARTH_IN_KM * 1000
	RadiusOfEarthInMeters = 6371010.0
)

// CoordinateBounds represents a bounding box with min/max latitude and longitude
type CoordinateBounds struct {
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
}

// Distance calculates the distance between two points on the Earth.
// For short distances (under ~22km), it uses a highly optimized Equirectangular
// approximation to save CPU cycles. For longer distances, it falls back to the exact formula.
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
	// Fast-path for short distances: coordinate differences less than 0.2 degrees (~22km)
	// Bypasses expensive Atan2, Pow, and multiple Sin/Cos calls for 99% of transit queries.
	if math.Abs(lat2-lat1) < 0.2 && math.Abs(lon2-lon1) < 0.2 {
		lat1Rad := lat1 * (math.Pi / 180)
		lat2Rad := lat2 * (math.Pi / 180)
		dLatRad := (lat2 - lat1) * (math.Pi / 180)
		dLonRad := (lon2 - lon1) * (math.Pi / 180)

		// Equirectangular approximation
		x := dLonRad * math.Cos((lat1Rad+lat2Rad)/2)
		y := dLatRad
		return RadiusOfEarthInMeters * math.Sqrt(x*x+y*y)
	}

	// Exact calculation fallback for longer distances
	lat1Rad := lat1 * (math.Pi / 180)
	lon1Rad := lon1 * (math.Pi / 180)
	lat2Rad := lat2 * (math.Pi / 180)
	lon2Rad := lon2 * (math.Pi / 180)

	deltaLon := lon2Rad - lon1Rad

	y := math.Sqrt(math.Pow(math.Cos(lat2Rad)*math.Sin(deltaLon), 2) +
		math.Pow(math.Cos(lat1Rad)*math.Sin(lat2Rad)-math.Sin(lat1Rad)*math.Cos(lat2Rad)*math.Cos(deltaLon), 2))
	x := math.Sin(lat1Rad)*math.Sin(lat2Rad) + math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Cos(deltaLon)

	return RadiusOfEarthInMeters * math.Atan2(y, x)
}

func CalculateBounds(lat, lon, distance float64) CoordinateBounds {
	latRadians := lat * math.Pi / 180
	lonRadians := lon * math.Pi / 180

	latRadius := RadiusOfEarthInMeters

	// Use math.Abs to prevent a negative cosine (possible for out-of-range latitudes)
	// which would invert the longitude bounds
	cosLat := math.Abs(math.Cos(latRadians))

	// Prevent division by zero (Infinity) exactly at the poles
	if cosLat < 1e-10 {
		cosLat = 1e-10
	}

	lonRadius := cosLat * RadiusOfEarthInMeters

	latOffset := distance / latRadius
	lonOffset := distance / lonRadius

	minLat := (latRadians - latOffset) * 180 / math.Pi
	maxLat := (latRadians + latOffset) * 180 / math.Pi
	minLon := (lonRadians - lonOffset) * 180 / math.Pi
	maxLon := (lonRadians + lonOffset) * 180 / math.Pi

	return CoordinateBounds{
		MinLat: minLat,
		MaxLat: maxLat,
		MinLon: minLon,
		MaxLon: maxLon,
	}
}

// CalculateBoundsFromSpan calculates a bounding box from lat/lon offsets.
func CalculateBoundsFromSpan(lat, lon, latOffset, lonOffset float64) CoordinateBounds {
	return CoordinateBounds{
		MinLat: lat - latOffset,
		MaxLat: lat + latOffset,
		MinLon: lon - lonOffset,
		MaxLon: lon + lonOffset,
	}
}

// IsOutOfBounds returns true only if the inner bounds have no overlap
// with the outer bounds.
func IsOutOfBounds(inner, outer CoordinateBounds) bool {
	return inner.MaxLat < outer.MinLat ||
		inner.MinLat > outer.MaxLat ||
		inner.MaxLon < outer.MinLon ||
		inner.MinLon > outer.MaxLon
}
