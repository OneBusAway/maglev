package gtfsdb

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeDatabase(t *testing.T) {
	client := newTestClientWithRABA(t)
	ctx := t.Context()

	// 1. Verify that StoreGtfsData runs OptimizeDatabase, populating sqlite_stat1.
	var statCount int
	err := client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_stat1").Scan(&statCount)
	require.NoError(t, err, "sqlite_stat1 table should exist and be queryable")
	assert.Greater(t, statCount, 0, "sqlite_stat1 should contain index statistics after import")

	// Verify key indexes have statistics populated.
	var bteStatCount int
	err = client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl = 'block_trip_entry'").Scan(&bteStatCount)
	require.NoError(t, err)
	assert.Greater(t, bteStatCount, 0, "sqlite_stat1 should contain statistics for block_trip_entry")

	// 2. Verify OptimizeDatabase is safe to call directly and idempotent.
	require.NoError(t, client.OptimizeDatabase(ctx))

	// 3. Verify query planner utilizes statistics for selective index lookups.
	var routeID, serviceID string
	var indexID int64
	require.NoError(t, client.DB.QueryRowContext(ctx, `
		SELECT trips.route_id, bte.service_id, bte.block_trip_index_id
		FROM block_trip_entry bte JOIN trips ON trips.id = bte.trip_id
		WHERE bte.block_id IS NOT NULL LIMIT 1`).Scan(&routeID, &serviceID, &indexID))

	recorder := &queryRecorder{DBTX: client.DB}
	_, err = New(recorder).GetBlocksForBlockTripIndexIDs(ctx, GetBlocksForBlockTripIndexIDsParams{
		FromTime:         sql.NullInt64{Int64: 0, Valid: true},
		ToTime:           sql.NullInt64{Int64: int64(24 * time.Hour), Valid: true},
		RouteID:          routeID,
		IndexIds:         []int64{indexID},
		ActiveServiceIds: []string{serviceID},
		RouteServiceIds:  []string{serviceID},
	})
	require.NoError(t, err)

	plan := strings.Join(explainRecordedQuery(t, client, recorder), "\n")
	assert.Contains(t, plan, "idx_block_trip_entry_index_id", "query plan should use index_id index based on statistics")
	assert.NotContains(t, plan, "idx_block_trip_entry_service_id", "query plan should not scan by service_id when statistics are available")
}
