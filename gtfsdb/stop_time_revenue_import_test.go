package gtfsdb

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestImportedStopTimesOmittingPickupColumns covers a feed whose stop_times.txt
// declares neither pickup_type nor drop_off_type. GTFS makes both optional and
// defines an empty value as 0, so every stop time in such a feed permits
// unrestricted pick-up and drop-off and must survive the revenue-service filter
// in searchStopsByName.
//
// This goes through the real import path rather than inserting rows directly,
// because the defect lives in parsing, not in our SQL: the pinned go-gtfs
// v1.1.1 stores an absent column as 1 ("not allowed"), which makes the filter
// drop every stop in such a feed and empties /api/where/search/stop.json.
//
// So this test fails today, on purpose. It goes red until
// https://github.com/OneBusAway/go-gtfs/pull/5 is released and go.mod is
// bumped to a version that parses a blank or absent column as 0.
func TestImportedStopTimesOmittingPickupColumns(t *testing.T) {
	client, err := NewClient(Config{
		DBPath: ":memory:",
		Env:    appconf.Test,
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	ctx := context.Background()

	// buildSyntheticGTFSZip's stop_times.txt header is
	// "trip_id,arrival_time,departure_time,stop_id,stop_sequence" - both pickup
	// columns are absent, which is exactly the shape under test.
	parsed, err := ParseGtfsData(buildSyntheticGTFSZip(t, false), "synthetic-test")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	t.Run("stores omitted pickup and drop-off types as unrestricted", func(t *testing.T) {
		var restricted int
		err := client.DB.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM stop_times
			WHERE COALESCE(pickup_type, 0) != 0
			   OR COALESCE(drop_off_type, 0) != 0
		`).Scan(&restricted)
		require.NoError(t, err)
		assert.Zero(t, restricted, "an absent pickup_type/drop_off_type column means 0, so no stop time may store a restricted value")
	})

	t.Run("keeps stops from such a feed searchable", func(t *testing.T) {
		results, err := client.Queries.SearchStopsByName(ctx, SearchStopsByNameParams{
			SearchQuery: "Stop",
			Limit:       10,
		})
		require.NoError(t, err)
		assert.Len(t, results, 3, "the revenue-service filter must not drop stops whose feed omits the pickup columns")
	})
}
