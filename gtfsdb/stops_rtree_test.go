package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetActiveRoutesWithinBounds_CenteredOnStop checks that a query centered
// exactly on a stop doesn't fail. Rounding can push the distance formula's acos
// argument just above 1 there, which made acos return NULL and the scan fail.
// Stop 4000 (Canby Transfer Center) is one of the affected stops, so its routes
// are checked too.
func TestGetActiveRoutesWithinBounds_CenteredOnStop(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	stops, err := client.Queries.ListStops(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	const boxSpan = 0.0005
	var stop4000RouteIDs []string
	for _, stop := range stops {
		routes, err := client.Queries.GetActiveRoutesWithinBounds(ctx, GetActiveRoutesWithinBoundsParams{
			Lat:      stop.Lat,
			Lon:      stop.Lon,
			MinLat:   stop.Lat - boxSpan,
			MaxLat:   stop.Lat + boxSpan,
			MinLon:   stop.Lon - boxSpan,
			MaxLon:   stop.Lon + boxSpan,
			MaxCount: 100,
		})
		assert.NoError(t, err, "query centered on stop %s", stop.ID)
		if stop.ID == "4000" {
			for _, route := range routes {
				stop4000RouteIDs = append(stop4000RouteIDs, route.ID)
			}
		}
	}

	assert.ElementsMatch(t, []string{"15", "154", "159", "160", "44X"}, stop4000RouteIDs)
}

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
