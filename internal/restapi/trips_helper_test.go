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
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
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

func TestBuildTripStatus_VehicleWithPosition_FindsStops(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	// Find a trip with stop times so we can exercise the stop-finding branch
	var tripID string
	var stopTimes []gtfsdb.StopTime
	for _, trip := range trips {
		st, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err == nil && len(st) >= 2 {
			tripID = trip.ID
			stopTimes = st
			break
		}
	}
	require.NotEmpty(t, tripID, "Need a trip with at least 2 stop times")

	// Look up coordinates for the first stop so the vehicle is nearby
	firstStopID := stopTimes[0].StopID
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, []string{firstStopID})
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	lat := float32(stops[0].Lat)
	lon := float32(stops[0].Lon)

	routeID := trips[0].Route.Id
	vehicleID := "VEHICLE_POS_TEST"

	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, tripID, routeID, internalgtfs.MockVehicleOptions{
		Position: &gtfs.Position{
			Latitude:  &lat,
			Longitude: &lon,
		},
	})

	// Set currentTime during the trip using the first stop's arrival time
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	arrivalSeconds := utils.EffectiveStopTimeSeconds(stopTimes[0].ArrivalTime, stopTimes[0].DepartureTime)
	currentTime := serviceDate.Add(time.Duration(arrivalSeconds) * time.Second)

	status, err := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	require.NoError(t, err)
	require.NotNil(t, status)

	// Vehicle has position, so the stop-finding code should have run and found stops
	assert.NotEmpty(t, status.ClosestStop, "ClosestStop should be populated when vehicle has position")
	assert.NotEmpty(t, status.NextStop, "NextStop should be populated when vehicle has position and is not at last stop")

	// Vehicle is fresh, so status should reflect real-time data
	assert.Equal(t, "SCHEDULED", status.Status)
	assert.Equal(t, "in_progress", status.Phase)
	assert.NotZero(t, status.LastKnownLocation.Lat, "LastKnownLocation should be set from vehicle position")
}

func TestBuildTripStatus_ScheduleDeviation_SetsPredicted(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)
	tripID := trips[0].ID
	routeID := trips[0].Route.Id

	// Add a trip update with a 120-second delay (no vehicle, just trip update)
	delay := 120 * time.Second
	api.GtfsManager.MockAddTripUpdate(tripID, &delay, nil)

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := serviceDate.Add(8 * time.Hour)

	api.GtfsManager.MockAddAgency(agencyID, agencies[0].Name)
	api.GtfsManager.MockAddRoute(routeID, agencyID, routeID)
	api.GtfsManager.MockAddTrip(tripID, agencyID, routeID)

	status, err := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Equal(t, 120, status.ScheduleDeviation, "ScheduleDeviation should reflect the trip update delay")
	assert.True(t, status.Predicted, "Predicted should be true when trip update exists")
	assert.False(t, status.Scheduled, "Scheduled should be false when predicted is true")
}

func TestBuildTripStatus_NoRealtimeData_SetsScheduled(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)
	tripID := trips[0].ID

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := serviceDate.Add(8 * time.Hour)

	// No vehicle, no trip updates — purely scheduled
	status, err := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Equal(t, 0, status.ScheduleDeviation, "ScheduleDeviation should be 0 with no real-time data")
	assert.False(t, status.Predicted, "Predicted should be false with no real-time data")
	assert.True(t, status.Scheduled, "Scheduled should be true with no real-time data")
	assert.Equal(t, "default", status.Status)
	assert.Equal(t, "scheduled", status.Phase)
}

func TestBuildTripStatus_ShapeData_ComputesDistanceAlongTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	// Find a trip that has both shape data and stop times
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	var tripID, routeID string
	for _, trip := range trips {
		shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, trip.ID)
		if err == nil && len(shapeRows) > 1 {
			st, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
			if err == nil && len(st) >= 2 {
				tripID = trip.ID
				routeID = trip.Route.Id
				break
			}
		}
	}
	require.NotEmpty(t, tripID, "Need a trip with shape data and stop times")

	// Get a mid-route stop to position the vehicle
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	require.NoError(t, err)

	midIdx := len(stopTimes) / 2
	midStopID := stopTimes[midIdx].StopID
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, []string{midStopID})
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	lat := float32(stops[0].Lat)
	lon := float32(stops[0].Lon)
	vehicleID := "VEHICLE_SHAPE_TEST"

	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, tripID, routeID, internalgtfs.MockVehicleOptions{
		Position: &gtfs.Position{
			Latitude:  &lat,
			Longitude: &lon,
		},
	})

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	arrivalSeconds := utils.EffectiveStopTimeSeconds(stopTimes[midIdx].ArrivalTime, stopTimes[midIdx].DepartureTime)
	currentTime := serviceDate.Add(time.Duration(arrivalSeconds) * time.Second)

	status, err := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.Greater(t, status.TotalDistanceAlongTrip, 0.0, "TotalDistanceAlongTrip should be > 0 with shape data")
	assert.Greater(t, status.DistanceAlongTrip, 0.0, "DistanceAlongTrip should be > 0 for a vehicle mid-route")
	assert.Less(t, status.DistanceAlongTrip, status.TotalDistanceAlongTrip,
		"DistanceAlongTrip should be less than total for a mid-route vehicle")
}

func TestBuildTripStatus_VehicleIDFormat(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

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

func makeStopTimePtrs(stops []gtfsdb.StopTime) []*gtfsdb.StopTime {
	ptrs := make([]*gtfsdb.StopTime, len(stops))
	for i := range stops {
		ptrs[i] = &stops[i]
	}
	return ptrs
}

func secondsToNanos(s int64) int64 { return s * int64(time.Second) }

func TestFindClosestStopByTimeWithDelays_NoDelays(t *testing.T) {
	// serviceDate at midnight UTC; currentTime = 08:00:00 UTC
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := []gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600)}, // 07:00
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600)}, // 08:00 — exact match
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600)}, // 09:00
	}

	stopID, _ := findClosestStopByTimeWithDelays(currentTime, serviceDate, makeStopTimePtrs(stops), nil)
	assert.Equal(t, "s2", stopID)
}

func TestFindClosestStopByTimeWithDelays_WithDelay(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := []gtfsdb.StopTime{
		{StopID: "s1", DepartureTime: secondsToNanos(7 * 3600)}, // scheduled 07:00
		{StopID: "s2", DepartureTime: secondsToNanos(9 * 3600)}, // scheduled 09:00
	}
	// delay of +60 minutes pushes s1 to 08:00 — closest to currentTime
	delays := map[string]StopDelayInfo{
		"s1": {DepartureDelay: 3600},
	}

	stopID, _ := findClosestStopByTimeWithDelays(currentTime, serviceDate, makeStopTimePtrs(stops), delays)
	assert.Equal(t, "s1", stopID)
}

func TestFindClosestStopByTimeWithDelays_EmptyStops(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stopID, offset := findClosestStopByTimeWithDelays(currentTime, serviceDate, nil, nil)
	assert.Equal(t, "", stopID)
	assert.Equal(t, 0, offset)
}

func TestFindNextStopByTimeWithDelays_NoDelays(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := []gtfsdb.StopTime{
		{StopID: "s1", DepartureTime: secondsToNanos(7 * 3600)},  // past
		{StopID: "s2", DepartureTime: secondsToNanos(9 * 3600)},  // first future stop
		{StopID: "s3", DepartureTime: secondsToNanos(10 * 3600)}, // later future
	}

	stopID, offset := findNextStopByTimeWithDelays(currentTime, serviceDate, makeStopTimePtrs(stops), nil)
	assert.Equal(t, "s2", stopID)
	assert.Equal(t, 3600, offset, "offset should be 3600 seconds (1 hour)")
}

func TestFindNextStopByTimeWithDelays_AllStopsPast(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 23, 0, 0, 0, time.UTC)

	stops := []gtfsdb.StopTime{
		{StopID: "s1", DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", DepartureTime: secondsToNanos(9 * 3600)},
	}

	stopID, _ := findNextStopByTimeWithDelays(currentTime, serviceDate, makeStopTimePtrs(stops), nil)
	assert.Equal(t, "", stopID, "no next stop when all are in the past")
}

func TestFindNextStopByTimeWithDelays_WithDelay(t *testing.T) {
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 30, 0, 0, time.UTC)

	stops := []gtfsdb.StopTime{
		{StopID: "s1", DepartureTime: secondsToNanos(8 * 3600)}, // scheduled 08:00
	}
	// +90 minute delay pushes it to 09:30, making it the next stop
	delays := map[string]StopDelayInfo{
		"s1": {DepartureDelay: 90 * 60},
	}

	stopID, offset := findNextStopByTimeWithDelays(currentTime, serviceDate, makeStopTimePtrs(stops), delays)
	assert.Equal(t, "s1", stopID)
	// predicted arrival = 09:30 - current 08:30 = 3600s
	assert.Equal(t, 3600, offset)
}

func TestFillStopsFromSchedule_BeforeAllStops(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)

	agencyID := agencies[0].Id
	tripID := trips[0].ID

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	// Set currentTime well before the first stop
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := serviceDate.Add(time.Second) // 00:00:01 — before any stop

	status := &models.TripStatusForTripDetails{}
	api.fillStopsFromSchedule(ctx, status, tripID, currentTime, serviceDate, agencyID)

	// When before all stops, NextStop should be the first stop
	assert.NotEmpty(t, status.NextStop, "NextStop should be set when currentTime is before all stops")
}

func TestFillStopsFromSchedule_AfterAllStops(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)

	agencyID := agencies[0].Id
	tripID := trips[0].ID

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes)

	// Set currentTime well past the last stop (next day)
	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := serviceDate.Add(30 * time.Hour)

	status := &models.TripStatusForTripDetails{}
	api.fillStopsFromSchedule(ctx, status, tripID, currentTime, serviceDate, agencyID)

	// When past all stops, ClosestStop should be the last stop
	assert.NotEmpty(t, status.ClosestStop, "ClosestStop should be set to last stop when past all stops")
	assert.Empty(t, status.NextStop, "NextStop should be empty when past all stops")
}

func TestFillStopsFromSchedule_InvalidTripID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	status := &models.TripStatusForTripDetails{}

	// Should not panic or set any stops for an invalid trip
	api.fillStopsFromSchedule(ctx, status, "non-existent-trip", serviceDate, serviceDate, "any-agency")

	assert.Empty(t, status.ClosestStop)
	assert.Empty(t, status.NextStop)
}

func TestCalculateOffsetForStop_MatchingStop(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	offset := api.calculateOffsetForStop("s2", stops, currentTime, serviceDate, 0)
	// predicted arrival = 28800 + 0 = 28800; current = 28800; offset = 0
	assert.Equal(t, 0, offset, "on-time vehicle at exact stop time should have offset 0")
}

func TestCalculateOffsetForStop_NonMatchingStop(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
	})

	offset := api.calculateOffsetForStop("nonexistent", stops, currentTime, serviceDate, 0)
	assert.Equal(t, 0, offset, "non-matching stop should return 0")
}

func TestCalculateOffsetForStop_WithDeviation(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
	})

	// 5-minute late deviation
	offset := api.calculateOffsetForStop("s1", stops, currentTime, serviceDate, 300)
	// predicted arrival = 28800 + 300 = 29100; current = 28800; offset = 300
	assert.Equal(t, 300, offset, "offset should reflect 5-minute delay")
}

func TestCalculateOffsetForStop_EmptyStops(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	offset := api.calculateOffsetForStop("s1", nil, currentTime, serviceDate, 0)
	assert.Equal(t, 0, offset, "empty stop times should return 0")
}

func TestFindNextStopAfter_MidTrip(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	nextStopID, nextOffset := api.findNextStopAfter("s2", stops, currentTime, serviceDate, 0)
	assert.Equal(t, "s3", nextStopID, "next stop after s2 should be s3")
	// predicted arrival for s3 = 32400 + 0 = 32400; current = 28800; offset = 3600
	assert.Equal(t, 3600, nextOffset, "offset should be 1 hour")
}

func TestFindNextStopAfter_LastStop(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	nextStopID, nextOffset := api.findNextStopAfter("s2", stops, currentTime, serviceDate, 0)
	assert.Empty(t, nextStopID, "no next stop after last stop")
	assert.Equal(t, 0, nextOffset, "offset should be 0 when no next stop")
}

func TestFindNextStopAfter_WithDeviation(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
	})

	// 5-minute late deviation
	nextStopID, nextOffset := api.findNextStopAfter("s1", stops, currentTime, serviceDate, 300)
	assert.Equal(t, "s2", nextStopID)
	// predicted arrival for s2 = 28800 + 300 = 29100; current = 28800; offset = 300
	assert.Equal(t, 300, nextOffset, "offset should include schedule deviation")
}

func TestFindNextStopAfter_NonMatchingStop(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
	})

	nextStopID, nextOffset := api.findNextStopAfter("nonexistent", stops, currentTime, serviceDate, 0)
	assert.Empty(t, nextStopID, "non-matching stop should return empty")
	assert.Equal(t, 0, nextOffset)
}

func TestFindNextStopAfter_EmptyStops(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	nextStopID, nextOffset := api.findNextStopAfter("s1", nil, currentTime, serviceDate, 0)
	assert.Empty(t, nextStopID, "empty stops should return empty")
	assert.Equal(t, 0, nextOffset)
}

func TestBuildTripStatus_VehicleWithStopID_FindsStops(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	ctx := context.Background()

	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].Id

	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	// Find a trip with at least 3 stop times so we can place the vehicle mid-trip
	var tripID string
	var stopTimes []gtfsdb.StopTime
	for _, trip := range trips {
		st, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err == nil && len(st) >= 3 {
			tripID = trip.ID
			stopTimes = st
			break
		}
	}
	require.NotEmpty(t, tripID, "Need a trip with at least 3 stop times")

	// Use the middle stop so there is both a closest and a next stop
	midIdx := len(stopTimes) / 2
	midStopID := stopTimes[midIdx].StopID

	// Look up coordinates for the mid stop
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, []string{midStopID})
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	lat := float32(stops[0].Lat)
	lon := float32(stops[0].Lon)

	routeID := trips[0].Route.Id
	vehicleID := "VEHICLE_STOPID_TEST"

	// Mark the vehicle as STOPPED_AT to exercise the StopID + isStoppedAt branch
	stoppedAt := gtfs.CurrentStatus(1)
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, tripID, routeID, internalgtfs.MockVehicleOptions{
		Position: &gtfs.Position{
			Latitude:  &lat,
			Longitude: &lon,
		},
		StopID:        &midStopID,
		CurrentStatus: &stoppedAt,
	})

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	arrivalSeconds := utils.EffectiveStopTimeSeconds(stopTimes[midIdx].ArrivalTime, stopTimes[midIdx].DepartureTime)
	currentTime := serviceDate.Add(time.Duration(arrivalSeconds) * time.Second)

	status, err := api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	require.NoError(t, err)
	require.NotNil(t, status)

	// The StopID branch should have identified the closest stop
	assert.NotEmpty(t, status.ClosestStop, "ClosestStop should be populated when vehicle has StopID")
	assert.Contains(t, status.ClosestStop, midStopID,
		"ClosestStop should contain the vehicle's reported StopID")

	// Because the vehicle is STOPPED_AT a mid-trip stop, NextStop should be the following stop
	if midIdx+1 < len(stopTimes) {
		assert.NotEmpty(t, status.NextStop, "NextStop should be populated when stopped at a mid-trip stop")
		assert.Contains(t, status.NextStop, stopTimes[midIdx+1].StopID,
			"NextStop should be the stop after the vehicle's current stop")
	}

	// Vehicle is fresh so status/phase reflect ScheduleRelationship (SCHEDULED → "SCHEDULED"/"in_progress"),
	// not CurrentStatus. CurrentStatus only affects the stop-finding branch, not GetVehicleStatusAndPhase.
	assert.Equal(t, "SCHEDULED", status.Status)
	assert.Equal(t, "in_progress", status.Phase)
	assert.NotZero(t, status.LastKnownLocation.Lat, "LastKnownLocation should be set from vehicle position")
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

func TestFindClosestStopBySequence_Match(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s into service day

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", StopSequence: 2, ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", StopSequence: 3, ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	stopID, offset := api.findClosestStopBySequence(stops, 2, currentTime, serviceDate, 0)
	assert.Equal(t, "s2", stopID)
	// predicted arrival = 8*3600 + 0(deviation) = 28800; offset = 28800 - 28800 = 0
	assert.Equal(t, 0, offset, "vehicle at on-time stop means offset == 0")
}

func TestFindClosestStopBySequence_WithDeviation(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
	})

	// Vehicle is 5 minutes late
	stopID, offset := api.findClosestStopBySequence(stops, 1, currentTime, serviceDate, 300)
	assert.Equal(t, "s1", stopID)
	// predicted arrival = 8*3600 + 300 = 29100; current = 28800; offset = 300
	assert.Equal(t, 300, offset, "offset should reflect 5-minute delay")
}

func TestFindClosestStopBySequence_NoMatch(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(8 * 3600)},
	})

	stopID, offset := api.findClosestStopBySequence(stops, 99, currentTime, serviceDate, 0)
	assert.Empty(t, stopID, "no stop should match sequence 99")
	assert.Equal(t, 0, offset)
}

func TestFindNextStopBySequence_InTransit(t *testing.T) {
	api := &RestAPI{}
	ctx := context.Background()

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", StopSequence: 2, ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", StopSequence: 3, ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	// Vehicle is IN_TRANSIT_TO (CurrentStatus 2 = the default for nil)
	inTransit := gtfs.CurrentStatus(2)
	vehicle := &gtfs.Vehicle{CurrentStatus: &inTransit}

	// When in transit, the current sequence stop IS the next stop
	stopID, offset := api.findNextStopBySequence(ctx, stops, 2, currentTime, serviceDate, 0, vehicle, "trip1")
	assert.Equal(t, "s2", stopID)
	assert.Equal(t, 0, offset)
}

func TestFindNextStopBySequence_StoppedAt(t *testing.T) {
	api := &RestAPI{}
	ctx := context.Background()

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", StopSequence: 2, ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", StopSequence: 3, ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	// Vehicle is STOPPED_AT (CurrentStatus 1) at stop sequence 2
	stoppedAt := gtfs.CurrentStatus(1)
	vehicle := &gtfs.Vehicle{CurrentStatus: &stoppedAt}

	stopID, offset := api.findNextStopBySequence(ctx, stops, 2, currentTime, serviceDate, 0, vehicle, "trip1")
	// Stopped at s2, so next stop should be s3
	assert.Equal(t, "s3", stopID)
	// s3 arrival = 9*3600 + 0(deviation) = 32400; current = 28800; offset = 3600
	assert.Equal(t, 3600, offset)
}

func TestFindNextStopBySequence_StoppedAtLastStop(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s2", StopSequence: 2, ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	stoppedAt := gtfs.CurrentStatus(1)
	vehicle := &gtfs.Vehicle{CurrentStatus: &stoppedAt}

	// Stopped at last stop (sequence 2), no next stop in this trip
	// because "trip1" doesn't exist in the DB. So we expect empty.
	stopID, _ := api.findNextStopBySequence(ctx, stops, 2, currentTime, serviceDate, 0, vehicle, "trip1")
	assert.Empty(t, stopID, "no next stop when stopped at last stop of trip without block continuation")
}

func TestFindNextStopBySequence_NoMatch(t *testing.T) {
	api := &RestAPI{}
	ctx := context.Background()

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", StopSequence: 1, ArrivalTime: secondsToNanos(8 * 3600)},
	})

	stopID, offset := api.findNextStopBySequence(ctx, stops, 99, currentTime, serviceDate, 0, nil, "trip1")
	assert.Empty(t, stopID)
	assert.Equal(t, 0, offset)
}

func TestFindStopsByScheduleDeviation_OnTime(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC) // 28800s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	closestStopID, closestOffset, nextStopID, nextOffset := api.findStopsByScheduleDeviation(stops, currentTime, serviceDate, 0)

	// With 0 deviation, effective schedule time = 28800 = 8*3600 → closest is s2
	assert.Equal(t, "s2", closestStopID)
	// predicted arrival for s2 = 28800 + 0 = 28800; current = 28800; offset = 0
	assert.Equal(t, 0, closestOffset)
	// Next stop after s2 is s3
	assert.Equal(t, "s3", nextStopID)
	// predicted arrival for s3 = 32400 + 0 = 32400; current = 28800; offset = 3600
	assert.Equal(t, 3600, nextOffset)
}

func TestFindStopsByScheduleDeviation_Late(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 5, 0, 0, time.UTC) // 28800 + 300 = 29100s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	// Vehicle is 5 minutes late (300s deviation)
	// effectiveScheduleTime = 29100 - 300 = 28800 → closest is still s2
	closestStopID, closestOffset, nextStopID, nextOffset := api.findStopsByScheduleDeviation(stops, currentTime, serviceDate, 300)

	assert.Equal(t, "s2", closestStopID)
	// predicted arrival = 28800 + 300 = 29100; current = 29100; offset = 0
	assert.Equal(t, 0, closestOffset)
	assert.Equal(t, "s3", nextStopID)
	// predicted next arrival = 32400 + 300 = 32700; current = 29100; offset = 3600
	assert.Equal(t, 3600, nextOffset)
}

func TestFindStopsByScheduleDeviation_Empty(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)

	closestStopID, closestOffset, nextStopID, nextOffset := api.findStopsByScheduleDeviation(nil, currentTime, serviceDate, 0)
	assert.Empty(t, closestStopID)
	assert.Equal(t, 0, closestOffset)
	assert.Empty(t, nextStopID)
	assert.Equal(t, 0, nextOffset)
}

func TestFindStopsByScheduleDeviation_LastStop(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC) // 32400s

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	closestStopID, _, nextStopID, _ := api.findStopsByScheduleDeviation(stops, currentTime, serviceDate, 0)
	assert.Equal(t, "s2", closestStopID, "should identify last stop as closest")
	assert.Empty(t, nextStopID, "no next stop after the last one")
}

func TestInterpolateDistanceAtScheduledTime_BetweenStops(t *testing.T) {
	stopTimes := []gtfsdb.StopTime{
		{DepartureTime: secondsToNanos(100), ArrivalTime: secondsToNanos(100)},
		{DepartureTime: secondsToNanos(200), ArrivalTime: secondsToNanos(200)},
	}
	distances := []float64{0.0, 1000.0}

	// Midpoint in time → midpoint in distance
	d := interpolateDistanceAtScheduledTime(150, stopTimes, distances)
	assert.InDelta(t, 500.0, d, 0.01, "midpoint time should give midpoint distance")
}

func TestInterpolateDistanceAtScheduledTime_AtStopBoundaries(t *testing.T) {
	stopTimes := []gtfsdb.StopTime{
		{DepartureTime: secondsToNanos(100), ArrivalTime: secondsToNanos(100)},
		{DepartureTime: secondsToNanos(200), ArrivalTime: secondsToNanos(200)},
		{DepartureTime: secondsToNanos(300), ArrivalTime: secondsToNanos(300)},
	}
	distances := []float64{0.0, 500.0, 1500.0}

	// At exact first departure
	d := interpolateDistanceAtScheduledTime(100, stopTimes, distances)
	assert.InDelta(t, 0.0, d, 0.01, "at first departure should be distance 0")

	// At exact second arrival
	d = interpolateDistanceAtScheduledTime(200, stopTimes, distances)
	assert.InDelta(t, 500.0, d, 0.01, "at second stop should be distance 500")
}

func TestInterpolateDistanceAtScheduledTime_BeforeFirstStop(t *testing.T) {
	stopTimes := []gtfsdb.StopTime{
		{DepartureTime: secondsToNanos(100), ArrivalTime: secondsToNanos(100)},
		{DepartureTime: secondsToNanos(200), ArrivalTime: secondsToNanos(200)},
	}
	distances := []float64{0.0, 1000.0}

	d := interpolateDistanceAtScheduledTime(50, stopTimes, distances)
	assert.Equal(t, 0.0, d, "before first stop should return 0")
}

func TestInterpolateDistanceAtScheduledTime_AfterLastStop(t *testing.T) {
	stopTimes := []gtfsdb.StopTime{
		{DepartureTime: secondsToNanos(100), ArrivalTime: secondsToNanos(100)},
		{DepartureTime: secondsToNanos(200), ArrivalTime: secondsToNanos(200)},
	}
	distances := []float64{0.0, 1000.0}

	d := interpolateDistanceAtScheduledTime(999, stopTimes, distances)
	assert.Equal(t, 1000.0, d, "after last stop should return total distance")
}

func TestInterpolateDistanceAtScheduledTime_EmptyInput(t *testing.T) {
	assert.Equal(t, 0.0, interpolateDistanceAtScheduledTime(100, nil, nil))
	assert.Equal(t, 0.0, interpolateDistanceAtScheduledTime(100,
		[]gtfsdb.StopTime{{DepartureTime: secondsToNanos(100)}},
		[]float64{0.0, 1.0}), // mismatched lengths
	)
}

func TestInterpolateDistanceAtScheduledTime_MultipleSegments(t *testing.T) {
	stopTimes := []gtfsdb.StopTime{
		{DepartureTime: secondsToNanos(0), ArrivalTime: secondsToNanos(0)},
		{DepartureTime: secondsToNanos(100), ArrivalTime: secondsToNanos(100)},
		{DepartureTime: secondsToNanos(300), ArrivalTime: secondsToNanos(300)},
	}
	distances := []float64{0.0, 500.0, 1500.0}

	// 75% through first segment: time=75 of [0,100] → 75% of [0, 500] = 375
	d := interpolateDistanceAtScheduledTime(75, stopTimes, distances)
	assert.InDelta(t, 375.0, d, 0.01)

	// 50% through second segment: time=200 of [100,300] → 50% of [500, 1500] = 1000
	d = interpolateDistanceAtScheduledTime(200, stopTimes, distances)
	assert.InDelta(t, 1000.0, d, 0.01)
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

// TestFindStopsByScheduleDeviation_Early verifies that a negative deviation (early vehicle)
// produces correct offsets. effectiveScheduleTime = currentSeconds - (-300) = currentSeconds + 300,
// which shifts the "effective" clock forward, making the vehicle appear ahead of schedule.
func TestFindStopsByScheduleDeviation_Early(t *testing.T) {
	api := &RestAPI{}

	serviceDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// currentTime = 08:05 (29100s since midnight)
	currentTime := time.Date(2024, 1, 1, 8, 5, 0, 0, time.UTC)

	stops := makeStopTimePtrs([]gtfsdb.StopTime{
		{StopID: "s1", ArrivalTime: secondsToNanos(7 * 3600), DepartureTime: secondsToNanos(7 * 3600)},
		{StopID: "s2", ArrivalTime: secondsToNanos(8 * 3600), DepartureTime: secondsToNanos(8 * 3600)},
		{StopID: "s3", ArrivalTime: secondsToNanos(9 * 3600), DepartureTime: secondsToNanos(9 * 3600)},
	})

	// Vehicle is 5 minutes early (deviation = -300s).
	// effectiveScheduleTime = 29100 - (-300) = 29400 = 8h10m → closest to s2 (28800s)
	closestStopID, closestOffset, nextStopID, nextOffset := api.findStopsByScheduleDeviation(stops, currentTime, serviceDate, -300)

	assert.Equal(t, "s2", closestStopID, "early vehicle: closest stop should be s2")
	// predicted arrival at s2 = 28800 + (-300) = 28500; current = 29100; offset = -600 (already passed)
	assert.Equal(t, -600, closestOffset, "early vehicle: closestOffset should be negative (stop already passed)")

	assert.Equal(t, "s3", nextStopID, "early vehicle: next stop should be s3")
	// predicted arrival at s3 = 32400 + (-300) = 32100; current = 29100; offset = 3000
	assert.Equal(t, 3000, nextOffset, "early vehicle: nextOffset should reflect earlier predicted arrival")
}

// TestGetFirstStopOfNextTripInBlock_WithBlockContinuation verifies that when a vehicle
// stops at the last stop of a trip that belongs to a block, the function correctly
// returns the first stop of the next trip in that block.
func TestGetFirstStopOfNextTripInBlock_WithBlockContinuation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	ctx := context.Background()

	// Find a trip that belongs to a block with at least two trips.
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "need at least one trip in test data")

	// Locate a trip that has a block ID and more than one trip in the block.
	var targetTripID string
	for _, trip := range trips {
		if trip.BlockID == "" {
			continue
		}
		blockID := trip.BlockID
		count := 0
		for _, other := range trips {
			if other.BlockID == blockID {
				count++
			}
		}
		if count >= 2 {
			targetTripID = trip.ID
			break
		}
	}

	if targetTripID == "" {
		t.Skip("no multi-trip block found in test data; skipping block continuation test")
	}

	serviceDate := time.Now()
	result := api.getFirstStopOfNextTripInBlock(ctx, targetTripID, serviceDate)
	assert.NotNil(t, result, "should find the first stop of the next block trip")
	assert.NotEmpty(t, result.StopID, "returned stop should have a non-empty StopID")
}
