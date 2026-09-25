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

// IsViewport reports whether the params describe a viewport search: both
// spans given and no radius. Radius takes precedence when both are supplied,
// per the OBA spec.
func (loc *LocationParams) IsViewport() bool {
	return loc.Radius <= 0 && loc.LatSpan > 0 && loc.LonSpan > 0
}

// RadiusOrDefault is the search radius, or DefaultSearchRadiusInMeters when
// none was given.
func (loc *LocationParams) RadiusOrDefault() float64 {
	if loc.Radius <= 0 {
		return models.DefaultSearchRadiusInMeters
	}
	return loc.Radius
}

// BoundsFromParams converts LocationParams into a CoordinateBounds bounding box.
// If Radius is positive (or when neither Radius nor valid Spans are provided),
// the box is computed from Radius (defaulting to DefaultSearchRadiusInMeters).
// If both Radius and LatSpan/LonSpan are provided, Radius takes precedence.
// If clamp is true, dimensions exceeding the maximum allowed search radius (20km)
// are clamped to the maximum circle bounds.
func BoundsFromParams(loc *LocationParams, clamp ...bool) utils.CoordinateBounds {
	shouldClamp := len(clamp) > 0 && clamp[0]

	if !loc.IsViewport() {
		radius := loc.RadiusOrDefault()
		if shouldClamp {
			radius = utils.ClampRadius(radius)
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
//
// clamp must match what the caller passed to BoundsFromParams when it ran the
// search. Reporting on unclamped bounds while searching clamped ones lets an
// oversized radius overlap a region it never actually searched.
func (manager *Manager) CheckIfOutOfBounds(loc *LocationParams, clamp ...bool) bool {
	overlaps, hasRegions := manager.OverlapsAnyRegion(BoundsFromParams(loc, clamp...))
	return hasRegions && !overlaps
}

// OverlapsAnyRegion reports whether bounds overlap at least one agency's
// region bounds. hasRegions is false when no region bounds are loaded, so a
// caller can tell "nothing to be outside of" from "outside every region".
func (manager *Manager) OverlapsAnyRegion(bounds utils.CoordinateBounds) (overlaps, hasRegions bool) {
	regions := manager.GetRegionBounds()
	for _, region := range regions {
		regionBounds := utils.CalculateBoundsFromSpan(region.Lat, region.Lon, region.LatSpan/2, region.LonSpan/2)
		if !utils.IsOutOfBounds(bounds, regionBounds) {
			return true, true
		}
	}
	return false, len(regions) > 0
}
