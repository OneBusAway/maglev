package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetActiveRoutesWithinBounds_EmptyRouteIDsMeansNoFilter pins the invariant that
// GetActiveRoutesWithinBoundsParams.RouteIDs being empty means "no filter" rather than
// "match nothing". Callers that derive RouteIDs from a candidate search (e.g.
// Manager.GetRoutesForLocation) must special-case an empty candidate list themselves
// before calling this, or they will silently get every in-bounds route instead of none.
func TestGetActiveRoutesWithinBounds_EmptyRouteIDsMeansNoFilter(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	const (
		centreLat = 40.583321
		centreLon = -122.362535
		boxSpan   = 0.05
	)

	unfiltered, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
		Lat:      centreLat,
		Lon:      centreLon,
		MinLat:   centreLat - boxSpan,
		MaxLat:   centreLat + boxSpan,
		MinLon:   centreLon - boxSpan,
		MaxLon:   centreLon + boxSpan,
		RouteIDs: nil,
		MaxCount: 100,
	})
	require.NoError(t, err)
	require.Greater(t, len(unfiltered), 1, "fixture box should contain more than one route")

	target := unfiltered[0].ID

	filtered, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
		Lat:      centreLat,
		Lon:      centreLon,
		MinLat:   centreLat - boxSpan,
		MaxLat:   centreLat + boxSpan,
		MinLon:   centreLon - boxSpan,
		MaxLon:   centreLon + boxSpan,
		RouteIDs: []string{target},
		MaxCount: 100,
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, target, filtered[0].ID)
}
