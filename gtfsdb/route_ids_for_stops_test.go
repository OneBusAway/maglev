package gtfsdb

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRouteIDsForStops_SeparateColumns(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Pick a stop known to exist in RABA (4062).
	rows, err := client.Queries.GetRouteIDsForStops(ctx, []string{"4062"})
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	for _, row := range rows {
		assert.Equal(t, "4062", row.StopID)
		assert.Equal(t, "25", row.AgencyID)
		assert.NotEmpty(t, row.RouteID)
		assert.False(t, strings.HasPrefix(row.RouteID, row.AgencyID+"_"), "RouteID should not be prefixed with agency ID")
	}
}

func TestGetRouteIDsForStops_AgencyWithUnderscore(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Insert test data where both agency ID and route ID contain underscores.
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO agencies (id, name, url, timezone)
		VALUES ('AGENCY_WITH_UNDERSCORE', 'Test Agency', 'http://example.com', 'America/Los_Angeles');

		INSERT INTO routes (id, agency_id, short_name, long_name, type)
		VALUES ('ROUTE_42', 'AGENCY_WITH_UNDERSCORE', '42', 'Route 42', 3);

		INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES ('SVC_UNDERSCORE', 1, 1, 1, 1, 1, 1, 1, '20240101', '20991231');

		INSERT INTO trips (id, route_id, service_id)
		VALUES ('TRIP_UNDERSCORE', 'ROUTE_42', 'SVC_UNDERSCORE');

		INSERT INTO stops (id, name, lat, lon)
		VALUES ('STOP_UNDERSCORE', 'Underscore Stop', 47.6062, -122.3321);

		INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
		VALUES ('TRIP_UNDERSCORE', 28800, 28800, 'STOP_UNDERSCORE', 1);
	`)
	require.NoError(t, err)
	require.NoError(t, buildStopRouteIndex(ctx, client.Queries))

	// Test GetRouteIDsForStops
	rows, err := client.Queries.GetRouteIDsForStops(ctx, []string{"STOP_UNDERSCORE"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "STOP_UNDERSCORE", rows[0].StopID)
	assert.Equal(t, "AGENCY_WITH_UNDERSCORE", rows[0].AgencyID)
	assert.Equal(t, "ROUTE_42", rows[0].RouteID)

	// Test GetActiveRouteIDsForStopsOnDate
	activeRows, err := client.Queries.GetActiveRouteIDsForStopsOnDate(ctx, GetActiveRouteIDsForStopsOnDateParams{
		StopIds:    []string{"STOP_UNDERSCORE"},
		ServiceIds: []string{"SVC_UNDERSCORE"},
	})
	require.NoError(t, err)
	require.Len(t, activeRows, 1)
	assert.Equal(t, "STOP_UNDERSCORE", activeRows[0].StopID)
	assert.Equal(t, "AGENCY_WITH_UNDERSCORE", activeRows[0].AgencyID)
	assert.Equal(t, "ROUTE_42", activeRows[0].RouteID)
}

// GetRouteIDsForStops answers from stop_routes, so a pair the index does not list is
// not returned even though stop times still produce it.
func TestGetRouteIDsForStops_ReadsStopRouteIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_routes LIMIT 1").Scan(&stopID))
	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_routes WHERE stop_id = ?", stopID)
	require.NoError(t, err)

	rows, err := client.Queries.GetRouteIDsForStops(ctx, []string{stopID})

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// Reading the index must return the same rows as walking stop_times did. The query has
// no ORDER BY and its callers sort, so only the set is compared.
func TestGetRouteIDsForStops_MatchesStopTimesJoin(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	addSecondRouteAtStop(t, client)

	stopIDs := queryStrings(t, client, "SELECT id FROM stops ORDER BY id")
	joinRows, err := client.DB.QueryContext(ctx, `
		SELECT DISTINCT routes.id, routes.agency_id, routes.short_name, routes.long_name, stop_times.stop_id
		FROM stop_times
		JOIN trips ON stop_times.trip_id = trips.id
		JOIN routes ON trips.route_id = routes.id
	`)
	require.NoError(t, err)
	var want []GetRouteIDsForStopsRow
	for joinRows.Next() {
		var row GetRouteIDsForStopsRow
		require.NoError(t, joinRows.Scan(&row.RouteID, &row.AgencyID, &row.ShortName, &row.LongName, &row.StopID))
		want = append(want, row)
	}
	require.NoError(t, joinRows.Err())
	require.NoError(t, joinRows.Close())
	require.NotEmpty(t, want)

	got, err := client.Queries.GetRouteIDsForStops(ctx, stopIDs)

	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
}
