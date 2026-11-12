package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreGtfsData_DuplicateStopIDLeavesOneIndexRow checks that a feed with
// the same stop_id twice ends up with exactly one stops_rtree and one
// stops_fts row for that stop. INSERT OR REPLACE deletes the first row before
// inserting the second, and SQLite only fires DELETE triggers for that
// implicit delete when recursive_triggers is on, so without it the first
// row's index entries stay behind and point at a rowid that no longer exists.
func TestStoreGtfsData_DuplicateStopIDLeavesOneIndexRow(t *testing.T) {
	feed := zipFiles(t, map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_type\nr,a,3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ns,1,1,1,1,1,0,0,20240101,20251231\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\ndup,Old Name,47.6,-122.3\nother,Other,47.7,-122.4\ndup,New Name,47.61,-122.31\n",
		"trips.txt":      "route_id,service_id,trip_id\nr,s,t\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nt,08:00:00,08:00:00,dup,1\nt,08:10:00,08:10:00,other,2\n",
	})
	parsed, err := ParseGtfsData(feed, "duplicate-stop")
	require.NoError(t, err)

	client := newTestClient(t)
	ctx := t.Context()
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	var stopCount, rtreeCount int
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stops").Scan(&stopCount))
	require.NoError(t, client.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM stops_rtree").Scan(&rtreeCount))
	assert.Equal(t, 2, stopCount)
	assert.Equal(t, stopCount, rtreeCount, "stops_rtree should hold one row per stop")

	var orphanedRtreeRows int
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM stops_rtree WHERE id NOT IN (SELECT rowid FROM stops)").Scan(&orphanedRtreeRows))
	assert.Equal(t, 0, orphanedRtreeRows, "stops_rtree should not point at deleted rowids")

	rows, err := client.DB.QueryContext(ctx, "SELECT stop_name FROM stops_fts WHERE id = 'dup'")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var ftsNames []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		ftsNames = append(ftsNames, name)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"New Name"}, ftsNames, "stops_fts should hold only the last row for a duplicated stop_id")
}

// TestStoreGtfsData_DuplicateRouteIDLeavesOneIndexRow checks that a feed with
// the same route_id twice leaves only the last row's text in routes_fts. The
// REPLACE delete has to fire routes_fts_ad, or the first row's terms keep
// matching.
func TestStoreGtfsData_DuplicateRouteIDLeavesOneIndexRow(t *testing.T) {
	feed := zipFiles(t, map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_long_name,route_type\nr,a,Oldline,3\nr,a,Newline,3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ns,1,1,1,1,1,0,0,20240101,20251231\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nx,X,47.6,-122.3\n",
		"trips.txt":      "route_id,service_id,trip_id\nr,s,t\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nt,08:00:00,08:00:00,x,1\n",
	})
	parsed, err := ParseGtfsData(feed, "duplicate-route")
	require.NoError(t, err)

	client := newTestClient(t)
	ctx := t.Context()
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	var oldMatches, newMatches int
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM routes_fts WHERE routes_fts MATCH 'Oldline'").Scan(&oldMatches))
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM routes_fts WHERE routes_fts MATCH 'Newline'").Scan(&newMatches))
	assert.Equal(t, 0, oldMatches, "routes_fts should not match the replaced route's name")
	assert.Equal(t, 1, newMatches, "routes_fts should match the last row for a duplicated route_id")
}
