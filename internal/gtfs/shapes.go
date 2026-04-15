package gtfs

import (
	"context"
	"math"

	"maglev.onebusaway.org/gtfsdb"
)

// roundTo7 rounds a float64 to 7 decimal places (~1.1cm precision, plenty
// for region-level bounding boxes).
func roundTo7(v float64) float64 {
	return math.Round(v*1e7) / 1e7
}

// computeRegionBounds calculates the geographic boundaries per agency.
func computeRegionBounds(ctx context.Context, gtfsDB *gtfsdb.Client) map[string]*RegionBounds {
	rows, err := gtfsDB.Queries.GetStopBoundsPerAgency(ctx)
	if err != nil || len(rows) == 0 {
		return nil
	}

	result := make(map[string]*RegionBounds, len(rows))
	for _, row := range rows {
		result[row.AgencyID] = &RegionBounds{
			Lat:     roundTo7((row.MinLat + row.MaxLat) / 2),
			Lon:     roundTo7((row.MinLon + row.MaxLon) / 2),
			LatSpan: roundTo7(row.MaxLat - row.MinLat),
			LonSpan: roundTo7(row.MaxLon - row.MinLon),
		}
	}

	return result
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetRegionBounds() map[string]RegionBounds {
	if manager.regionBounds == nil {
		return nil
	}
	result := make(map[string]RegionBounds, len(manager.regionBounds))
	for k, v := range manager.regionBounds {
		result[k] = *v
	}
	return result
}
