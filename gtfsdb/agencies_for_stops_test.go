package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetAgenciesForStops answers from the stop_agencies index, so a stop the
// index does not list has no agencies even though stop times still serve it.
func TestGetAgenciesForStops_ReadsStopAgencyIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_agencies LIMIT 1").Scan(&stopID))
	_, err := client.DB.ExecContext(ctx, "DELETE FROM stop_agencies WHERE stop_id = ?", stopID)
	require.NoError(t, err)

	rows, err := client.Queries.GetAgenciesForStops(ctx, []string{stopID})

	require.NoError(t, err)
	assert.Empty(t, rows)
}

// Reading the index must return the same rows, in the same order, as walking
// stop_times did, including for a stop served by two agencies.
func TestGetAgenciesForStops_MatchesStopTimesJoin(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()
	addSecondAgencyAtStop(t, client)

	stopIDRows, err := client.DB.QueryContext(ctx, "SELECT id FROM stops ORDER BY id")
	require.NoError(t, err)
	var stopIDs []string
	for stopIDRows.Next() {
		var id string
		require.NoError(t, stopIDRows.Scan(&id))
		stopIDs = append(stopIDs, id)
	}
	require.NoError(t, stopIDRows.Err())
	require.NoError(t, stopIDRows.Close())

	joinRows, err := client.DB.QueryContext(ctx, `
		SELECT DISTINCT a.id, a.name, a.url, a.timezone, a.lang, a.phone, a.fare_url, a.email, stop_times.stop_id
		FROM stop_times
		JOIN trips ON stop_times.trip_id = trips.id
		JOIN routes ON trips.route_id = routes.id
		JOIN agencies a ON routes.agency_id = a.id
		ORDER BY stop_times.stop_id ASC, a.id ASC
	`)
	require.NoError(t, err)
	var want []GetAgenciesForStopsRow
	for joinRows.Next() {
		var row GetAgenciesForStopsRow
		require.NoError(t, joinRows.Scan(&row.ID, &row.Name, &row.Url, &row.Timezone, &row.Lang,
			&row.Phone, &row.FareUrl, &row.Email, &row.StopID))
		want = append(want, row)
	}
	require.NoError(t, joinRows.Err())
	require.NoError(t, joinRows.Close())
	require.NotEmpty(t, want)

	got, err := client.Queries.GetAgenciesForStops(ctx, stopIDs)

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// addSecondAgencyAtStop makes one RABA stop also served by a second agency and
// rebuilds the index, so the stop has two (stop, agency) pairs to order.
func addSecondAgencyAtStop(t *testing.T, client *Client) {
	t.Helper()
	ctx := context.Background()

	var stopID string
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT stop_id FROM stop_agencies LIMIT 1").Scan(&stopID))

	for _, stmt := range []string{
		`INSERT INTO agencies (id, name, url, timezone) VALUES ('0-first-agency', 'First Agency', 'http://example.com', 'UTC')`,
		`INSERT INTO routes (id, agency_id, short_name, type) VALUES ('first-route', '0-first-agency', 'F', 3)`,
		`INSERT INTO trips (id, route_id, service_id) VALUES ('first-trip', 'first-route', (SELECT service_id FROM trips LIMIT 1))`,
	} {
		_, err := client.DB.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_times (trip_id, stop_id, arrival_time, departure_time, stop_sequence)
		VALUES ('first-trip', ?, 0, 0, 0)
	`, stopID)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))
}
