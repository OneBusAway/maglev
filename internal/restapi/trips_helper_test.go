package restapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// TestDistanceToLineSegment tests the helper function that calculates distance from a point to a line segment
func TestDistanceToLineSegment(t *testing.T) {
	tests := []struct {
		name             string
		px, py           float64 // point coordinates
		x1, y1, x2, y2   float64 // line segment endpoints
		expectedRatioMin float64 // minimum expected ratio (for ranges)
		expectedRatioMax float64 // maximum expected ratio (for ranges)
		expectedRatio    float64 // expected ratio (for exact matches)
		description      string
	}{
		{
			name:          "Point projects onto middle of segment",
			px:            0.5,
			py:            1.0,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            0.0,
			expectedRatio: 0.5,
			description:   "Point above middle of horizontal segment should project to middle (t=0.5)",
		},
		{
			name:          "Point projects onto start of segment",
			px:            0.0,
			py:            1.0,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            0.0,
			expectedRatio: 0.0,
			description:   "Point above start should project to start (t=0.0)",
		},
		{
			name:          "Point projects onto end of segment",
			px:            1.0,
			py:            1.0,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            0.0,
			expectedRatio: 1.0,
			description:   "Point above end should project to end (t=1.0)",
		},
		{
			name:          "Point beyond start is clamped",
			px:            -1.0,
			py:            0.0,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            0.0,
			expectedRatio: 0.0,
			description:   "Point beyond start should clamp to start (t=0.0)",
		},
		{
			name:          "Point beyond end is clamped",
			px:            2.0,
			py:            0.0,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            0.0,
			expectedRatio: 1.0,
			description:   "Point beyond end should clamp to end (t=1.0)",
		},
		{
			name:          "Vertical segment",
			px:            1.0,
			py:            0.5,
			x1:            0.0,
			y1:            0.0,
			x2:            0.0,
			y2:            1.0,
			expectedRatio: 0.5,
			description:   "Point beside middle of vertical segment should project to middle",
		},
		{
			name:          "Diagonal segment",
			px:            0.5,
			py:            0.5,
			x1:            0.0,
			y1:            0.0,
			x2:            1.0,
			y2:            1.0,
			expectedRatio: 0.5,
			description:   "Point on diagonal line should project correctly",
		},
		{
			name:          "Zero-length segment (point)",
			px:            1.0,
			py:            1.0,
			x1:            0.0,
			y1:            0.0,
			x2:            0.0,
			y2:            0.0,
			expectedRatio: 0.0,
			description:   "Zero-length segment should return ratio 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			distance, ratio := distanceToLineSegment(tt.px, tt.py, tt.x1, tt.y1, tt.x2, tt.y2)

			// Verify ratio is correct
			assert.InDelta(t, tt.expectedRatio, ratio, 0.001, "Ratio should match expected value: %s", tt.description)

			// Verify ratio is within valid range [0, 1]
			assert.GreaterOrEqual(t, ratio, 0.0, "Ratio should be >= 0")
			assert.LessOrEqual(t, ratio, 1.0, "Ratio should be <= 1")

			// Verify distance is non-negative
			assert.GreaterOrEqual(t, distance, 0.0, "Distance should be non-negative")
		})
	}
}

// TestDistanceToLineSegment_GeographicCoordinates tests with realistic lat/lon coordinates
func TestDistanceToLineSegment_GeographicCoordinates(t *testing.T) {
	tests := []struct {
		name          string
		stopLat       float64
		stopLon       float64
		shapeLat1     float64
		shapeLon1     float64
		shapeLat2     float64
		shapeLon2     float64
		expectedRatio float64
		description   string
	}{
		{
			name:          "Stop near middle of route segment",
			stopLat:       40.5900,
			stopLon:       -122.3900,
			shapeLat1:     40.5890,
			shapeLon1:     -122.3890,
			shapeLat2:     40.5910,
			shapeLon2:     -122.3910,
			expectedRatio: 0.5,
			description:   "Stop near midpoint of diagonal segment",
		},
		{
			name:          "Stop near start of route segment",
			stopLat:       40.5891,
			stopLon:       -122.3891,
			shapeLat1:     40.5890,
			shapeLon1:     -122.3890,
			shapeLat2:     40.5910,
			shapeLon2:     -122.3910,
			expectedRatio: 0.1,
			description:   "Stop near start of segment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			distance, ratio := distanceToLineSegment(
				tt.stopLat, tt.stopLon,
				tt.shapeLat1, tt.shapeLon1,
				tt.shapeLat2, tt.shapeLon2,
			)

			// For geographic coordinates, we expect the ratio to be approximately correct
			// but not exact due to the Haversine approximation
			assert.InDelta(t, tt.expectedRatio, ratio, 0.15, "Ratio should be approximately correct: %s", tt.description)
			assert.GreaterOrEqual(t, distance, 0.0, "Distance should be non-negative")
		})
	}
}

// TestCalculatePreciseDistanceAlongTrip tests the main distance calculation function
func TestCalculatePreciseDistanceAlongTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Get a test trip with shape data
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "Should have test trips")

	var testTripID string
	for _, trip := range trips {
		if trip.Shape != nil && len(trip.Shape.Points) > 0 {
			testTripID = trip.ID
			break
		}
	}
	require.NotEmpty(t, testTripID, "Should find a trip with shape data")

	// Get shape points for this trip
	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, testTripID)
	require.NoError(t, err)
	require.NotEmpty(t, shapeRows, "Should have shape points")

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  sp.Lat,
			Longitude: sp.Lon,
		}
	}

	// Get stop times for this trip
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, testTripID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes, "Should have stop times")

	// Test that we can calculate distance for each stop
	var previousDistance float64
	for i, st := range stopTimes {
		distance := api.calculatePreciseDistanceAlongTrip(ctx, st.StopID, shapePoints)

		// Distance should be non-negative
		assert.GreaterOrEqual(t, distance, 0.0, "Distance should be non-negative for stop %d", i)

		// Distance should generally increase along the trip (with some tolerance for slight variations)
		// Note: In some cases, stops might not be in perfect sequential order along the shape,
		// so we allow for some flexibility
		if i > 0 {
			// Allow distance to be slightly less (within 100m) to account for stops not perfectly on the route
			assert.GreaterOrEqual(t, distance, previousDistance-100.0,
				"Distance should generally increase or stay similar along trip (stop %d)", i)
		}

		previousDistance = distance
	}
}

// TestCalculatePreciseDistanceAlongTrip_EdgeCases tests edge cases
func TestCalculatePreciseDistanceAlongTrip_EdgeCases(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	t.Run("Empty shape points", func(t *testing.T) {
		emptyShape := []gtfs.ShapePoint{}
		distance := api.calculatePreciseDistanceAlongTrip(ctx, "any-stop-id", emptyShape)
		assert.Equal(t, 0.0, distance, "Should return 0 for empty shape")
	})

	t.Run("Invalid stop ID", func(t *testing.T) {
		shapePoints := []gtfs.ShapePoint{
			{Latitude: 40.5890, Longitude: -122.3890},
			{Latitude: 40.5900, Longitude: -122.3900},
		}
		distance := api.calculatePreciseDistanceAlongTrip(ctx, "invalid-stop-id", shapePoints)
		assert.Equal(t, 0.0, distance, "Should return 0 for invalid stop ID")
	})

	t.Run("Single shape point", func(t *testing.T) {
		// Get a valid stop
		trips := api.GtfsManager.GetTrips()
		require.NotEmpty(t, trips)

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trips[0].ID)
		require.NoError(t, err)
		require.NotEmpty(t, stopTimes)

		singlePointShape := []gtfs.ShapePoint{
			{Latitude: 40.5890, Longitude: -122.3890},
		}
		distance := api.calculatePreciseDistanceAlongTrip(ctx, stopTimes[0].StopID, singlePointShape)
		assert.Equal(t, 0.0, distance, "Should return 0 for single shape point")
	})
}

// TestCalculatePreciseDistanceAlongTrip_Correctness validates the algorithm correctness
func TestCalculatePreciseDistanceAlongTrip_Correctness(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Create a simple linear shape: three points in a line
	// Point 1: (40.0, -122.0)
	// Point 2: (40.1, -122.0) - 100km north
	// Point 3: (40.2, -122.0) - 200km north of start
	shapePoints := []gtfs.ShapePoint{
		{Latitude: 40.0, Longitude: -122.0},
		{Latitude: 40.1, Longitude: -122.0},
		{Latitude: 40.2, Longitude: -122.0},
	}

	// Get a real stop to test with (we'll use its ID but override the coordinates conceptually)
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trips[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	// Note: We can't actually modify the stop coordinates in the DB for this test,
	// so we're just testing that the function runs and returns reasonable values
	distance := api.calculatePreciseDistanceAlongTrip(ctx, stopTimes[0].StopID, shapePoints)

	// The distance should be reasonable (between 0 and the total trip length)
	// The exact value depends on where the actual stop is located
	assert.GreaterOrEqual(t, distance, 0.0, "Distance should be non-negative")

	// Maximum possible distance would be close to the distance from first to last point
	// which is approximately 200km (in the simple case above, though the real stop might be elsewhere)
	maxPossibleDistance := 1000000.0 // 1000km is a safe upper bound
	assert.LessOrEqual(t, distance, maxPossibleDistance, "Distance should be reasonable")
}

// TestCalculateBatchStopDistances verifies the new Monotonic Search logic
func TestCalculateBatchStopDistances(t *testing.T) {

	api := createTestApi(t)
	defer api.Shutdown()

	// Setup a simple straight line shape (1 meter per point)
	// Point 0: (0,0), Point 1: (0, 0.00001), ...
	shapePoints := make([]gtfs.ShapePoint, 100)
	for i := 0; i < 100; i++ {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  0.0,
			Longitude: float64(i) * 0.00001, // Roughly 1.1 meters per index
		}
	}

	// 2. Setup Stops at known indices
	stopCoords := map[string]struct{ lat, lon float64 }{
		"stop_A": {lat: 0.0, lon: shapePoints[10].Longitude},
		"stop_B": {lat: 0.0, lon: shapePoints[50].Longitude},
		"stop_C": {lat: 0.0, lon: shapePoints[90].Longitude},
	}

	stops := []gtfsdb.StopTime{
		{StopID: "stop_A", ArrivalTime: 100},
		{StopID: "stop_B", ArrivalTime: 200},
		{StopID: "stop_C", ArrivalTime: 300},
	}

	results := api.calculateBatchStopDistances(stops, shapePoints, stopCoords, "agency_1")

	assert.Equal(t, 3, len(results), "Should return 3 results")

	// Distance A should be roughly the distance to index 10
	// Distance B should be roughly the distance to index 50
	// Distance C should be roughly the distance to index 90
	assert.Greater(t, results[1].DistanceAlongTrip, results[0].DistanceAlongTrip, "Stop B should be further than Stop A")
	assert.Greater(t, results[2].DistanceAlongTrip, results[1].DistanceAlongTrip, "Stop C should be further than Stop B")

	assert.NotZero(t, results[0].DistanceAlongTrip, "Distance should not be zero")
}

// TestCalculatePreciseDistanceAlongTripWithCoords_Validation tests input validation
func TestCalculatePreciseDistanceAlongTripWithCoords_Validation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("Mismatched array sizes", func(t *testing.T) {
		shapePoints := []gtfs.ShapePoint{
			{Latitude: 40.0, Longitude: -122.0},
			{Latitude: 40.1, Longitude: -122.0},
			{Latitude: 40.2, Longitude: -122.0},
		}
		// Wrong size - should have 3 elements, not 2
		cumulativeDistances := []float64{0.0, 100.0}

		distance := api.calculatePreciseDistanceAlongTripWithCoords(
			40.05, -122.0, shapePoints, cumulativeDistances,
		)

		assert.Equal(t, 0.0, distance, "Should return 0 for mismatched array sizes")
	})

	t.Run("Less than 2 shape points", func(t *testing.T) {
		shapePoints := []gtfs.ShapePoint{
			{Latitude: 40.0, Longitude: -122.0},
		}
		cumulativeDistances := []float64{0.0}

		distance := api.calculatePreciseDistanceAlongTripWithCoords(
			40.05, -122.0, shapePoints, cumulativeDistances,
		)

		assert.Equal(t, 0.0, distance, "Should return 0 for single shape point")
	})

	t.Run("Valid inputs with simple shape", func(t *testing.T) {
		shapePoints := []gtfs.ShapePoint{
			{Latitude: 40.0, Longitude: -122.0},
			{Latitude: 40.1, Longitude: -122.0},
		}
		cumulativeDistances := preCalculateCumulativeDistances(shapePoints)

		// Stop at the midpoint
		distance := api.calculatePreciseDistanceAlongTripWithCoords(
			40.05, -122.0, shapePoints, cumulativeDistances,
		)

		assert.Greater(t, distance, 0.0, "Should calculate a positive distance")
		// The stop is roughly at the midpoint, so distance should be approximately half the total
		totalDistance := cumulativeDistances[len(cumulativeDistances)-1]
		assert.InDelta(t, totalDistance/2, distance, totalDistance*0.2,
			"Distance should be approximately half for midpoint stop")
	})
}

// TestBuildStopTimesList_ErrorHandling tests error handling when batch query fails
func TestBuildStopTimesList_ErrorHandling(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Get real stop times to work with
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trips[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	// Get shape points
	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, trips[0].ID)
	require.NoError(t, err)

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  sp.Lat,
			Longitude: sp.Lon,
		}
	}

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	t.Run("Normal operation - coordinates found", func(t *testing.T) {
		result := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)

		assert.NotEmpty(t, result, "Should return stop times")
		assert.Equal(t, len(stopTimes), len(result), "Should return same number of stop times")

		// At least some stops should have non-zero distances if shape data is available
		hasNonZeroDistance := false
		for _, st := range result {
			if st.DistanceAlongTrip > 0 {
				hasNonZeroDistance = true
				break
			}
		}
		assert.True(t, hasNonZeroDistance, "At least some stops should have calculated distances")
	})

	t.Run("With invalid stop IDs - graceful degradation", func(t *testing.T) {
		// Create stop times with invalid IDs that won't be found
		invalidStopTimes := []gtfsdb.StopTime{
			{StopID: "invalid-stop-1", ArrivalTime: 100, DepartureTime: 100},
			{StopID: "invalid-stop-2", ArrivalTime: 200, DepartureTime: 200},
		}

		result := buildStopTimesList(api, ctx, invalidStopTimes, shapePoints, agencyID)

		assert.NotEmpty(t, result, "Should still return results")
		assert.Equal(t, len(invalidStopTimes), len(result), "Should return same number of stop times")

		// All distances should be 0 since stops weren't found
		for _, st := range result {
			assert.Equal(t, 0.0, st.DistanceAlongTrip, "Distance should be 0 for unfound stops")
		}
	})

	t.Run("Empty shape points - all distances zero", func(t *testing.T) {
		emptyShape := []gtfs.ShapePoint{}

		result := buildStopTimesList(api, ctx, stopTimes, emptyShape, agencyID)

		assert.NotEmpty(t, result, "Should return stop times")
		assert.Equal(t, len(stopTimes), len(result), "Should return same number of stop times")

		// All distances should be 0 with no shape data
		for _, st := range result {
			assert.Equal(t, 0.0, st.DistanceAlongTrip, "Distance should be 0 with no shape")
		}
	})
}

func TestBuildTripStatus_VehicleIDFormat(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyStatic := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := trips[0].ID
	agencyID := agencyStatic.Id
	vehicleID := "MOCK_VEHICLE_1"
	routeID := utils.FormCombinedID(agencyID, trips[0].Route.Id)

	api.GtfsManager.MockAddAgency(agencyID, "unitrans")
	api.GtfsManager.MockAddRoute(routeID, agencyID, routeID)
	api.GtfsManager.MockAddTrip(tripID, agencyID, routeID)
	api.GtfsManager.MockAddVehicle(vehicleID, tripID, routeID)
	ctx := context.Background()

	currentTime := time.Now()
	model, err := api.BuildTripStatus(ctx, agencyID, tripID, currentTime, currentTime)

	assert.NoError(t, err)
	assert.NotEmpty(t, model)
	assert.Equal(t, utils.FormCombinedID(agencyID, vehicleID), model.VehicleID)
}

// BenchmarkDistanceToLineSegment benchmarks the line segment distance calculation
func BenchmarkDistanceToLineSegment(b *testing.B) {
	px, py := 0.5, 1.0
	x1, y1 := 0.0, 0.0
	x2, y2 := 1.0, 0.0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = distanceToLineSegment(px, py, x1, y1, x2, y2)
	}
}

// BenchmarkCalculatePreciseDistanceAlongTrip benchmarks the full distance calculation
func BenchmarkCalculatePreciseDistanceAlongTrip(b *testing.B) {
	t := &testing.T{}
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Find a trip with shape data
	trips := api.GtfsManager.GetTrips()
	if len(trips) == 0 {
		b.Skip("No trips available for benchmark")
	}

	var testTripID string
	for _, trip := range trips {
		if trip.Shape != nil && len(trip.Shape.Points) > 0 {
			testTripID = trip.ID
			break
		}
	}

	if testTripID == "" {
		b.Skip("No trips with shape data available")
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, testTripID)
	if err != nil || len(shapeRows) == 0 {
		b.Skip("No shape points available")
	}

	shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
	for i, sp := range shapeRows {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  sp.Lat,
			Longitude: sp.Lon,
		}
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, testTripID)
	if err != nil || len(stopTimes) == 0 {
		b.Skip("No stop times available")
	}

	stopID := stopTimes[0].StopID

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = api.calculatePreciseDistanceAlongTrip(ctx, stopID, shapePoints)
	}
}

// BenchmarkBuildTripSchedule benchmarks the full trip schedule building (includes all distance calculations)
func BenchmarkBuildTripSchedule(b *testing.B) {
	t := &testing.T{}
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Find a trip
	trips := api.GtfsManager.GetTrips()
	if len(trips) == 0 {
		b.Skip("No trips available")
	}

	trip := trips[0]
	tripRow, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
	if err != nil {
		b.Skip("Could not get trip")
	}

	agencies := api.GtfsManager.GetAgencies()
	if len(agencies) == 0 {
		b.Skip("No agencies available")
	}
	agencyID := agencies[0].Id

	// Get timezone for service date
	loc, err := time.LoadLocation(agencies[0].Timezone)
	if err != nil {
		b.Skip("Could not get timezone")
	}

	serviceDate := time.Now().In(loc)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = api.BuildTripSchedule(ctx, agencyID, serviceDate, &tripRow, loc)
	}
}

// BenchmarkBuildTripSchedule_VaryingShapeSize benchmarks with different shape sizes
func BenchmarkBuildTripSchedule_VaryingShapeSize(b *testing.B) {
	t := &testing.T{}
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips := api.GtfsManager.GetTrips()
	if len(trips) == 0 {
		b.Skip("No trips available")
	}

	agencies := api.GtfsManager.GetAgencies()
	if len(agencies) == 0 {
		b.Skip("No agencies available")
	}
	agencyID := agencies[0].Id

	loc, err := time.LoadLocation(agencies[0].Timezone)
	if err != nil {
		b.Skip("Could not get timezone")
	}
	serviceDate := time.Now().In(loc)

	// Find trips with different numbers of shape points
	type tripInfo struct {
		trip        *gtfsdb.Trip
		shapePoints int
	}

	var testTrips []tripInfo

	for _, trip := range trips {
		tripRow, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
		if err != nil {
			continue
		}

		shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, trip.ID)
		if err != nil || len(shapeRows) == 0 {
			continue
		}

		testTrips = append(testTrips, tripInfo{
			trip:        &tripRow,
			shapePoints: len(shapeRows),
		})

		if len(testTrips) >= 5 {
			break
		}
	}

	if len(testTrips) == 0 {
		b.Skip("No trips with shape data available")
	}

	for _, ti := range testTrips {
		b.Run(fmt.Sprintf("ShapePoints_%d", ti.shapePoints), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = api.BuildTripSchedule(ctx, agencyID, serviceDate, ti.trip, loc)
			}
		})
	}
}

// Helper to generate large datasets for benchmarking
func generateBenchmarkData() ([]gtfs.ShapePoint, []gtfsdb.StopTime, map[string]struct{ lat, lon float64 }) {
	shapeSize := 10000 // 10k shape points
	stopsSize := 100   // 100 stops

	shapePoints := make([]gtfs.ShapePoint, shapeSize)
	for i := 0; i < shapeSize; i++ {
		shapePoints[i] = gtfs.ShapePoint{
			Latitude:  40.0 + (float64(i) * 0.0001),
			Longitude: -74.0 + (float64(i) * 0.0001),
		}
	}

	stopTimes := make([]gtfsdb.StopTime, stopsSize)
	stopCoords := make(map[string]struct{ lat, lon float64 })

	for i := 0; i < stopsSize; i++ {
		stopID := fmt.Sprintf("stop_%d", i)
		// Place stops sequentially along the route
		idx := i * (shapeSize / stopsSize)

		stopTimes[i] = gtfsdb.StopTime{StopID: stopID}
		stopCoords[stopID] = struct{ lat, lon float64 }{
			lat: shapePoints[idx].Latitude,
			lon: shapePoints[idx].Longitude,
		}
	}

	return shapePoints, stopTimes, stopCoords
}

// BENCHMARK OLD WAY (Simulating the loop over O(M) function)
func BenchmarkLegacy_LinearScan(b *testing.B) {
	api := &RestAPI{}
	shape, stops, coords := generateBenchmarkData()

	// Pre-calc happens once in the handler
	cumDist := preCalculateCumulativeDistances(shape)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate the handler loop
		for _, st := range stops {
			if c, ok := coords[st.StopID]; ok {
				// Each call scans from 0 -> O(M)
				api.calculatePreciseDistanceAlongTripWithCoords(c.lat, c.lon, shape, cumDist)
			}
		}
	}
}

// BenchmarkOptimized_MonotonicBatch benchmarks the optimized batch distance calculation
func BenchmarkOptimized_MonotonicBatch(b *testing.B) {
	api := &RestAPI{}
	shape, stops, coords := generateBenchmarkData()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Single call handles all logic -> O(N+M)
		api.calculateBatchStopDistances(stops, shape, coords, "agency_1")
	}
}

func TestGetDistanceAlongShape_Projection(t *testing.T) {
	shape := []gtfs.ShapePoint{
		{Latitude: 0.0, Longitude: 0.0},
		{Latitude: 0.01, Longitude: 0.0},
	}

	vehicleLat := 0.005
	vehicleLon := 0.0001

	expectedDist := utils.Distance(0.0, 0.0, 0.005, 0.0)

	actualDist := getDistanceAlongShape(vehicleLat, vehicleLon, shape)

	assert.InDelta(t, expectedDist, actualDist, 1.0,
		"Distance calculation should use projection logic, not vertex snapping")
}

func TestGetDistanceAlongShape_LoopingRoute(t *testing.T) {
	shape := []gtfs.ShapePoint{
		{Latitude: 0.0, Longitude: 0.0},
		{Latitude: 0.01, Longitude: 0.0},
		{Latitude: 0.01, Longitude: 0.01},
		{Latitude: 0.0, Longitude: 0.01},
		{Latitude: 0.0001, Longitude: 0.0001},
	}

	vehicleLat := 0.00005
	vehicleLon := 0.0

	expectedDist := utils.Distance(0.0, 0.0, vehicleLat, vehicleLon)

	actualDist := getDistanceAlongShapeInRange(vehicleLat, vehicleLon, shape, 0, 100)

	assert.InDelta(t, expectedDist, actualDist, 5.0,
		"Should identify distance at the start of the loop, not jump to the end")
}
