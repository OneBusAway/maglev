package gtfs

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

const (
	defaultVarianceThreshold = 0.7
	shapePointWindow         = 5
)

// AdvancedDirectionCalculator implements the OneBusAway Java algorithm for stop direction calculation
type AdvancedDirectionCalculator struct {
	queries           *gtfsdb.Queries
	varianceThreshold float64
	contextCache      map[string][]gtfsdb.GetStopsWithShapeContextRow   // Cache of stop shape context data
	shapeCache        map[string][]gtfsdb.GetShapePointsWithDistanceRow // Cache of all shape data for bulk operations
	initialized       atomic.Bool                                       // Tracks whether concurrent operations have started
}

// NewAdvancedDirectionCalculator creates a new advanced direction calculator
func NewAdvancedDirectionCalculator(queries *gtfsdb.Queries) *AdvancedDirectionCalculator {
	return &AdvancedDirectionCalculator{
		queries:           queries,
		varianceThreshold: defaultVarianceThreshold,
	}
}

// SetVarianceThreshold sets the standard deviation threshold for direction variance checking.
// IMPORTANT: This must be called before any concurrent operations begin.
// Panics if called after CalculateStopDirection has been invoked.
func (adc *AdvancedDirectionCalculator) SetVarianceThreshold(threshold float64) {
	if adc.initialized.Load() {
		panic("SetVarianceThreshold called after concurrent operations have started")
	}
	adc.varianceThreshold = threshold
}

// SetShapeCache sets a pre-loaded cache of shape data to avoid database queries during bulk operations.
// This significantly improves performance when calculating directions for many stops.
// IMPORTANT: This must be called before any concurrent operations begin.
// Panics if called after CalculateStopDirection has been invoked.
func (adc *AdvancedDirectionCalculator) SetShapeCache(cache map[string][]gtfsdb.GetShapePointsWithDistanceRow) {
	if adc.initialized.Load() {
		panic("SetShapeCache called after concurrent operations have started")
	}
	adc.shapeCache = cache
}

// SetContextCache sets a pre-loaded cache of stop shape context data to avoid database queries.
// This can improve performance when calculating directions for many stops.
// IMPORTANT: This must be called before any concurrent operations begin.
// Panics if called after CalculateStopDirection has been invoked.
func (adc *AdvancedDirectionCalculator) SetContextCache(cache map[string][]gtfsdb.GetStopsWithShapeContextRow) {
	adc.contextCache = cache
}

// CalculateStopDirection computes the direction for a stop using the Java algorithm
func (adc *AdvancedDirectionCalculator) CalculateStopDirection(ctx context.Context, stopID string, gtfsDirection sql.NullString) string {
	// Mark as initialized on first use to prevent configuration changes during concurrent operations
	adc.initialized.Store(true)

	// Step 1: Try to use GTFS direction field if provided
	if gtfsDirection.Valid && gtfsDirection.String != "" {
		if direction := adc.translateGtfsDirection(gtfsDirection.String); direction != "" {
			return direction
		}
	}

	// Step 2: Calculate from shape data
	return adc.computeFromShapes(ctx, stopID)
}

// translateGtfsDirection converts GTFS direction field to compass direction
func (adc *AdvancedDirectionCalculator) translateGtfsDirection(direction string) string {
	direction = strings.TrimSpace(strings.ToLower(direction))

	// Try text-based directions
	switch direction {
	case "north":
		return "N"
	case "northeast":
		return "NE"
	case "east":
		return "E"
	case "southeast":
		return "SE"
	case "south":
		return "S"
	case "southwest":
		return "SW"
	case "west":
		return "W"
	case "northwest":
		return "NW"
	}

	// Try numeric directions (degrees)
	if degrees, err := strconv.ParseFloat(direction, 64); err == nil {
		// GTFS uses geographic bearings: 0°=North, 90°=East, 180°=South, 270°=West
		// Convert to mathematical angle: 0=East, π/2=North, π=West, -π/2=South
		// Formula: math_angle = (90 - bearing) * π/180
		orientation := (90.0 - degrees) * math.Pi / 180.0

		// Normalize to [-π, π]
		for orientation > math.Pi {
			orientation -= 2 * math.Pi
		}
		for orientation < -math.Pi {
			orientation += 2 * math.Pi
		}

		return adc.getAngleAsDirection(orientation)
	}

	return ""
}

// computeFromShapes calculates direction from shape data using the Java algorithm
func (adc *AdvancedDirectionCalculator) computeFromShapes(ctx context.Context, stopID string) string {

	var stopTrips []gtfsdb.GetStopsWithShapeContextRow

	// Use cache if available, otherwise hit DB
	if adc.contextCache != nil {
		stopTrips = adc.contextCache[stopID]
	} else {
		stopTrips, _ = adc.queries.GetStopsWithShapeContext(ctx, stopID)
	}

	// Collect orientations from all trips, using cache to avoid duplicates
	type shapeKey struct {
		shapeID      string
		distTraveled float64
		useGeo       bool // true when using geographic matching instead of distance
	}
	orientationCache := make(map[shapeKey]float64)
	var orientations []float64

	// Get stop coordinates (same for all trips)
	var stopLat, stopLon float64
	if len(stopTrips) > 0 {
		stopLat = stopTrips[0].Lat
		stopLon = stopTrips[0].Lon
	}

	for _, stopTrip := range stopTrips {
		if !stopTrip.ShapeID.Valid {
			continue
		}

		shapeID := stopTrip.ShapeID.String
		distTraveled := -1.0 // Use -1 to signal geographic matching
		useGeo := false

		// Prefer shape_dist_traveled if available
		if stopTrip.ShapeDistTraveled.Valid {
			distTraveled = stopTrip.ShapeDistTraveled.Float64
		} else {
			useGeo = true
		}

		// Check cache first
		key := shapeKey{shapeID, distTraveled, useGeo}
		if cachedOrientation, found := orientationCache[key]; found {
			orientations = append(orientations, cachedOrientation)
			continue
		}

		// Calculate orientation at this stop location using shape point window
		orientation, err := adc.calculateOrientationAtStop(ctx, shapeID, distTraveled, stopLat, stopLon)
		if err != nil {
			continue
		}

		// Cache and store
		orientationCache[key] = orientation
		orientations = append(orientations, orientation)
	}

	if len(orientations) == 0 {
		return ""
	}

	// Single orientation - return it directly
	if len(orientations) == 1 {
		return adc.getAngleAsDirection(orientations[0])
	}

	// Calculate mean orientation vector
	var xs, ys []float64
	for _, orientation := range orientations {
		x := math.Cos(orientation)
		y := math.Sin(orientation)
		xs = append(xs, x)
		ys = append(ys, y)
	}

	xMu := mean(xs)
	yMu := mean(ys)

	// Check for opposite directions (mean vector is zero)
	if xMu == 0.0 && yMu == 0.0 {
		return "" // Ambiguous direction
	}

	// Check variance threshold
	xVariance := variance(xs, xMu)
	yVariance := variance(ys, yMu)
	xStdDev := math.Sqrt(xVariance)
	yStdDev := math.Sqrt(yVariance)

	if xStdDev > adc.varianceThreshold || yStdDev > adc.varianceThreshold {
		return "" // Too much variance
	}

	// Calculate median orientation
	thetaMu := math.Atan2(yMu, xMu)
	var normalizedThetas []float64

	for _, orientation := range orientations {
		delta := orientation - thetaMu

		// Normalize delta to [-π, π)
		for delta < -math.Pi {
			delta += 2 * math.Pi
		}
		for delta >= math.Pi {
			delta -= 2 * math.Pi
		}

		normalizedThetas = append(normalizedThetas, thetaMu+delta)
	}

	sort.Float64s(normalizedThetas)
	thetaMedian := median(normalizedThetas)

	return adc.getAngleAsDirection(thetaMedian)
}

// calculateOrientationAtStop calculates the orientation at a stop using a window of shape points
// If distTraveled is < 0, it uses stopLat/stopLon for geographic matching (when shape_dist_traveled is unavailable)
func (adc *AdvancedDirectionCalculator) calculateOrientationAtStop(ctx context.Context, shapeID string, distTraveled float64, stopLat, stopLon float64) (float64, error) {
	var shapePoints []gtfsdb.GetShapePointsWithDistanceRow
	var err error

	// Try cache first if available
	if adc.shapeCache != nil {
		var found bool
		shapePoints, found = adc.shapeCache[shapeID]
		if !found || len(shapePoints) < 2 {
			return 0, sql.ErrNoRows
		}
	} else {
		// Fall back to database query if no cache
		shapePoints, err = adc.queries.GetShapePointsWithDistance(ctx, shapeID)
		if err != nil || len(shapePoints) < 2 {
			return 0, err
		}
	}

	closestIdx := 0
	minDiff := math.MaxFloat64

	// Use shape_dist_traveled if available (distTraveled >= 0)
	if distTraveled >= 0 {
		// Find the closest shape point using shape_dist_traveled
		for i, point := range shapePoints {
			if point.ShapeDistTraveled.Valid {
				diff := math.Abs(point.ShapeDistTraveled.Float64 - distTraveled)
				if diff < minDiff {
					minDiff = diff
					closestIdx = i
				}
			}
		}
	}

	// Fall back to geographic matching when shape_dist_traveled is not available
	if minDiff == math.MaxFloat64 && stopLat != 0 && stopLon != 0 {
		for i, point := range shapePoints {
			distance := utils.Distance(stopLat, stopLon, point.Lat, point.Lon)
			if distance < minDiff {
				minDiff = distance
				closestIdx = i
			}
		}
	}

	// If still no match found, fall back to first point
	if minDiff == math.MaxFloat64 {
		closestIdx = 0
	}

	// Define window around stop
	indexFrom := closestIdx - shapePointWindow
	if indexFrom < 0 {
		indexFrom = 0
	}
	indexTo := closestIdx + shapePointWindow
	if indexTo > len(shapePoints) {
		indexTo = len(shapePoints)
	}

	// Calculate orientation from the window
	// Use the bearing from the first point to the last point in the window
	if indexTo > indexFrom+1 {
		fromPoint := shapePoints[indexFrom]
		toPoint := shapePoints[indexTo-1]

		bearing := utils.BearingBetweenPoints(fromPoint.Lat, fromPoint.Lon, toPoint.Lat, toPoint.Lon)
		// Convert bearing (0-360°, 0=North) to mathematical angle (radians, 0=East, counterclockwise)
		// Bearing: 0°=N, 90°=E, 180°=S, 270°=W
		// Math angle: 0=E, π/2=N, π=W, -π/2=S
		orientation := (90.0 - bearing) * math.Pi / 180.0
		return orientation, nil
	}

	return 0, sql.ErrNoRows
}

// getAngleAsDirection converts a radian angle to compass direction
// Uses the Java coordinate system: 0=East, π/2=North, π=West, -π/2=South
func (adc *AdvancedDirectionCalculator) getAngleAsDirection(theta float64) string {
	// Normalize angle to [-π, π)
	for theta >= math.Pi {
		theta -= 2 * math.Pi
	}
	for theta < -math.Pi {
		theta += 2 * math.Pi
	}

	t := math.Pi / 4 // 45 degrees in radians
	r := int(math.Floor((theta + t/2) / t))

	switch r {
	case 0:
		return "E" // 0° ± 22.5°
	case 1:
		return "NE" // 45° ± 22.5°
	case 2:
		return "N" // 90° ± 22.5°
	case 3:
		return "NW" // 135° ± 22.5°
	case 4, -4:
		return "W" // ±180° ± 22.5°
	case -1:
		return "SE" // -45° ± 22.5°
	case -2:
		return "S" // -90° ± 22.5°
	case -3:
		return "SW" // -135° ± 22.5°
	default:
		return "" // Unknown
	}
}

// Statistical helper functions

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func variance(values []float64, mean float64) float64 {
	if len(values) <= 1 {
		return 0
	}
	sumSquares := 0.0
	for _, v := range values {
		diff := v - mean
		sumSquares += diff * diff
	}
	return sumSquares / float64(len(values)-1) // Sample variance
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	n := len(values)
	if n%2 == 0 {
		return (values[n/2-1] + values[n/2]) / 2.0
	}
	return values[n/2]
}
