package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/nulls"
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
		ShortName: nulls.String("TEST"),
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
		Name: nulls.String("Test Stop"),
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create a test trip
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:           "test_trip",
		RouteID:      "test_route",
		ServiceID:    "test_service",
		TripHeadsign: nulls.String("Test"),
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
			err = client.bulkInsertStopTimes(ctx, slices.Values(stopTimes), nil)
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
			err = client.bulkInsertShapes(ctx, slices.Values(shapes), nil)
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

	err = client.bulkInsertShapes(ctx, slices.Values(shapes), nil)
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
		ShortName: nulls.String("PERF"),
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
		Name: nulls.String("Performance Test Stop"),
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create a test trip
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:           "perf_trip",
		RouteID:      "perf_route",
		ServiceID:    "perf_service",
		TripHeadsign: nulls.String("Performance"),
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
	err = client.bulkInsertStopTimes(ctx, slices.Values(stopTimes), nil)
	duration := time.Since(start)

	require.NoError(t, err, "Bulk insert should succeed")

	// Verify all records inserted
	var count int
	err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_times").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, recordCount, count)

	// Windows runners are too slow at SQLite file I/O to hold a wall-clock budget,
	// the same reason cmd/api and internal/gtfs skip their timing tests there.
	if runtime.GOOS != "windows" {
		assert.Less(t, duration.Seconds(), 5.0,
			"Bulk insert of %d records should complete in < 5 seconds (took %v)", recordCount, duration)
	}

	t.Logf("Bulk inserted %d stop_times in %v (~%.0f inserts/sec)",
		recordCount, duration, float64(recordCount)/duration.Seconds())
}

func TestBulkInsertStops(t *testing.T) {
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
		{"Single stop", 1},
		{"Small batch", 50},
		{"Multiple stops", 150},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DB.ExecContext(ctx, "DELETE FROM stops")
			require.NoError(t, err)

			stops := make([]CreateStopParams, tc.count)
			for i := range tc.count {
				stops[i] = CreateStopParams{
					ID:                 fmt.Sprintf("stop_%d", i),
					Code:               nulls.String(fmt.Sprintf("code_%d", i)),
					Name:               nulls.String(fmt.Sprintf("Stop %d", i)),
					Desc:               nulls.String(fmt.Sprintf("Desc %d", i)),
					Lat:                47.6062 + float64(i)*0.001,
					Lon:                -122.3321 + float64(i)*0.001,
					ZoneID:             nulls.String("zone_1"),
					Url:                nulls.String("http://example.com"),
					LocationType:       nulls.Int64(0),
					Timezone:           nulls.String("America/Los_Angeles"),
					WheelchairBoarding: nulls.Int64(1),
					PlatformCode:       nulls.String("1"),
					Direction:          nulls.String("N"),
					ParentStation:      sql.NullString{},
				}
			}

			err = client.bulkInsertStops(ctx, slices.Values(stops), nil)
			require.NoError(t, err)

			var count int
			err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stops").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, tc.count, count)

			if tc.count > 0 {
				var name, desc string
				err = client.DB.QueryRowContext(ctx, "SELECT name, \"desc\" FROM stops WHERE id = 'stop_0'").Scan(&name, &desc)
				require.NoError(t, err)
				assert.Equal(t, "Stop 0", name)
				assert.Equal(t, "Desc 0", desc)
			}
		})
	}

	t.Run("Replace duplicate primary key", func(t *testing.T) {
		_, err := client.DB.ExecContext(ctx, "DELETE FROM stops")
		require.NoError(t, err)

		stops := []CreateStopParams{
			{
				ID:   "stop_1",
				Name: nulls.String("First Name"),
				Desc: nulls.String("First Desc"),
				Lat:  47.6062,
				Lon:  -122.3321,
			},
			{
				ID:   "stop_1",
				Name: nulls.String("Second Name"),
				Desc: nulls.String("Second Desc"),
				Lat:  47.6063,
				Lon:  -122.3322,
			},
		}

		err = client.bulkInsertStops(ctx, slices.Values(stops), nil)
		require.NoError(t, err)

		var count int
		err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stops").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Duplicate stop ID should replace")

		var name, desc string
		err = client.DB.QueryRowContext(ctx, "SELECT name, \"desc\" FROM stops WHERE id = 'stop_1'").Scan(&name, &desc)
		require.NoError(t, err)
		assert.Equal(t, "Second Name", name, "Should have replaced with second name")
		assert.Equal(t, "Second Desc", desc, "Should have replaced with second desc")
	})
}

func TestBulkInsertTrips(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Prerequisite data: agency, route, service
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency_1",
		Name:     "Test Agency",
		Url:      "http://example.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:        "route_1",
		AgencyID:  "agency_1",
		ShortName: nulls.String("1"),
		Type:      3,
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "service_1",
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

	testCases := []struct {
		name  string
		count int
	}{
		{"Empty batch", 0},
		{"Single trip", 1},
		{"Small batch", 50},
		{"Multiple trips", 200},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DB.ExecContext(ctx, "DELETE FROM trips")
			require.NoError(t, err)

			trips := make([]CreateTripParams, tc.count)
			for i := range tc.count {
				trips[i] = CreateTripParams{
					ID:                   fmt.Sprintf("trip_%d", i),
					RouteID:              "route_1",
					ServiceID:            "service_1",
					TripHeadsign:         nulls.String("Downtown"),
					TripShortName:        nulls.String("DT"),
					DirectionID:          nulls.Int64(0),
					BlockID:              nulls.String("block_1"),
					ShapeID:              nulls.String("shape_1"),
					WheelchairAccessible: nulls.Int64(1),
					BikesAllowed:         nulls.Int64(1),
					MinArrivalTime:       nulls.Int64(int64(i * 60)),
					MaxDepartureTime:     nulls.Int64(int64(i*60 + 3600)),
				}
			}

			err = client.bulkInsertTrips(ctx, slices.Values(trips), nil)
			require.NoError(t, err)

			var count int
			err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM trips").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, tc.count, count)
		})
	}

	t.Run("Replace duplicate primary key", func(t *testing.T) {
		_, err := client.DB.ExecContext(ctx, "DELETE FROM trips")
		require.NoError(t, err)

		trips := []CreateTripParams{
			{
				ID:           "trip_1",
				RouteID:      "route_1",
				ServiceID:    "service_1",
				TripHeadsign: nulls.String("First Headsign"),
			},
			{
				ID:           "trip_1",
				RouteID:      "route_1",
				ServiceID:    "service_1",
				TripHeadsign: nulls.String("Second Headsign"),
			},
		}

		err = client.bulkInsertTrips(ctx, slices.Values(trips), nil)
		require.NoError(t, err)

		var count int
		err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM trips").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Duplicate trip ID should replace")

		var headsign string
		err = client.DB.QueryRowContext(ctx, "SELECT trip_headsign FROM trips WHERE id = 'trip_1'").Scan(&headsign)
		require.NoError(t, err)
		assert.Equal(t, "Second Headsign", headsign, "Should have replaced with second headsign")
	})
}

func TestBulkInsertCalendarDates(t *testing.T) {
	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// Prerequisite calendar
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "service_1",
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

	testCases := []struct {
		name  string
		count int
	}{
		{"Empty batch", 0},
		{"Single date", 1},
		{"Small batch", 100},
		{"Multiple dates", 700},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.DB.ExecContext(ctx, "DELETE FROM calendar_dates")
			require.NoError(t, err)

			dates := make([]CreateCalendarDateParams, tc.count)
			for i := range tc.count {
				dates[i] = CreateCalendarDateParams{
					ServiceID:     "service_1",
					Date:          fmt.Sprintf("%08d", i),
					ExceptionType: 1,
				}
			}

			err = client.bulkInsertCalendarDates(ctx, slices.Values(dates), nil)
			require.NoError(t, err)

			var count int
			err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM calendar_dates").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, tc.count, count)
		})
	}

	t.Run("Replace duplicate primary key", func(t *testing.T) {
		_, err := client.DB.ExecContext(ctx, "DELETE FROM calendar_dates")
		require.NoError(t, err)

		dates := []CreateCalendarDateParams{
			{ServiceID: "service_1", Date: "20241225", ExceptionType: 1},
			{ServiceID: "service_1", Date: "20241225", ExceptionType: 2},
		}

		err = client.bulkInsertCalendarDates(ctx, slices.Values(dates), nil)
		require.NoError(t, err)

		var count int
		err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM calendar_dates").Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Duplicate (service_id, date) should replace")

		var exceptionType int64
		err = client.DB.QueryRowContext(ctx, "SELECT exception_type FROM calendar_dates WHERE service_id = 'service_1'").Scan(&exceptionType)
		require.NoError(t, err)
		assert.Equal(t, int64(2), exceptionType, "Should have replaced with second exception_type")
	})
}
