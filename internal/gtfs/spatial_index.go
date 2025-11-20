package gtfs

import (
	"context"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"

	"github.com/tidwall/rtree"
)

// buildStopSpatialIndex creates an R-tree from all stops that are actively used in stop_times
func buildStopSpatialIndex(ctx context.Context, queries *gtfsdb.Queries) (*rtree.RTree, error) {
	// Get only stops that are actively used in stop_times (have scheduled service)
	stops, err := queries.GetActiveStops(ctx)
	if err != nil {
		return nil, err
	}

	tree := &rtree.RTree{}

	// For points, min and max are the same [lat, lon]
	for _, stop := range stops {
		tree.Insert(
			[2]float64{stop.Lat, stop.Lon}, // min
			[2]float64{stop.Lat, stop.Lon}, // max
			stop,                           // data
		)
	}

	return tree, nil
}

// queryStopsInBounds retrieves all stops within the given geographic bounds from the R-tree
func queryStopsInBounds(tree *rtree.RTree, bounds utils.CoordinateBounds) []gtfsdb.Stop {
	if tree == nil {
		return []gtfsdb.Stop{}
	}

	minLat := min(bounds.MinLat, bounds.MaxLat)
	maxLat := max(bounds.MinLat, bounds.MaxLat)
	minLon := min(bounds.MinLon, bounds.MaxLon)
	maxLon := max(bounds.MinLon, bounds.MaxLon)

	var results []gtfsdb.Stop
	tree.Search(
		[2]float64{minLat, minLon}, // search min
		[2]float64{maxLat, maxLon}, // search max
		func(min, max [2]float64, data interface{}) bool {
			if stop, ok := data.(gtfsdb.Stop); ok {
				results = append(results, stop)
			}
			return true
		},
	)

	return results
}

// Helper functions for min/max
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
