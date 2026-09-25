package gtfsdb

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/flexfixtures"
)

// newTestClientWithZip imports a GTFS zip from disk into a fresh in-memory client.
func newTestClientWithZip(t *testing.T, zipPath string) *Client {
	t.Helper()
	bytes, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	return newTestClientWithBytes(t, bytes, zipPath)
}

// newTestClientWithBytes imports GTFS zip bytes into a fresh in-memory client.
func newTestClientWithBytes(t *testing.T, zipBytes []byte, source string) *Client {
	t.Helper()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	parsed, err := ParseGtfsData(zipBytes, source)
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	return client
}

func countRows(t *testing.T, client *Client, table string) int {
	t.Helper()
	var n int
	require.NoError(t, client.DB.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestStoreGtfsData_AlexandriaFlexImports(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/alexandria-flex.zip")

	assert.Equal(t, 1, countRows(t, client, "agencies"))
	assert.Equal(t, 1, countRows(t, client, "routes"))
	assert.Equal(t, 1, countRows(t, client, "stops"))
	assert.Equal(t, 2, countRows(t, client, "trips"))
	assert.Equal(t, 0, countRows(t, client, "stop_times"), "windowed records never land in stop_times")

	trip, err := client.Queries.GetTrip(context.Background(), "t_6124961_b_85952_tn_0")
	require.NoError(t, err)
	assert.False(t, trip.MinArrivalTime.Valid, "a flex-only trip has no cached time bounds")
}

func TestStoreGtfsData_ZeroStopsFeedImports(t *testing.T) {
	client := newTestClientWithBytes(t, flexfixtures.ZipBytes(t, flexfixtures.ZeroStopsFiles()), "flex-zero-stops")

	assert.Equal(t, 0, countRows(t, client, "stops"))
	assert.Equal(t, 22, countRows(t, client, "trips"))
	assert.Equal(t, 0, countRows(t, client, "stop_times"))
	assert.Equal(t, 0, countRows(t, client, "block_layover"))
}

func TestStoreGtfsData_DeviatedFeedIndexesTimedRecordsOnly(t *testing.T) {
	client := newTestClientWithBytes(t, flexfixtures.ZipBytes(t, flexfixtures.GroupDeviatedFiles()), "flex-group-deviated")

	assert.Equal(t, 6, countRows(t, client, "stop_times"), "3 timed rows on her-trip + 3 on her-trip-2")
	assert.Equal(t, 7, countRows(t, client, "stops"))
	assert.Equal(t, 4, countRows(t, client, "trips"), "the group and windowed-stop trips survive validation")

	// her-trip ends at h3 09:40 and her-trip-2 starts at h3 10:00 in the same block:
	// the layover index must be built from the timed records around the zone rows.
	assert.Equal(t, 1, countRows(t, client, "block_layover"))

	var indexed int
	require.NoError(t, client.DB.QueryRow(
		"SELECT COUNT(*) FROM block_trip_entry WHERE trip_id IN ('her-trip','her-trip-2')").Scan(&indexed))
	assert.Equal(t, 2, indexed)
	require.NoError(t, client.DB.QueryRow(
		"SELECT COUNT(*) FROM block_trip_entry WHERE trip_id IN ('ruf-trip','win-trip')").Scan(&indexed))
	assert.Equal(t, 0, indexed, "trips with no timed records are skipped by the block index")
}
