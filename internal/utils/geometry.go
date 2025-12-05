package utils

import "math"

const (
	// RADIUS_OF_EARTH_IN_METERS RADIUS_OF_EARTH_IN_KM * 1000
	RadiusOfEarthInMeters = 6371010.0
)

// CoordinateBounds represents a bounding box with min/max latitude and longitude
type CoordinateBounds struct {
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
}

// Distance calculates the distance between two points on the Earth
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
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
	lonRadius := math.Cos(latRadians) * RadiusOfEarthInMeters

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
