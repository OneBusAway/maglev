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
