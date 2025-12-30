package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func TestNewDirectionCalculator(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	require.NotNil(t, dc)
	assert.NotNil(t, dc.gtfsManager)
}

func TestCalculateStopDirection_PrecomputedDirection(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Insert a stop with a precomputed direction
	_, err := client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon, direction)
		VALUES ('STOP1', 'Test Stop 1', 40.7128, -74.0060, 'N')
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.CalculateStopDirection(ctx, "STOP1")

	assert.Equal(t, "N", direction)
}

func TestCalculateStopDirection_NoPrecomputedDirection_FallsBackToShape(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Insert agency, route, trip with shape
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AGENCY1', 'Test Agency', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('ROUTE1', 'AGENCY1', 'R1', 'Route 1', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO shapes (shape_id, lat, lon, shape_pt_sequence)
		VALUES
			('SHAPE1', 40.7128, -74.0060, 1),
			('SHAPE1', 40.7228, -74.0060, 2),
			('SHAPE1', 40.7328, -74.0060, 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SERVICE1', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id, shape_id)
		VALUES ('TRIP1', 'ROUTE1', 'SERVICE1', 'SHAPE1')
	`)
	require.NoError(t, err)

	// Insert stop without precomputed direction
	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP2', 'Test Stop 2', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	// Insert stop_time
	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('TRIP1', 28800000000000, 28800000000000, 'STOP2', 1)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.CalculateStopDirection(ctx, "STOP2")

	// Should calculate direction from shape (northbound based on coordinates)
	assert.Equal(t, "N", direction)
}

func TestCalculateStopDirection_NoShapeData_FallsBackToNextStop(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Insert agency, route, trip without shape
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AGENCY2', 'Test Agency 2', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('ROUTE2', 'AGENCY2', 'R2', 'Route 2', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SERVICE2', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('TRIP2', 'ROUTE2', 'SERVICE2')
	`)
	require.NoError(t, err)

	// Insert two stops
	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES
			('STOP3', 'Test Stop 3', 40.7128, -74.0060),
			('STOP4', 'Test Stop 4', 40.7228, -74.0060)
	`)
	require.NoError(t, err)

	// Insert stop_times
	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES
			('TRIP2', 28800000000000, 28800000000000, 'STOP3', 1),
			('TRIP2', 28900000000000, 28900000000000, 'STOP4', 2)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.CalculateStopDirection(ctx, "STOP3")

	// Should calculate direction from next stop (northbound)
	assert.Equal(t, "N", direction)
}

func TestCalculateStopDirection_NoData_ReturnsUnknown(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.CalculateStopDirection(ctx, "NONEXISTENT")

	assert.Equal(t, models.UnknownValue, direction)
}

func TestCalculateStopDirection_StopWithoutTrips_ReturnsUnknown(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Insert a stop with no trips
	_, err := client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_ALONE', 'Isolated Stop', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.CalculateStopDirection(ctx, "STOP_ALONE")

	assert.Equal(t, models.UnknownValue, direction)
}

func TestFindClosestShapePoint_EmptyPoints(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	result := dc.findClosestShapePoint([]gtfsdb.GetShapePointsForTripRow{}, 40.7128, -74.0060)

	assert.Equal(t, -1, result)
}

func TestFindClosestShapePoint_SinglePoint(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	points := []gtfsdb.GetShapePointsForTripRow{
		{Lat: 40.7128, Lon: -74.0060},
	}

	result := dc.findClosestShapePoint(points, 40.7128, -74.0060)

	assert.Equal(t, 0, result)
}

func TestFindClosestShapePoint_MultiplePoints(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	points := []gtfsdb.GetShapePointsForTripRow{
		{Lat: 40.7128, Lon: -74.0060}, // Point 0 - far
		{Lat: 40.7500, Lon: -74.0000}, // Point 1 - closest
		{Lat: 40.8000, Lon: -73.9000}, // Point 2 - far
	}

	// Test point close to point 1
	result := dc.findClosestShapePoint(points, 40.7510, -74.0010)

	assert.Equal(t, 1, result)
}

func TestFindClosestShapePoint_ClosestIsFirst(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	points := []gtfsdb.GetShapePointsForTripRow{
		{Lat: 40.7128, Lon: -74.0060}, // Point 0 - closest
		{Lat: 40.7500, Lon: -74.0000}, // Point 1 - far
		{Lat: 40.8000, Lon: -73.9000}, // Point 2 - far
	}

	result := dc.findClosestShapePoint(points, 40.7130, -74.0061)

	assert.Equal(t, 0, result)
}

func TestFindClosestShapePoint_ClosestIsLast(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	points := []gtfsdb.GetShapePointsForTripRow{
		{Lat: 40.7128, Lon: -74.0060}, // Point 0 - far
		{Lat: 40.7500, Lon: -74.0000}, // Point 1 - far
		{Lat: 40.8000, Lon: -73.9000}, // Point 2 - closest
	}

	result := dc.findClosestShapePoint(points, 40.8001, -73.9001)

	assert.Equal(t, 2, result)
}

func TestGetMostCommonDirection_EmptyMap(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	directions := make(map[string]int)

	result := dc.getMostCommonDirection(directions)

	assert.Equal(t, models.UnknownValue, result)
}

func TestGetMostCommonDirection_SingleDirection(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	directions := map[string]int{
		"N": 5,
	}

	result := dc.getMostCommonDirection(directions)

	assert.Equal(t, "N", result)
}

func TestGetMostCommonDirection_MultipleDirections(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	directions := map[string]int{
		"N":  3,
		"NE": 7,
		"E":  2,
	}

	result := dc.getMostCommonDirection(directions)

	assert.Equal(t, "NE", result)
}

func TestGetMostCommonDirection_TieGoesToFirst(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)

	// Map iteration order is random, but one will be selected
	directions := map[string]int{
		"N": 5,
		"S": 5,
	}

	result := dc.getMostCommonDirection(directions)

	// One of them should be selected
	assert.Contains(t, []string{"N", "S"}, result)
}

func TestCalculateFromShape_NoTrips(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Insert a stop with no associated trips
	_, err := client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_NO_TRIPS', 'No Trips Stop', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.calculateFromShape(ctx, "STOP_NO_TRIPS")

	assert.Equal(t, models.UnknownValue, direction)
}

func TestCalculateFromShape_TripWithoutShape(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Setup data with trip but no shape
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AG3', 'Agency 3', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('R3', 'AG3', 'R3', 'Route 3', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SVC3', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('T3', 'R3', 'SVC3')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_NO_SHAPE', 'No Shape Stop', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('T3', 28800000000000, 28800000000000, 'STOP_NO_SHAPE', 1)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.calculateFromShape(ctx, "STOP_NO_SHAPE")

	assert.Equal(t, models.UnknownValue, direction)
}

func TestCalculateFromShape_ShapeWithOnePoint(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Setup with a shape that has only one point
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AG4', 'Agency 4', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('R4', 'AG4', 'R4', 'Route 4', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO shapes (shape_id, lat, lon, shape_pt_sequence)
		VALUES ('SHAPE_ONE', 40.7128, -74.0060, 1)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SVC4', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id, shape_id)
		VALUES ('T4', 'R4', 'SVC4', 'SHAPE_ONE')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_ONE_POINT', 'One Point Stop', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('T4', 28800000000000, 28800000000000, 'STOP_ONE_POINT', 1)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.calculateFromShape(ctx, "STOP_ONE_POINT")

	assert.Equal(t, models.UnknownValue, direction)
}

func TestCalculateFromShape_ClosestPointIsLast(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Setup with shape where stop is closest to last point
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AG5', 'Agency 5', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('R5', 'AG5', 'R5', 'Route 5', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO shapes (shape_id, lat, lon, shape_pt_sequence)
		VALUES
			('SHAPE_LAST', 40.7128, -74.0060, 1),
			('SHAPE_LAST', 40.7228, -74.0060, 2),
			('SHAPE_LAST', 40.7328, -74.0060, 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SVC5', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id, shape_id)
		VALUES ('T5', 'R5', 'SVC5', 'SHAPE_LAST')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_AT_END', 'Stop at End', 40.7328, -74.0060)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('T5', 28800000000000, 28800000000000, 'STOP_AT_END', 1)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.calculateFromShape(ctx, "STOP_AT_END")

	// When stop is at last shape point, can't calculate direction
	assert.Equal(t, models.UnknownValue, direction)
}

func TestCalculateFromNextStop_NoNextStop(t *testing.T) {
	client := setupTestClient(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	// Setup with stop that is the last stop in trip
	_, err := client.DB.Exec(`
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AG6', 'Agency 6', 'http://test.com', 'America/New_York')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('R6', 'AG6', 'R6', 'Route 6', 3)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SVC6', 1, 1, 1, 1, 1, 0, 0, '20240101', '20241231')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO trips (id, route_id, service_id)
		VALUES ('T6', 'R6', 'SVC6')
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stops (id, name, lat, lon)
		VALUES ('LAST_STOP', 'Last Stop', 40.7128, -74.0060)
	`)
	require.NoError(t, err)

	_, err = client.DB.Exec(`
		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('T6', 28800000000000, 28800000000000, 'LAST_STOP', 1)
	`)
	require.NoError(t, err)

	manager := &Manager{GtfsDB: client}
	dc := NewDirectionCalculator(manager)
	direction := dc.calculateFromNextStop(ctx, "LAST_STOP")

	assert.Equal(t, models.UnknownValue, direction)
}

// Helper function to setup test client with database
func setupTestClient(t *testing.T) *gtfsdb.Client {
	config := gtfsdb.Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := gtfsdb.NewClient(config)
	require.NoError(t, err, "Failed to create test client")
	require.NotNil(t, client, "Client should not be nil")

	return client
}
