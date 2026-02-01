package gtfs

import (
	"context"
	"log/slog"

	"maglev.onebusaway.org/gtfsdb"
)

func InitializeGlobalCache(ctx context.Context, queries *gtfsdb.Queries, adc *AdvancedDirectionCalculator) error {
	slog.Info("starting global cache warmup...")

	// Fetch ALL Stop IDs
	allStopIDs, err := queries.GetAllStopIDs(ctx)
	if err != nil {
		return err
	}

	// Fetch Context (Stop -> Shape mappings)
	contextRows, err := queries.GetStopsWithShapeContextByIDs(ctx, allStopIDs)
	if err != nil {
		return err
	}

	contextCache := make(map[string][]gtfsdb.GetStopsWithShapeContextRow)
	shapeIDMap := make(map[string]bool)
	var uniqueShapeIDs []string

	for _, row := range contextRows {
		// Map the DB row to the Cache row struct
		calcRow := gtfsdb.GetStopsWithShapeContextRow{
			ID:                row.StopID,
			ShapeID:           row.ShapeID,
			Lat:               row.Lat,
			Lon:               row.Lon,
			ShapeDistTraveled: row.ShapeDistTraveled,
		}
		contextCache[row.StopID] = append(contextCache[row.StopID], calcRow)

		// Collect unique valid Shape IDs
		if row.ShapeID.Valid && row.ShapeID.String != "" && !shapeIDMap[row.ShapeID.String] {
			shapeIDMap[row.ShapeID.String] = true
			uniqueShapeIDs = append(uniqueShapeIDs, row.ShapeID.String)
		}
	}

	// Fetch Shape Points (Geometry)
	shapeCache := make(map[string][]gtfsdb.GetShapePointsWithDistanceRow)

	if len(uniqueShapeIDs) > 0 {
		shapePoints, err := queries.GetShapePointsByIDs(ctx, uniqueShapeIDs)
		if err != nil {
			// Fail fast if we can't load shapes (or just log error if you want to be resilient)
			slog.Warn("Failed to fetch shape points for global cache", "error", err)
			return err
		}

		for _, p := range shapePoints {
			shapeCache[p.ShapeID] = append(shapeCache[p.ShapeID], gtfsdb.GetShapePointsWithDistanceRow{
				Lat:               p.Lat,
				Lon:               p.Lon,
				ShapeDistTraveled: p.ShapeDistTraveled,
			})
		}
	}

	// Set Cache
	adc.SetShapeCache(shapeCache)
	adc.SetContextCache(contextCache)

	slog.Info("global cache warmup complete",
		slog.Int("stops_cached", len(contextCache)),
		slog.Int("shapes_cached", len(shapeCache)))

	return nil
}
