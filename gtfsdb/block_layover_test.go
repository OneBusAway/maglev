package gtfsdb

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// newTestClientWithRABA builds a fresh in-memory client populated from testdata/raba.zip.
func newTestClientWithRABA(t *testing.T) *Client {
	t.Helper()

	client, err := NewClient(Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)

	parsed, err := ParseGtfsData(rabaBytes, "test-raba")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)

	return client
}

func TestBuildBlockLayoverIndex_PopulatesTable(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var total int
	err := client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&total)
	require.NoError(t, err)
	assert.Greater(t, total, 0, "RABA feed should produce at least one block layover")

	// Invariants: every row has the required fields and layover_start <= layover_end.
	rows, err := client.DB.QueryContext(ctx, `
		SELECT block_id, route_id, service_id, layover_stop_id, next_trip_id,
		       layover_start, layover_end
		FROM block_layover`)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var blockID, routeID, serviceID, stopID, nextTripID string
		var start, end int64
		require.NoError(t, rows.Scan(&blockID, &routeID, &serviceID, &stopID, &nextTripID, &start, &end))

		assert.NotEmpty(t, blockID)
		assert.NotEmpty(t, routeID)
		assert.NotEmpty(t, serviceID)
		assert.NotEmpty(t, stopID)
		assert.NotEmpty(t, nextTripID)
		assert.LessOrEqual(t, start, end, "layover_start must not exceed layover_end")
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, total, checked)
}

func TestBuildBlockLayoverIndex_RebuildOnReimport(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	var first int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&first))
	require.Greater(t, first, 0)

	// Reimport the same feed; the builder clears the table first, so the row
	// count must stay identical (not double).
	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	parsed, err := ParseGtfsData(rabaBytes, "test-raba-reimport")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	var second int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM block_layover").Scan(&second))
	assert.Equal(t, first, second, "reimport must not accumulate duplicate layover rows")
}

func TestGetActiveLayoverBlockIDsForRoute_MatchesWindow(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := context.Background()

	// Pull a real layover from the built index to drive the query with known-good inputs.
	var routeID, serviceID, blockID string
	var start, end int64
	err := client.DB.QueryRowContext(ctx,
		`SELECT route_id, service_id, block_id, layover_start, layover_end FROM block_layover LIMIT 1`,
	).Scan(&routeID, &serviceID, &blockID, &start, &end)
	require.NoError(t, err)

	// Window that straddles the layover: [start-1s, end+1s] must match.
	hits, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{serviceID},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Contains(t, hits, blockID, "block should be returned when window overlaps its layover")

	// Window entirely after the layover: [end+1s, end+2s] must NOT match.
	miss, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{serviceID},
		TimeRangeStart: end + 1_000_000_000,
		TimeRangeEnd:   end + 2_000_000_000,
	})
	require.NoError(t, err)
	assert.NotContains(t, miss, blockID, "block must not be returned when window is after its layover")

	// Wrong route: must return empty.
	wrongRoute, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        "__not_a_real_route__",
		ServiceIds:     []string{serviceID},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Empty(t, wrongRoute)

	// Wrong service: must return empty.
	wrongService, err := client.Queries.GetActiveLayoverBlockIDsForRoute(ctx, GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     []string{"__not_a_real_service__"},
		TimeRangeStart: start - 1_000_000_000,
		TimeRangeEnd:   end + 1_000_000_000,
	})
	require.NoError(t, err)
	assert.Empty(t, wrongService)
}
