package gtfsdb

import (
	"context"
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

	rows, err := client.DB.QueryContext(context.Background(), `EXPLAIN QUERY PLAN
		SELECT DISTINCT a.block_id FROM block_trip_entry a
		JOIN block_trip_entry b ON b.block_id = a.block_id
		WHERE a.trip_id = 't1'`)
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

	assert.Contains(t, strings.Join(plan, "\n"), "idx_block_trip_entry_block_id")
}
