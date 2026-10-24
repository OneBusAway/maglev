package gtfsdb

import (
	"bytes"
	"strings"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// consolidationFeed has a canonical stop, an absorbed stop whose stop times
// move to it, an absorbed stop whose stop times are dropped, and children of
// both absorbed stops.
func consolidationFeed(t *testing.T) *GtfsData {
	t.Helper()
	feed := zipFiles(t, map[string]string{
		"agency.txt":   "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":   "route_id,agency_id,route_type\nr,a,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ns,1,1,1,1,1,0,0,20240101,20251231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon,location_type,parent_station\n" +
			"canonical,Canonical,47.6,-122.3,1,\n" +
			"moved,Moved,47.60001,-122.3,1,\n" +
			"dropped,Dropped,47.7,-122.4,1,\n" +
			"mid,Mid,47.65,-122.35,0,\n" +
			"child_of_moved,Child,47.60002,-122.3,0,moved\n" +
			"child_of_dropped,Child,47.70001,-122.4,0,dropped\n",
		"trips.txt": "route_id,service_id,trip_id,block_id\nr,s,t1,b\nr,s,only_dropped,b\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"t1,08:00:00,08:00:00,moved,1\n" +
			"t1,08:10:00,08:10:00,mid,2\n" +
			"t1,08:20:00,08:20:00,dropped,3\n" +
			"only_dropped,09:00:00,09:00:00,dropped,1\n",
	})
	data, err := ParseGtfsStream(bytes.NewReader(feed), int64(len(feed)), strings.Repeat("a", 64), "source")
	require.NoError(t, err)

	stops := make(map[string]*gtfs.Stop)
	for i := range data.Static.Stops {
		stops[data.Static.Stops[i].Id] = &data.Static.Stops[i]
	}
	data.StopReplacements = map[string]StopReplacement{
		"moved":   {RuleCanonical: "a_canonical", Canonical: stops["canonical"]},
		"dropped": {RuleCanonical: "x_gone"},
	}
	return data
}

func TestStoreGtfsData_ConsolidatesStops(t *testing.T) {
	client := newTestClient(t)
	_, err := client.StoreGtfsData(t.Context(), consolidationFeed(t))
	require.NoError(t, err)

	t.Run("absorbed stops are not stored", func(t *testing.T) {
		assert.ElementsMatch(t,
			[]string{"canonical", "mid", "child_of_moved", "child_of_dropped"},
			queryStrings(t, client, `SELECT id FROM stops`))
	})

	t.Run("stop times move to the canonical stop or are dropped", func(t *testing.T) {
		assert.Equal(t,
			[]string{"t1 1 canonical", "t1 2 mid"},
			queryStrings(t, client, `SELECT trip_id || ' ' || stop_sequence || ' ' || stop_id FROM stop_times ORDER BY trip_id, stop_sequence`))
	})

	t.Run("children follow their parent", func(t *testing.T) {
		assert.Equal(t,
			[]string{"child_of_dropped ", "child_of_moved canonical"},
			queryStrings(t, client, `SELECT id || ' ' || COALESCE(parent_station, '') FROM stops WHERE id LIKE 'child%' ORDER BY id`))
	})

	t.Run("each absorbed stop is recorded", func(t *testing.T) {
		rows, err := client.Queries.ListStopConsolidations(t.Context())
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, "dropped", rows[0].AbsorbedID)
		assert.False(t, rows[0].CanonicalID.Valid)
		assert.Equal(t, "x_gone", rows[0].RuleCanonical)
		assert.Equal(t, "moved", rows[1].AbsorbedID)
		assert.Equal(t, "canonical", rows[1].CanonicalID.String)
		assert.Equal(t, "Moved", rows[1].AbsorbedName.String)
		assert.InDelta(t, 47.60001, rows[1].AbsorbedLat.Float64, 1e-9)
	})
}

func TestFeedRows_TripEndsUseConsolidatedStops(t *testing.T) {
	rows := consolidationFeed(t).rows()
	for range rows.stopTimes() {
	}
	require.NoError(t, rows.err)

	require.Len(t, rows.tripEnds, 1, "a trip whose stop times are all dropped has no ends")
	assert.Equal(t, "t1", rows.tripEnds[0].tripID)
	assert.Equal(t, "canonical", rows.tripEnds[0].firstStopID)
	assert.Equal(t, "mid", rows.tripEnds[0].lastStopID)
}

func TestStoreGtfsData_ReimportClearsStopConsolidations(t *testing.T) {
	client := newTestClient(t)
	_, err := client.StoreGtfsData(t.Context(), consolidationFeed(t))
	require.NoError(t, err)

	data := consolidationFeed(t)
	data.Hash = strings.Repeat("b", 64)
	data.StopReplacements = nil
	_, err = client.StoreGtfsData(t.Context(), data)
	require.NoError(t, err)

	rows, err := client.Queries.ListStopConsolidations(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func queryStrings(t *testing.T, client *Client, query string) []string {
	t.Helper()
	rows, err := client.DB.Query(query)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		values = append(values, value)
	}
	require.NoError(t, rows.Err())
	return values
}
