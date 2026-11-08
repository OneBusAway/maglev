package gtfsdb

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetBlocksForBlockTripIndexIDs joins block_trip_entry to itself on block_id. Without
// an index there, SQLite scans every block entry for every route trip, which made
// trips-for-route take seconds on NJ Transit's 33k-trip feed.
func TestBlockTripEntryBlockIDJoinUsesIndex(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := t.Context()

	var routeID, serviceID string
	var indexID int64
	require.NoError(t, client.DB.QueryRowContext(ctx, `
		SELECT trips.route_id, bte.service_id, bte.block_trip_index_id
		FROM block_trip_entry bte JOIN trips ON trips.id = bte.trip_id
		WHERE bte.block_id IS NOT NULL LIMIT 1`).Scan(&routeID, &serviceID, &indexID))

	recorder := &queryRecorder{DBTX: client.DB}
	_, err := New(recorder).GetBlocksForBlockTripIndexIDs(ctx, GetBlocksForBlockTripIndexIDsParams{
		FromTime:         sql.NullInt64{Int64: 0, Valid: true},
		ToTime:           sql.NullInt64{Int64: int64(24 * time.Hour), Valid: true},
		RouteID:          routeID,
		IndexIds:         []int64{indexID},
		ActiveServiceIds: []string{serviceID},
		RouteServiceIds:  []string{serviceID},
	})
	require.NoError(t, err)

	// The planner may start the self-join from either side, so accept the index
	// on either alias.
	plan := strings.Join(explainRecordedQuery(t, client, recorder), "\n")
	assert.Regexp(t, `SEARCH \w+ USING (COVERING )?INDEX idx_block_trip_entry_block_id \(block_id=\?`, plan)
}
