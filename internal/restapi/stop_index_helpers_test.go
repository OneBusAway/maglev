package restapi

import (
	"context"
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
