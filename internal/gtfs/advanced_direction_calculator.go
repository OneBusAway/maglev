package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

const (
	defaultStandardDeviationThreshold = 0.7
	shapePointWindow                  = 5
)

// AdvancedDirectionCalculator implements the OneBusAway Java algorithm for stop direction calculation
type AdvancedDirectionCalculator struct {
	queries                    *gtfsdb.Queries
	queriesMu                  sync.RWMutex // Protects queries pointer
	standardDeviationThreshold float64
	shapeCache                 map[string][]gtfsdb.GetShapePointsWithDistanceRow // Cache of all shape data for bulk operations
	initialized                atomic.Bool                                       // Tracks whether concurrent operations have started
	cacheMutex                 sync.RWMutex                                      // Protects shapeCache map access
	// directionResults caches computed stop directions.
	// Only non-error results are cached; transient DB errors are never stored so that
	// a recovered database will be retried on the next request.
	// Lifecycle note: This map caches computed directions to reduce database load.
	// It is explicitly cleared during GTFS reloads (via UpdateQueries) to prevent
	// stale directions from persisting across dataset updates.
	directionResults sync.Map           // Cached direction results (stopID -> string), includes negative cache
	requestGroup     singleflight.Group // Prevents duplicate concurrent computations for the same stop
}

// NewAdvancedDirectionCalculator creates a new advanced direction calculator
func NewAdvancedDirectionCalculator(queries *gtfsdb.Queries) *AdvancedDirectionCalculator {
	return &AdvancedDirectionCalculator{
		queries:                    queries,
		standardDeviationThreshold: defaultStandardDeviationThreshold,
	}
}

// UpdateQueries replaces the queries pointer used for on-demand DB lookups.
// Call this after a GTFS hot-swap so the calculator queries the new database.
// It also clears the direction result cache so stale entries from the old database
// are not served.
func (adc *AdvancedDirectionCalculator) UpdateQueries(queries *gtfsdb.Queries) {
	adc.queriesMu.Lock()
	adc.queries = queries
	adc.queriesMu.Unlock()

	// Evict all cached directions so they are recomputed against the new DB.
	adc.directionResults.Clear()
}

// SetStandardDeviationThreshold sets the standard deviation threshold for direction variance checking.
// IMPORTANT: This must be called before any concurrent operations begin.
// Returns an error if called after CalculateStopDirection has been invoked.
func (adc *AdvancedDirectionCalculator) SetStandardDeviationThreshold(threshold float64) error {
	if adc.initialized.Load() {
		return errors.New("SetStandardDeviationThreshold called after concurrent operations have started")
	}
	if threshold <= 0 {
		return errors.New("standard deviation threshold must be greater than zero")
	}
	adc.standardDeviationThreshold = threshold
	return nil
}

// SetShapeCache is retained exclusively for use by the DirectionPrecomputer during startup.
// It sets a pre-loaded cache of shape data to avoid thousands of database queries during
// the precomputation phase, significantly improving startup performance.
// IMPORTANT: This must be called before any concurrent operations begin.
// Returns an error if called after CalculateStopDirection has been invoked.
func (adc *AdvancedDirectionCalculator) SetShapeCache(cache map[string][]gtfsdb.GetShapePointsWithDistanceRow) error {
	adc.cacheMutex.Lock()
	defer adc.cacheMutex.Unlock()

	if adc.initialized.Load() {
		return errors.New("SetShapeCache called after concurrent operations have started")
	}
	adc.shapeCache = cache
	return nil
}

// CalculateStopDirection computes the direction for a stop using the Java algorithm
func (adc *AdvancedDirectionCalculator) CalculateStopDirection(ctx context.Context, stopID string, gtfsDirection ...sql.NullString) string {
	if len(gtfsDirection) > 0 && gtfsDirection[0].Valid && gtfsDirection[0].String != "" {
		if direction := adc.translateGtfsDirection(gtfsDirection[0].String); direction != "" {
			return direction
		}
	}

	// Check the in-memory result cache (includes negative cache for empty results)
	if cached, ok := adc.directionResults.Load(stopID); ok {
		return cached.(string)
	}

	// Mark as initialized for concurrency safety
	adc.initialized.Store(true)

	// Fall back to computing from shapes, protected by singleflight
	// This ensures concurrent requests for the SAME stopID don't hit the DB multiple times.
	v, _, _ := adc.requestGroup.Do(stopID, func() (interface{}, error) {
		// Double-check cache inside the singleflight in case another goroutine just finished it
		if cached, ok := adc.directionResults.Load(stopID); ok {
			return cached.(string), nil
		}

		// Actually compute it (Hits the DB)
		computedDir, err := adc.computeFromShapes(context.WithoutCancel(ctx), stopID)

		// Only cache when there was no transient error. A transient error (e.g. DB
		// connection lost) must not permanently poison the cache; omitting it here
		// means the next request will retry the DB.
		if err == nil {
			adc.directionResults.Store(stopID, computedDir)
		}

		// Intentionally return nil so singleflight shares the empty fallback result with concurrent callers.
		// Since we skip caching on error, future requests will safely retry the DB.
		return computedDir, nil
	})

	return v.(string)
}

// translateGtfsDirection converts GTFS direction field to compass direction
func (adc *AdvancedDirectionCalculator) translateGtfsDirection(direction string) string {
	direction = strings.TrimSpace(strings.ToLower(direction))

	// Try text-based directions and compass abbreviations
	switch direction {
	case "north", "n":
		return "N"
	case "northeast", "ne":
		return "NE"
	case "east", "e":
		return "E"
	case "southeast", "se":
		return "SE"
	case "south", "s":
		return "S"
	case "southwest", "sw":
		return "SW"
	case "west", "w":
		return "W"
	case "northwest", "nw":
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

// computeFromShapes calculates direction from shape data using the Java algorithm.
// Returns (direction, nil) on success, ("", nil) when there is legitimately no shape
// data for the stop (safe to cache), or ("", err) on a transient database error
// (must NOT be cached so the next request retries the DB).
func (adc *AdvancedDirectionCalculator) computeFromShapes(ctx context.Context, stopID string) (string, error) {

	adc.queriesMu.RLock()
	q := adc.queries
	adc.queriesMu.RUnlock()

	stopTrips, err := q.GetStopsWithShapeContext(ctx, stopID)
	if err != nil {
		slog.Warn("failed to get stop shape context",
			slog.String("stopID", stopID),
			slog.String("error", err.Error()))
		return "", err
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

	var lastTransientErr error

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
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Warn("failed to calculate orientation at stop",
					slog.String("stopID", stopID),
					slog.String("shapeID", shapeID),
					slog.String("error", err.Error()))
				lastTransientErr = err
			}
			continue
		}

		// Cache and store
		orientationCache[key] = orientation
		orientations = append(orientations, orientation)
	}

	if len(orientations) == 0 {
		if lastTransientErr != nil {
			return "", lastTransientErr
		}
		return "", nil
	}

	// Single orientation - return it directly
	if len(orientations) == 1 {
		return adc.getAngleAsDirection(orientations[0]), nil
	}

	// Calculate mean orientation vector
	var xs, ys []float64
	for _, orientation := range orientations {
		xs = append(xs, math.Cos(orientation))
		ys = append(ys, math.Sin(orientation))
	}

	xMu := mean(xs)
	yMu := mean(ys)

	// Intentional improvement over Java's exact == 0.0 comparison;
	// floating-point mean of cos/sin values is unlikely to be exactly zero.
	if math.Abs(xMu) < 1e-6 && math.Abs(yMu) < 1e-6 {
		return "", nil
	}

	// Calculate standard deviation and compare against threshold
	xVariance := variance(xs, xMu)
	yVariance := variance(ys, yMu)

	xStdDev := math.Sqrt(xVariance)
	yStdDev := math.Sqrt(yVariance)
	if xStdDev > adc.standardDeviationThreshold || yStdDev > adc.standardDeviationThreshold {
		return "", nil // Too much variance
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

	return adc.getAngleAsDirection(thetaMedian), nil
}

// calculateOrientationAtStop calculates the orientation at a stop using a window of shape points
// If distTraveled is < 0, it uses stopLat/stopLon for geographic matching (when shape_dist_traveled is unavailable)
func (adc *AdvancedDirectionCalculator) calculateOrientationAtStop(ctx context.Context, shapeID string, distTraveled float64, stopLat, stopLon float64) (float64, error) {
	var shapePoints []gtfsdb.GetShapePointsWithDistanceRow
	var err error

	adc.cacheMutex.RLock()
	hasCache := adc.shapeCache != nil
	var found bool
	if hasCache {
		shapePoints, found = adc.shapeCache[shapeID]
	}
	adc.cacheMutex.RUnlock()

	// Try cache first if available
	if hasCache {
		if !found || len(shapePoints) < 2 {
			return 0, sql.ErrNoRows
		}
	} else {
		// Fall back to database query if no cache
		adc.queriesMu.RLock()
		q := adc.queries
		adc.queriesMu.RUnlock()
		shapePoints, err = q.GetShapePointsWithDistance(ctx, shapeID)
		if err != nil {
			return 0, err
		}
		if len(shapePoints) < 2 {
			// Insufficient points is a data condition, not a transient error.
			// Return ErrNoRows so the caller treats this the same as "no shape data".
			return 0, sql.ErrNoRows
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
	if indexTo >= len(shapePoints) {
		indexTo = len(shapePoints) - 1
	}

	// Calculate orientation from the window using flat-earth approximation
	if indexTo > indexFrom {
		fromPoint := shapePoints[indexFrom]
		toPoint := shapePoints[indexTo]

		dx := (toPoint.Lon - fromPoint.Lon) * math.Cos(fromPoint.Lat*math.Pi/180.0)
		dy := toPoint.Lat - fromPoint.Lat

		orientation := math.Atan2(dy, dx)
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
	return sumSquares / float64(len(values)-1)
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
