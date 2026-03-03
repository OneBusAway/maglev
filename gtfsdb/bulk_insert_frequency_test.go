package gtfsdb

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// createFrequencyTestClient sets up a test client with the prerequisite data
func createFrequencyTestClient(t *testing.T) *Client {
	t.Helper()

	config := Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	}

	client, err := NewClient(config)
	require.NoError(t, err)

	ctx := context.Background()

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

	_, err = client.Queries.CreateStop(ctx, CreateStopParams{
		ID:   "stop_1",
		Name: sql.NullString{String: "Test Stop", Valid: true},
		Lat:  40.0,
		Lon:  -74.0,
	})
	require.NoError(t, err)

	// Create multiple trips for testing
	for _, tripID := range []string{"trip_1", "trip_2", "trip_3", "trip_4", "trip_5"} {
		_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
			ID:           tripID,
			RouteID:      "test_route",
			ServiceID:    "test_service",
			TripHeadsign: sql.NullString{String: "Test", Valid: true},
		})
		require.NoError(t, err)
	}

	return client
}

func TestBulkInsertFrequencies(t *testing.T) {
	testCases := []struct {
		name  string
		count int
	}{
		{"Empty batch", 0},
		{"Single record", 1},
		{"Small batch", 5},
		{"Multiple windows for one trip", 3},
		{"Large batch", 50},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := createFrequencyTestClient(t)
			defer func() { _ = client.Close() }()

			ctx := context.Background()

			var frequencies []CreateFrequencyParams
			if tc.name == "Multiple windows for one trip" {
				// Create 3 frequency windows for the same trip (morning/midday/evening)
				frequencies = []CreateFrequencyParams{
					{
						TripID:      "trip_1",
						StartTime:   int64(6 * 3600 * 1e9), // 6 AM in nanoseconds
						EndTime:     int64(9 * 3600 * 1e9), // 9 AM
						HeadwaySecs: 600,                   // 10 minutes
						ExactTimes:  0,
					},
					{
						TripID:      "trip_1",
						StartTime:   int64(11 * 3600 * 1e9), // 11 AM
						EndTime:     int64(14 * 3600 * 1e9), // 2 PM
						HeadwaySecs: 900,                    // 15 minutes
						ExactTimes:  0,
					},
					{
						TripID:      "trip_1",
						StartTime:   int64(16 * 3600 * 1e9), // 4 PM
						EndTime:     int64(19 * 3600 * 1e9), // 7 PM
						HeadwaySecs: 600,                    // 10 minutes
						ExactTimes:  1,
					},
				}
			} else {
				frequencies = make([]CreateFrequencyParams, tc.count)
				for i := 0; i < tc.count; i++ {
					frequencies[i] = CreateFrequencyParams{
						TripID:      "trip_1",
						StartTime:   int64(i) * int64(3600*1e9), // Each hour
						EndTime:     int64(i+1) * int64(3600*1e9),
						HeadwaySecs: 600,
						ExactTimes:  int64(i % 2), // Alternate between 0 and 1
					}
				}
			}

			err := client.bulkInsertFrequencies(ctx, frequencies)
			require.NoError(t, err)

			if tc.count == 0 {
				return
			}

			rows, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_1")
			require.NoError(t, err)

			expectedCount := tc.count
			if tc.name == "Multiple windows for one trip" {
				expectedCount = 3
			}
			assert.Equal(t, expectedCount, len(rows))

			for i := 1; i < len(rows); i++ {
				assert.Less(t, rows[i-1].StartTime, rows[i].StartTime,
					"Rows should be ordered by start_time")
			}

			for _, row := range rows {
				assert.Less(t, row.StartTime, row.EndTime,
					"start_time should be less than end_time")
				assert.Greater(t, row.HeadwaySecs, int64(0),
					"headway_secs should be positive")
				assert.Contains(t, []int64{0, 1}, row.ExactTimes,
					"exact_times should be 0 or 1")
			}
		})
	}
}

func TestBulkInsertFrequencies_MultipleTrips(t *testing.T) {
	client := createFrequencyTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	frequencies := []CreateFrequencyParams{
		{TripID: "trip_1", StartTime: int64(6 * 3600 * 1e9), EndTime: int64(9 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_1", StartTime: int64(16 * 3600 * 1e9), EndTime: int64(19 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_2", StartTime: int64(7 * 3600 * 1e9), EndTime: int64(10 * 3600 * 1e9), HeadwaySecs: 300, ExactTimes: 1},
		{TripID: "trip_3", StartTime: int64(8 * 3600 * 1e9), EndTime: int64(12 * 3600 * 1e9), HeadwaySecs: 1200, ExactTimes: 0},
	}

	err := client.bulkInsertFrequencies(ctx, frequencies)
	require.NoError(t, err)

	rows1, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_1")
	require.NoError(t, err)
	assert.Equal(t, 2, len(rows1))

	rows2, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_2")
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows2))
	assert.Equal(t, int64(1), rows2[0].ExactTimes)
	assert.Equal(t, int64(300), rows2[0].HeadwaySecs)

	rows3, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_3")
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows3))

	rows4, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_4")
	require.NoError(t, err)
	assert.Equal(t, 0, len(rows4))
}

func TestBulkInsertFrequencies_DuplicatePrimaryKey(t *testing.T) {
	client := createFrequencyTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	startTime := int64(6 * 3600 * 1e9)
	frequencies := []CreateFrequencyParams{
		{TripID: "trip_1", StartTime: startTime, EndTime: int64(9 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_1", StartTime: startTime, EndTime: int64(10 * 3600 * 1e9), HeadwaySecs: 900, ExactTimes: 1},
	}

	err := client.bulkInsertFrequencies(ctx, frequencies)
	require.NoError(t, err)

	rows, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_1")
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows), "Duplicate primary key should result in only 1 row")
	assert.Equal(t, int64(600), rows[0].HeadwaySecs)
	assert.Equal(t, int64(0), rows[0].ExactTimes)
}

func TestGetFrequencyTripIDs(t *testing.T) {
	client := createFrequencyTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	frequencies := []CreateFrequencyParams{
		{TripID: "trip_1", StartTime: int64(6 * 3600 * 1e9), EndTime: int64(9 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_1", StartTime: int64(16 * 3600 * 1e9), EndTime: int64(19 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_3", StartTime: int64(8 * 3600 * 1e9), EndTime: int64(12 * 3600 * 1e9), HeadwaySecs: 1200, ExactTimes: 0},
	}

	err := client.bulkInsertFrequencies(ctx, frequencies)
	require.NoError(t, err)

	tripIDs, err := client.Queries.GetFrequencyTripIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(tripIDs), "Should return 2 distinct trip IDs")
	assert.Contains(t, tripIDs, "trip_1")
	assert.Contains(t, tripIDs, "trip_3")
}

func TestClearFrequencies(t *testing.T) {
	client := createFrequencyTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	frequencies := []CreateFrequencyParams{
		{TripID: "trip_1", StartTime: int64(6 * 3600 * 1e9), EndTime: int64(9 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_2", StartTime: int64(7 * 3600 * 1e9), EndTime: int64(10 * 3600 * 1e9), HeadwaySecs: 300, ExactTimes: 1},
	}

	err := client.bulkInsertFrequencies(ctx, frequencies)
	require.NoError(t, err)

	rows, err := client.Queries.GetFrequenciesForTrip(ctx, "trip_1")
	require.NoError(t, err)
	assert.Equal(t, 1, len(rows))

	err = client.Queries.ClearFrequencies(ctx)
	require.NoError(t, err)

	rows, err = client.Queries.GetFrequenciesForTrip(ctx, "trip_1")
	require.NoError(t, err)
	assert.Equal(t, 0, len(rows))

	rows, err = client.Queries.GetFrequenciesForTrip(ctx, "trip_2")
	require.NoError(t, err)
	assert.Equal(t, 0, len(rows))
}

func TestGetFrequenciesForTrips(t *testing.T) {
	client := createFrequencyTestClient(t)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	frequencies := []CreateFrequencyParams{
		{TripID: "trip_1", StartTime: int64(6 * 3600 * 1e9), EndTime: int64(9 * 3600 * 1e9), HeadwaySecs: 600, ExactTimes: 0},
		{TripID: "trip_2", StartTime: int64(7 * 3600 * 1e9), EndTime: int64(10 * 3600 * 1e9), HeadwaySecs: 300, ExactTimes: 1},
		{TripID: "trip_3", StartTime: int64(8 * 3600 * 1e9), EndTime: int64(12 * 3600 * 1e9), HeadwaySecs: 1200, ExactTimes: 0},
	}

	err := client.bulkInsertFrequencies(ctx, frequencies)
	require.NoError(t, err)

	rows, err := client.Queries.GetFrequenciesForTrips(ctx, []string{"trip_1", "trip_2"})
	require.NoError(t, err)

	assert.Equal(t, 2, len(rows), "Should return exactly 2 rows for the 2 requested trips")

	var returnedTripIDs []string
	for _, row := range rows {
		returnedTripIDs = append(returnedTripIDs, row.TripID)
	}
	assert.Contains(t, returnedTripIDs, "trip_1")
	assert.Contains(t, returnedTripIDs, "trip_2")
	assert.NotContains(t, returnedTripIDs, "trip_3", "Should NOT return trip_3")

	rowsEmpty, err := client.Queries.GetFrequenciesForTrips(ctx, []string{})
	require.NoError(t, err)
	assert.Equal(t, 0, len(rowsEmpty), "Empty input slice should return empty results")
}
