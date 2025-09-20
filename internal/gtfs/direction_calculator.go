package gtfs

import (
	"context"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

const unknownDirection = models.UnknownValue

type DirectionCalculator struct {
	queries *gtfsdb.Queries
}

func NewDirectionCalculator(queries *gtfsdb.Queries) *DirectionCalculator {
	return &DirectionCalculator{
		queries: queries,
	}
}

// CalculateStopDirection determines the compass direction for a stop
func (dc *DirectionCalculator) CalculateStopDirection(ctx context.Context, stopID string) string {
	// Strategy 1: Try shape-based calculation
	if direction := dc.calculateFromShape(ctx, stopID); direction != unknownDirection {
		return direction
	}

	// Strategy 2: Fallback to stop-to-stop calculation
	if direction := dc.calculateFromNextStop(ctx, stopID); direction != unknownDirection {
		return direction
	}

	// No direction could be calculated
	return unknownDirection
}

func (dc *DirectionCalculator) calculateFromShape(ctx context.Context, stopID string) string {
	// Get trips serving this stop
	stopTrips, err := dc.queries.GetStopsWithTripContext(ctx, stopID)
	if err != nil || len(stopTrips) == 0 {
		return unknownDirection
	}

	directions := make(map[string]int)

	for _, stopTrip := range stopTrips {
		if !stopTrip.ShapeID.Valid {
			continue
		}

		// Get shape points for this trip
		shapePoints, err := dc.queries.GetShapePointsForTrip(ctx, stopTrip.TripID)
		if err != nil || len(shapePoints) < 2 {
			continue
		}

		// Find closest shape point to stop
		stopLat, stopLon := stopTrip.Lat, stopTrip.Lon
		closestIdx := dc.findClosestShapePoint(shapePoints, stopLat, stopLon)

		// Calculate direction to next shape point
		if closestIdx < len(shapePoints)-1 {
			nextPoint := shapePoints[closestIdx+1]
			direction := utils.CompassDirection(
				stopLat, stopLon,
				nextPoint.Lat, nextPoint.Lon,
			)
			directions[direction]++
		}
	}

	return dc.getMostCommonDirection(directions)
}

func (dc *DirectionCalculator) calculateFromNextStop(ctx context.Context, stopID string) string {
	stopTrips, err := dc.queries.GetStopsWithTripContext(ctx, stopID)
	if err != nil || len(stopTrips) == 0 {
		return unknownDirection
	}

	directions := make(map[string]int)

	for _, stopTrip := range stopTrips {
		nextStop, err := dc.queries.GetNextStopInTrip(ctx, gtfsdb.GetNextStopInTripParams{
			TripID:       stopTrip.TripID,
			StopSequence: stopTrip.StopSequence,
		})
		if err != nil {
			continue
		}

		direction := utils.CompassDirection(
			stopTrip.Lat, stopTrip.Lon,
			nextStop.Lat, nextStop.Lon,
		)
		directions[direction]++
	}

	return dc.getMostCommonDirection(directions)
}

func (dc *DirectionCalculator) findClosestShapePoint(points []gtfsdb.GetShapePointsForTripRow, lat, lon float64) int {
	if len(points) == 0 {
		return -1
	}

	closestIdx := 0
	minDistance := utils.Haversine(lat, lon, points[0].Lat, points[0].Lon)

	for i, point := range points[1:] {
		distance := utils.Haversine(lat, lon, point.Lat, point.Lon)
		if distance < minDistance {
			minDistance = distance
			closestIdx = i + 1
		}
	}

	return closestIdx
}

func (dc *DirectionCalculator) getMostCommonDirection(directions map[string]int) string {
	maxCount := 0
	result := unknownDirection

	for direction, count := range directions {
		if count > maxCount {
			maxCount = count
			result = direction
		}
	}

	return result
}
