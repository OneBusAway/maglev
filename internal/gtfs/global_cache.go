package gtfs

import (
	"context"
	"fmt"
	"log/slog"

	"maglev.onebusaway.org/gtfsdb"
)

func InitializeGlobalCache(ctx context.Context, queries *gtfsdb.Queries, adc *AdvancedDirectionCalculator) error {
	slog.Info("starting global cache warmup...")

	allStopIDs, err := queries.GetAllStopIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch all stop IDs: %w", err)
	}

	contextRows, err := queries.GetStopsWithShapeContextByIDs(ctx, allStopIDs)
	if err != nil {
		return fmt.Errorf("failed to fetch stop context rows: %w", err)
	}

	contextCache := make(map[string][]gtfsdb.GetStopsWithShapeContextRow)
	shapeIDMap := make(map[string]bool)
	var uniqueShapeIDs []string

	for _, row := range contextRows {
		calcRow := gtfsdb.GetStopsWithShapeContextRow{
			ID:                row.StopID,
			ShapeID:           row.ShapeID,
			Lat:               row.Lat,
			Lon:               row.Lon,
			ShapeDistTraveled: row.ShapeDistTraveled,
		}
		contextCache[row.StopID] = append(contextCache[row.StopID], calcRow)

		if row.ShapeID.Valid && row.ShapeID.String != "" && !shapeIDMap[row.ShapeID.String] {
			shapeIDMap[row.ShapeID.String] = true
			uniqueShapeIDs = append(uniqueShapeIDs, row.ShapeID.String)
		}
	}

	shapeCache := make(map[string][]gtfsdb.GetShapePointsWithDistanceRow)

	if len(uniqueShapeIDs) > 0 {
		shapePoints, err := queries.GetShapePointsByIDs(ctx, uniqueShapeIDs)
		if err != nil {
			return fmt.Errorf("failed to fetch shape points for global cache: %w", err)
		}

		for _, p := range shapePoints {
			shapeCache[p.ShapeID] = append(shapeCache[p.ShapeID], gtfsdb.GetShapePointsWithDistanceRow{
				Lat:               p.Lat,
				Lon:               p.Lon,
				ShapeDistTraveled: p.ShapeDistTraveled,
				ShapePtSequence:   p.ShapePtSequence,
			})
		}
	}

	if err := adc.SetShapeCache(shapeCache); err != nil {
		return fmt.Errorf("failed to set shape cache: %w", err)
	}
	if err := adc.SetContextCache(contextCache); err != nil {
		return fmt.Errorf("failed to set context cache: %w", err)
	}

	slog.Info("global cache warmup complete",
		slog.Int("stops_cached", len(contextCache)),
		slog.Int("shapes_cached", len(shapeCache)))

	return nil
}
