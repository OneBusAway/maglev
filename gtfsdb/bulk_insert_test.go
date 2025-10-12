package gtfsdb

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestBulkInsertStopTimes(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Create prerequisite data (agency, route, calendar/service)
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "test_agency",
		Name:     "Test Agency",
		Url:      "http://test.com",
		Timezone: "America/New_York",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:        "test_route",
		AgencyID:  "test_agency",
		ShortName: sql.NullString{String: "TEST", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "test_service",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240101",
		EndDate:   "20241231",
	})
	require.NoError(t, err)

	// Create a test stop
	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID:   "stop_1",
		Name: sql.NullString{String: "Test Stop", Valid: true},
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create a test trip
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:           "test_trip",
		RouteID:      "test_route",
		ServiceID:    "test_service",
		TripHeadsign: sql.NullString{String: "Test", Valid: true},
	})
	require.NoError(t, err)

	// Test with various batch sizes
	testCases := []struct {
		name  string
		count int
	}{
		{"Empty batch", 0},
		{"Single record", 1},
		{"Small batch", 50},
		{"Exact batch size", 1000},
		{"Just over batch size", 1001},
		{"Multiple batches", 2500},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear stop_times table
			_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_times")
			require.NoError(t, err)

			// Generate test data
			stopTimes := make([]CreateStopTimeParams, tc.count)
			for i := 0; i < tc.count; i++ {
				stopTimes[i] = CreateStopTimeParams{
					TripID:        "test_trip",
					ArrivalTime:   int64(i * 60),
					DepartureTime: int64(i * 60),
					StopID:        "stop_1",
					StopSequence:  int64(i),
					PickupType:    sql.NullInt64{Int64: 0, Valid: true},
					DropOffType:   sql.NullInt64{Int64: 0, Valid: true},
				}
			}

			// Perform bulk insert
			err = client.bulkInsertStopTimes(ctx, stopTimes)
			require.NoError(t, err, "Bulk insert should succeed")

			// Verify all records were inserted
			var count int
			err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_times").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, tc.count, count, "Should insert exactly %d records", tc.count)

			// Verify data integrity for first and last record if any
			if tc.count > 0 {
				var firstSeq int64
				err = client.DB.QueryRowContext(ctx,
					"SELECT stop_sequence FROM stop_times ORDER BY stop_sequence LIMIT 1").Scan(&firstSeq)
				require.NoError(t, err)
				assert.Equal(t, int64(0), firstSeq, "First record should have sequence 0")

				var lastSeq int64
				err = client.DB.QueryRowContext(ctx,
					"SELECT stop_sequence FROM stop_times ORDER BY stop_sequence DESC LIMIT 1").Scan(&lastSeq)
				require.NoError(t, err)
				assert.Equal(t, int64(tc.count-1), lastSeq, "Last record should have correct sequence")
			}
		})
	}
}

func TestBulkInsertShapes(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	testCases := []struct {
		name  string
		count int
	}{
		{"Empty batch", 0},
		{"Single point", 1},
		{"Small batch", 100},
		{"Exact batch size", 1000},
		{"Just over batch size", 1001},
		{"Multiple batches", 3000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear shapes table
			_, err := client.DB.ExecContext(ctx, "DELETE FROM shapes")
			require.NoError(t, err)

			// Generate test data
			shapes := make([]CreateShapeParams, tc.count)
			for i := 0; i < tc.count; i++ {
				shapes[i] = CreateShapeParams{
					ShapeID:           "test_shape",
					Lat:               40.0 + float64(i)*0.001,
					Lon:               -74.0 + float64(i)*0.001,
					ShapePtSequence:   int64(i),
					ShapeDistTraveled: sql.NullFloat64{Float64: float64(i) * 100, Valid: true},
				}
			}

			// Perform bulk insert
			err = client.bulkInsertShapes(ctx, shapes)
			require.NoError(t, err, "Bulk insert should succeed")

			// Verify all records were inserted
			var count int
			err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM shapes").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, tc.count, count, "Should insert exactly %d records", tc.count)

			// Verify data integrity for first and last point if any
			if tc.count > 0 {
				var firstLat, firstLon float64
				err = client.DB.QueryRowContext(ctx,
					"SELECT lat, lon FROM shapes ORDER BY shape_pt_sequence LIMIT 1").Scan(&firstLat, &firstLon)
				require.NoError(t, err)
				assert.InDelta(t, 40.0, firstLat, 0.0001, "First point should have correct latitude")
				assert.InDelta(t, -74.0, firstLon, 0.0001, "First point should have correct longitude")

				var lastSeq int64
				err = client.DB.QueryRowContext(ctx,
					"SELECT shape_pt_sequence FROM shapes ORDER BY shape_pt_sequence DESC LIMIT 1").Scan(&lastSeq)
				require.NoError(t, err)
				assert.Equal(t, int64(tc.count-1), lastSeq, "Last point should have correct sequence")
			}
		})
	}
}

func TestBulkInsertWithNullValues(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Test shapes with NULL distance traveled
	shapes := []CreateShapeParams{
		{
			ShapeID:           "shape1",
			Lat:               40.0,
			Lon:               -74.0,
			ShapePtSequence:   0,
			ShapeDistTraveled: sql.NullFloat64{Valid: false}, // NULL
		},
		{
			ShapeID:           "shape1",
			Lat:               40.001,
			Lon:               -74.001,
			ShapePtSequence:   1,
			ShapeDistTraveled: sql.NullFloat64{Float64: 100.0, Valid: true},
		},
	}

	err = client.bulkInsertShapes(ctx, shapes)
	require.NoError(t, err, "Should handle NULL values")

	// Verify NULL was inserted correctly
	var distTrav sql.NullFloat64
	err = client.DB.QueryRowContext(ctx,
		"SELECT shape_dist_traveled FROM shapes WHERE shape_pt_sequence = 0").Scan(&distTrav)
	require.NoError(t, err)
	assert.False(t, distTrav.Valid, "First point should have NULL distance")

	err = client.DB.QueryRowContext(ctx,
		"SELECT shape_dist_traveled FROM shapes WHERE shape_pt_sequence = 1").Scan(&distTrav)
	require.NoError(t, err)
	assert.True(t, distTrav.Valid, "Second point should have valid distance")
	assert.Equal(t, 100.0, distTrav.Float64, "Distance should be correct")
}

func TestBulkInsertPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Create prerequisite data (agency, route, calendar/service)
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "perf_agency",
		Name:     "Performance Test Agency",
		Url:      "http://test.com",
		Timezone: "America/New_York",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:        "perf_route",
		AgencyID:  "perf_agency",
		ShortName: sql.NullString{String: "PERF", Valid: true},
		Type:      3,
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "perf_service",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240101",
		EndDate:   "20241231",
	})
	require.NoError(t, err)

	// Create a test stop
	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID:   "stop_1",
		Name: sql.NullString{String: "Performance Test Stop", Valid: true},
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create a test trip
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:           "perf_trip",
		RouteID:      "perf_route",
		ServiceID:    "perf_service",
		TripHeadsign: sql.NullString{String: "Performance", Valid: true},
	})
	require.NoError(t, err)

	// Generate large dataset (10,000 stop times)
	const recordCount = 10000
	stopTimes := make([]CreateStopTimeParams, recordCount)
	for i := 0; i < recordCount; i++ {
		stopTimes[i] = CreateStopTimeParams{
			TripID:        "perf_trip",
			ArrivalTime:   int64(i * 60),
			DepartureTime: int64(i * 60),
			StopID:        "stop_1",
			StopSequence:  int64(i),
			PickupType:    sql.NullInt64{Int64: 0, Valid: true},
			DropOffType:   sql.NullInt64{Int64: 0, Valid: true},
		}
	}

	// Measure bulk insert performance
	start := time.Now()
	err = client.bulkInsertStopTimes(ctx, stopTimes)
	duration := time.Since(start)

	require.NoError(t, err, "Bulk insert should succeed")

	// Verify all records inserted
	var count int
	err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_times").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, recordCount, count)

	// Performance assertion: should complete in reasonable time (< 5 seconds for 10k records)
	assert.Less(t, duration.Seconds(), 5.0,
		"Bulk insert of %d records should complete in < 5 seconds (took %v)", recordCount, duration)

	t.Logf("Bulk inserted %d stop_times in %v (~%.0f inserts/sec)",
		recordCount, duration, float64(recordCount)/duration.Seconds())
}
