package gtfs

import (
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type LocationParams struct {
	Lat     float64
	Lon     float64
	Radius  float64
	LatSpan float64
	LonSpan float64
}

// BoundsFromParams converts LocationParams into a CoordinateBounds bounding box.
// If Radius is positive (or when neither Radius nor valid Spans are provided),
// the box is computed from Radius (defaulting to DefaultSearchRadiusInMeters).
// If both Radius and LatSpan/LonSpan are provided, Radius takes precedence.
// If clamp is true, dimensions exceeding the maximum allowed search radius (20km)
// are clamped to the maximum circle bounds.
func BoundsFromParams(loc *LocationParams, clamp ...bool) utils.CoordinateBounds {
	shouldClamp := len(clamp) > 0 && clamp[0]

	// If Radius is specified (>0) OR neither Radius nor both Spans are provided (>0), use radius calculation.
	// This ensures radius takes precedence when both radius and span are supplied per OBA spec.
	if loc.Radius > 0 || !(loc.LatSpan > 0 && loc.LonSpan > 0) {
		radius := loc.Radius
		if radius <= 0 {
			radius = models.DefaultSearchRadiusInMeters
		}
		if shouldClamp && radius > models.MaxSearchRadiusInMeters {
			radius = models.MaxSearchRadiusInMeters
		}
		return utils.CalculateBounds(loc.Lat, loc.Lon, radius)
	}

	latSpan := loc.LatSpan
	lonSpan := loc.LonSpan
	if shouldClamp {
		maxBounds := utils.CalculateBounds(loc.Lat, loc.Lon, models.MaxSearchRadiusInMeters)
		maxLatSpan := maxBounds.MaxLat - maxBounds.MinLat
		maxLonSpan := maxBounds.MaxLon - maxBounds.MinLon
		if latSpan > maxLatSpan {
			latSpan = maxLatSpan
		}
		if lonSpan > maxLonSpan {
			lonSpan = maxLonSpan
		}
	}
	return utils.CalculateBoundsFromSpan(loc.Lat, loc.Lon, latSpan/2, lonSpan/2)
}

// CheckIfOutOfBounds returns true if the user's search area is completely
// outside every agency's region bounds.
func (manager *Manager) CheckIfOutOfBounds(loc *LocationParams) bool {
	boundsMap := manager.GetRegionBounds()
	if len(boundsMap) == 0 {
		return false
	}

	innerBounds := BoundsFromParams(loc)

	for _, region := range boundsMap {
		outerBounds := utils.CalculateBoundsFromSpan(region.Lat, region.Lon, region.LatSpan/2, region.LonSpan/2)
		if !utils.IsOutOfBounds(innerBounds, outerBounds) {
			return false
		}
	}

	return true
}
