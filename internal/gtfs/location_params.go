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

// boundsFromParams converts LocationParams into a CoordinateBounds bounding box.
// If LatSpan and LonSpan are both positive, they define the box; otherwise the
// box is computed from Radius (defaulting to DefaultSearchRadiusInMeters).
func boundsFromParams(loc *LocationParams) utils.CoordinateBounds {
	if loc.LatSpan > 0 && loc.LonSpan > 0 {
		return utils.CalculateBoundsFromSpan(loc.Lat, loc.Lon, loc.LatSpan/2, loc.LonSpan/2)
	}
	radius := loc.Radius
	if radius == 0 {
		radius = models.DefaultSearchRadiusInMeters
	}
	return utils.CalculateBounds(loc.Lat, loc.Lon, radius)
}

// CheckIfOutOfBounds returns true if the user's search area is completely
// outside every agency's region bounds.
func (manager *Manager) CheckIfOutOfBounds(loc *LocationParams) bool {
	boundsMap := manager.GetRegionBounds()
	if len(boundsMap) == 0 {
		return false
	}

	innerBounds := boundsFromParams(loc)

	for _, region := range boundsMap {
		outerBounds := utils.CalculateBoundsFromSpan(region.Lat, region.Lon, region.LatSpan/2, region.LonSpan/2)
		if !utils.IsOutOfBounds(innerBounds, outerBounds) {
			return false
		}
	}

	return true
}
