package restapi

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

// rebuildStopIndexes rebuilds every index derived from stop_times (stop_routes and
// stop_agencies) the way an import does. Tests that insert stops, trips or stop_times
// straight into the database must call it afterwards; otherwise the route and agency
// lookups miss the new rows and the stops silently drop out of responses.
func rebuildStopIndexes(t *testing.T, api *RestAPI) {
	t.Helper()
	ctx := context.Background()
	queries := api.GtfsManager.GtfsDB.Queries
	require.NoError(t, queries.ClearStopRoutes(ctx))
	require.NoError(t, queries.BuildStopRoutes(ctx))
	require.NoError(t, queries.ClearStopAgencies(ctx))
	require.NoError(t, queries.BuildStopAgencies(ctx))
}

// registerStopFixtureCleanup is registerFixtureCleanup for fixtures that add stops:
// it first removes the stops' rows from every stop index, which would otherwise block
// deleting the stops, routes or agencies they point at. stopIDs is a quoted SQL list,
// e.g. `'a', 'b'`.
func registerStopFixtureCleanup(t *testing.T, db *sql.DB, stopIDs string, statements ...string) {
	t.Helper()
	indexCleanup := []string{
		`DELETE FROM stop_routes WHERE stop_id IN (` + stopIDs + `)`,
		`DELETE FROM stop_agencies WHERE stop_id IN (` + stopIDs + `)`,
	}
	registerFixtureCleanup(t, db, append(indexCleanup, statements...)...)
}
