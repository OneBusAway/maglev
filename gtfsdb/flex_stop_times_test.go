package gtfsdb

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flexFeedFiles is a one-route feed with a regular trip T1 over stops S1-S3
// and a location group G1. Tests add their own trips and stop times.
func flexFeedFiles() map[string]string {
	return map[string]string{
		"agency.txt":               "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":               "route_id,agency_id,route_type\nR1,a,3\n",
		"calendar.txt":             "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nWK,1,1,1,1,1,0,0,20240101,20301231\n",
		"stops.txt":                "stop_id,stop_name,stop_lat,stop_lon\nS1,One,47.60,-122.30\nS2,Two,47.61,-122.31\nS3,Three,47.62,-122.32\n",
		"location_groups.txt":      "location_group_id,location_group_name\nG1,Group\n",
		"location_group_stops.txt": "location_group_id,stop_id\nG1,S1\nG1,S2\n",
	}
}

// flexLocationFeedFiles is like flexFeedFiles but uses a locations.geojson
// polygon instead of a location group, covering the static.Locations != nil
// branch of hasFlexLocations.
func flexLocationFeedFiles() map[string]string {
	return map[string]string{
		"agency.txt":        "agency_id,agency_name,agency_url,agency_timezone\na,A,http://a,America/Los_Angeles\n",
		"routes.txt":        "route_id,agency_id,route_type\nR1,a,3\n",
		"calendar.txt":      "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nWK,1,1,1,1,1,0,0,20240101,20301231\n",
		"stops.txt":         "stop_id,stop_name,stop_lat,stop_lon\nS1,One,47.60,-122.30\nS2,Two,47.61,-122.31\nS3,Three,47.62,-122.32\n",
		"locations.geojson": `{"type":"FeatureCollection","features":[{"type":"Feature","id":"Z1","properties":{"stop_name":"Zone"},"geometry":{"type":"Polygon","coordinates":[[[-122.30,47.60],[-122.31,47.61],[-122.32,47.62],[-122.30,47.60]]]}}]}`,
	}
}

const flexStopTimesHeader = "trip_id,arrival_time,departure_time,stop_id,location_group_id,stop_sequence,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type\n"

// flexLocationStopTimesHeader is like flexStopTimesHeader but references
// location_id instead of location_group_id, for feeds that use
// locations.geojson.
const flexLocationStopTimesHeader = "trip_id,arrival_time,departure_time,stop_id,location_id,stop_sequence,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_type,drop_off_type\n"

// importFlexFeed streams and stores a feed, returning its trip IDs and each
// trip's stop IDs in order.
func importFlexFeed(t *testing.T, files map[string]string) ([]string, map[string][]string) {
	t.Helper()
	feed := zipFiles(t, files)
	data, err := ParseGtfsStream(bytes.NewReader(feed), int64(len(feed)), FeedIdentity{Hash: strings.Repeat("f", 64), Source: "flex"})
	require.NoError(t, err)
	client := newTestClient(t)
	_, err = client.StoreGtfsData(t.Context(), data)
	require.NoError(t, err)

	rows, err := client.DB.Query(`SELECT id FROM trips ORDER BY id`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var tripIDs []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		tripIDs = append(tripIDs, id)
	}
	require.NoError(t, rows.Err())

	stopIDs := make(map[string][]string, len(tripIDs))
	for _, tripID := range tripIDs {
		stopTimes, err := client.Queries.GetStopTimesForTrip(t.Context(), tripID)
		require.NoError(t, err)
		for _, stopTime := range stopTimes {
			stopIDs[tripID] = append(stopIDs[tripID], stopTime.StopID)
		}
	}
	return tripIDs, stopIDs
}

// go-gtfs 1.5.0 keeps stop times that serve a location group instead of a
// stop (#2209). A trip that serves only location groups is stored, but its
// windowed records go to flex_stop_times, not stop_times.
func TestStreamedImportStoresTripsWithOnlyFlexStopTimes(t *testing.T) {
	files := flexFeedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id\nR1,WK,T1\nR1,WK,FLEX\n"
	files["stop_times.txt"] = flexStopTimesHeader +
		"T1,06:00:00,06:00:00,S1,,1,,,,\n" +
		"T1,06:10:00,06:10:00,S2,,2,,,,\n" +
		"T1,06:20:00,06:20:00,S3,,3,,,,\n" +
		"FLEX,,,,G1,1,08:00:00,18:00:00,2,1\n" +
		"FLEX,,,,G1,2,08:00:00,18:00:00,1,2\n"

	tripIDs, stopIDs := importFlexFeed(t, files)

	assert.Equal(t, []string{"FLEX", "T1"}, tripIDs)
	assert.Empty(t, stopIDs["FLEX"])
	assert.Equal(t, []string{"S1", "S2", "S3"}, stopIDs["T1"])
}

// A fixed-route trip that deviates into a location group keeps its stop
// times at stops in stop_times; the deviation is not one of them (#2209).
func TestStreamedImportKeepsStopsOfDeviatedTrip(t *testing.T) {
	files := flexFeedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id\nR1,WK,T1\n"
	files["stop_times.txt"] = flexStopTimesHeader +
		"T1,06:00:00,06:00:00,S1,,1,,,0,1\n" +
		"T1,,,,G1,2,06:05:00,06:15:00,2,2\n" +
		"T1,06:20:00,06:20:00,S3,,3,,,1,0\n"

	tripIDs, stopIDs := importFlexFeed(t, files)

	assert.Equal(t, []string{"T1"}, tripIDs)
	assert.Equal(t, []string{"S1", "S3"}, stopIDs["T1"])
}

// A deviated trip with one stop time at a stop still has two records, so it
// is stored with that one stop time in stop_times.
func TestStreamedImportKeepsDeviatedTripWithOneStop(t *testing.T) {
	files := flexFeedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id\nR1,WK,T1\nR1,WK,SHORT\n"
	files["stop_times.txt"] = flexStopTimesHeader +
		"T1,06:00:00,06:00:00,S1,,1,,,,\n" +
		"T1,06:20:00,06:20:00,S3,,2,,,,\n" +
		"SHORT,07:00:00,07:00:00,S1,,1,,,0,1\n" +
		"SHORT,,,,G1,2,07:05:00,07:15:00,2,1\n"

	tripIDs, stopIDs := importFlexFeed(t, files)

	assert.Equal(t, []string{"SHORT", "T1"}, tripIDs)
	assert.Equal(t, []string{"S1"}, stopIDs["SHORT"])
}

// A trip that serves only a GeoJSON location (locations.geojson, not
// location_groups.txt) is stored with no stop_times rows, and a regular trip
// in the same feed loads normally.
func TestStreamedImportStoresTripsWithOnlyLocationIDStopTimes(t *testing.T) {
	files := flexLocationFeedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id\nR1,WK,T1\nR1,WK,ZONE\n"
	files["stop_times.txt"] = flexLocationStopTimesHeader +
		"T1,06:00:00,06:00:00,S1,,1,,,,\n" +
		"T1,06:10:00,06:10:00,S2,,2,,,,\n" +
		"T1,06:20:00,06:20:00,S3,,3,,,,\n" +
		"ZONE,,,,Z1,1,08:00:00,18:00:00,2,1\n" +
		"ZONE,,,,Z1,2,08:00:00,18:00:00,1,2\n"

	tripIDs, stopIDs := importFlexFeed(t, files)

	assert.Equal(t, []string{"T1", "ZONE"}, tripIDs)
	assert.Empty(t, stopIDs["ZONE"])
	assert.Equal(t, []string{"S1", "S2", "S3"}, stopIDs["T1"])
}

// A deviated trip that mixes stops and a GeoJSON location keeps its stop
// times at stops in stop_times; the location deviation is not one of them.
func TestStreamedImportKeepsStopsOfLocationDeviatedTrip(t *testing.T) {
	files := flexLocationFeedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id\nR1,WK,T1\n"
	files["stop_times.txt"] = flexLocationStopTimesHeader +
		"T1,06:00:00,06:00:00,S1,,1,,,0,1\n" +
		"T1,,,,Z1,2,06:05:00,06:15:00,2,2\n" +
		"T1,06:20:00,06:20:00,S3,,3,,,1,0\n"

	tripIDs, stopIDs := importFlexFeed(t, files)

	assert.Equal(t, []string{"T1"}, tripIDs)
	assert.Equal(t, []string{"S1", "S3"}, stopIDs["T1"])
}
