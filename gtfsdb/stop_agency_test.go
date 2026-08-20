package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStopAgencyIndex_PopulatesTable(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var indexed int
	err := client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stop_agencies").Scan(&indexed)
	require.NoError(t, err)
	assert.Greater(t, indexed, 0, "RABA feed should produce at least one indexed stop")

	// Every stop a route serves must be indexed, and no other stop may be.
	var missing int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT DISTINCT stop_times.stop_id FROM stop_times
			EXCEPT
			SELECT stop_id FROM stop_agencies
		)
	`).Scan(&missing)
	require.NoError(t, err)
	assert.Zero(t, missing, "every stop with a stop time should have an indexed agency")

	var stray int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stop_agencies
		WHERE stop_id NOT IN (SELECT stop_id FROM stop_times)
	`).Scan(&stray)
	require.NoError(t, err)
	assert.Zero(t, stray, "a stop no route serves should not be indexed")

	// The indexed agency must be the lowest one serving the stop.
	var mismatched int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stop_agencies sa
		WHERE sa.agency_id != (
			SELECT MIN(routes.agency_id)
			FROM stop_times
			JOIN trips ON stop_times.trip_id = trips.id
			JOIN routes ON trips.route_id = routes.id
			WHERE stop_times.stop_id = sa.stop_id
		)
	`).Scan(&mismatched)
	require.NoError(t, err)
	assert.Zero(t, mismatched, "indexed agency should be the lowest agency serving the stop")
}

func TestBuildStopAgencyIndex_RebuildsFromScratch(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// A stale row from a previous feed version must not survive a rebuild.
	_, err := client.DB.ExecContext(ctx, `
		INSERT INTO stop_agencies (stop_id, agency_id)
		SELECT id, (SELECT id FROM agencies LIMIT 1) FROM stops
		WHERE id NOT IN (SELECT stop_id FROM stop_agencies) LIMIT 1
	`)
	require.NoError(t, err)

	require.NoError(t, buildStopAgencyIndex(ctx, client.Queries))

	var stray int
	err = client.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM stop_agencies
		WHERE stop_id NOT IN (SELECT stop_id FROM stop_times)
	`).Scan(&stray)
	require.NoError(t, err)
	assert.Zero(t, stray, "rebuild should drop rows for stops no route serves")
}
