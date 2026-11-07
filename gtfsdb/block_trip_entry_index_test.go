package gtfsdb

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// GetBlocksForBlockTripIndexIDs joins block_trip_entry to itself on block_id. Without
// an index there, SQLite scans every block entry for every route trip, which made
// trips-for-route take seconds on NJ Transit's 33k-trip feed.
func TestBlockTripEntryBlockIDJoinUsesIndex(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	// Explain the generated query itself, with one value in each IN list, so the
	// test follows any change to its joins and filters.
	sliceMarker := regexp.MustCompile(`/\*SLICE:\w+\*/\?`)
	query := sliceMarker.ReplaceAllString(getBlocksForBlockTripIndexIDs, "?")
	fromTime, toTime, routeID := int64(0), int64(3600), "route_1"
	indexID, activeServiceID, routeServiceID := int64(1), "service_1", "service_1"
	rows, err := client.DB.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query,
		fromTime, toTime, routeID, indexID, activeServiceID, routeServiceID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		plan = append(plan, detail)
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, strings.Join(plan, "\n"),
		"SEARCH route_bte USING INDEX idx_block_trip_entry_block_id (block_id=?)")
}
