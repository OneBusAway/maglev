package utils

import (
	"math"
)

const (
	degreesInCircle     = 360.0
	compassSectorOffset = 22.5
	compassSectorSize   = 45.0
)

// BearingBetweenPoints calculates the bearing in degrees from point1 to point2
func BearingBetweenPoints(lat1, lon1, lat2, lon2 float64) float64 {
	// Convert to radians
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	y := math.Sin(deltaLon) * math.Cos(phi2)
	x := math.Cos(phi1)*math.Sin(phi2) - math.Sin(phi1)*math.Cos(phi2)*math.Cos(deltaLon)

	theta := math.Atan2(y, x)
	bearing := math.Mod(theta*180/math.Pi+degreesInCircle, degreesInCircle)

	return bearing
}

// BearingToCompass converts a bearing (0-360°) to 8-point compass direction
func BearingToCompass(bearing float64) string {
	directions := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	index := int((bearing+compassSectorOffset)/compassSectorSize) % 8
	return directions[index]
}

// CompassDirection calculates compass direction from lat1,lon1 to lat2,lon2
func CompassDirection(lat1, lon1, lat2, lon2 float64) string {
	bearing := BearingBetweenPoints(lat1, lon1, lat2, lon2)
	return BearingToCompass(bearing)
}
