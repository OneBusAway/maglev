package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetRoutesForStops answers from stop_routes, so a pair the index does not list is
// not returned even though stop times still produce it.
func TestGetRoutesForStops_ReadsStopRouteIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_routes LIMIT 1").Scan(&stopID))
	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_routes WHERE stop_id = ?", stopID)
	require.NoError(t, err)

	rows, err := client.Queries.GetRoutesForStops(ctx, []string{stopID})

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// Reading the index must return the same rows, in the same order, as walking
// stop_times did, including for a stop served by two routes.
func TestGetRoutesForStops_MatchesStopTimesJoin(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	addSecondRouteAtStop(t, client)

	stopIDs := queryStrings(t, client, "SELECT id FROM stops ORDER BY id")
	joinRows, err := client.DB.QueryContext(ctx, `
		SELECT DISTINCT routes.id, routes.agency_id, routes.short_name, routes.long_name, routes."desc",
			routes.type, routes.url, routes.color, routes.text_color, stop_times.stop_id
		FROM stop_times
		JOIN trips ON stop_times.trip_id = trips.id
		JOIN routes ON trips.route_id = routes.id
		ORDER BY routes.agency_id ASC, routes.id ASC, stop_times.stop_id ASC
	`)
	require.NoError(t, err)
	var want []GetRoutesForStopsRow
	for joinRows.Next() {
		var row GetRoutesForStopsRow
		require.NoError(t, joinRows.Scan(&row.ID, &row.AgencyID, &row.ShortName, &row.LongName, &row.Desc,
			&row.Type, &row.Url, &row.Color, &row.TextColor, &row.StopID))
		want = append(want, row)
	}
	require.NoError(t, joinRows.Err())
	require.NoError(t, joinRows.Close())
	require.NotEmpty(t, want)

	got, err := client.Queries.GetRoutesForStops(ctx, stopIDs)

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestGetRoutesForStops_UnknownStop(t *testing.T) {
	client := newTestClientWithRABA(t)

	rows, err := client.Queries.GetRoutesForStops(context.Background(), []string{"no-such-stop"})

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// GetRoutesForStop answers from stop_routes, so a pair the index does not list is
// not returned even though stop times still produce it.
func TestGetRoutesForStop_ReadsStopRouteIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_routes LIMIT 1").Scan(&stopID))
	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_routes WHERE stop_id = ?", stopID)
	require.NoError(t, err)

	rows, err := client.Queries.GetRoutesForStop(ctx, stopID)

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// Reading the index must return the same routes as walking stop_times did. The query
// has no ORDER BY and its callers sort, so only the set is compared.
func TestGetRoutesForStop_MatchesStopTimesJoin(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	stopID := addSecondRouteAtStop(t, client)

	joinRows, err := client.DB.QueryContext(ctx, `
		SELECT DISTINCT routes.id, routes.agency_id, routes.short_name, routes.long_name, routes."desc",
			routes.type, routes.url, routes.color, routes.text_color
		FROM stop_times
		JOIN trips ON stop_times.trip_id = trips.id
		JOIN routes ON trips.route_id = routes.id
		WHERE stop_times.stop_id = ?
	`, stopID)
	require.NoError(t, err)
	var want []GetRoutesForStopRow
	for joinRows.Next() {
		var row GetRoutesForStopRow
		require.NoError(t, joinRows.Scan(&row.ID, &row.AgencyID, &row.ShortName, &row.LongName, &row.Desc,
			&row.Type, &row.Url, &row.Color, &row.TextColor))
		want = append(want, row)
	}
	require.NoError(t, joinRows.Err())
	require.NoError(t, joinRows.Close())
	require.Greater(t, len(want), 1)

	got, err := client.Queries.GetRoutesForStop(ctx, stopID)

	require.NoError(t, err)
	assert.ElementsMatch(t, want, got)
}
