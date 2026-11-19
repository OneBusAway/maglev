package gtfsdb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/nulls"
)

// TestImportCSVHeadersWithSpaces covers feeds that put a space after each
// comma in the header row. The columns must still be found by name. Spaces
// in data cells are a different case that go-gtfs doesn't trim.
func TestImportCSVHeadersWithSpaces(t *testing.T) {
	files := map[string]string{
		// Two agencies, so a route whose agency_id column goes unread can't
		// fall back to the feed's only agency.
		"agency.txt":   "agency_id, agency_name, agency_url, agency_timezone\nagency_1,Spaced Transit,http://example.com,America/Los_Angeles\nagency_2,Other Transit,http://example.org,America/Los_Angeles\n",
		"routes.txt":   "route_id, agency_id, route_short_name, route_type\nroute_1,agency_1,R1,3\nroute_2,agency_2,R2,3\n",
		"calendar.txt": "service_id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date\nservice_1,1,1,1,1,1,0,0,20240101,20251231\n",
		"stops.txt":    "stop_id, stop_name, stop_lat, stop_lon\nstop_1,First Stop,37.7749,-122.4194\nstop_2,Second Stop,37.7849,-122.4094\n",
		"trips.txt":    "route_id, service_id, trip_id\nroute_1,service_1,trip_1\nroute_2,service_1,trip_2\n",
		// Arrival and departure differ at each stop, so each column is pinned.
		"stop_times.txt": "trip_id, arrival_time, departure_time, stop_id, stop_sequence\ntrip_1,06:00:00,06:01:00,stop_1,1\ntrip_1,06:10:00,06:11:00,stop_2,2\ntrip_2,07:00:00,07:00:00,stop_2,1\ntrip_2,07:10:00,07:10:00,stop_1,2\n",
	}

	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	parsed, err := ParseGtfsData(writeGTFSZip(t, files), "spaced-headers")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(t.Context(), parsed)
	require.NoError(t, err)

	stop, err := client.Queries.GetStop(t.Context(), "stop_2")
	require.NoError(t, err)
	assert.Equal(t, "Second Stop", nulls.StringOrEmpty(stop.Name))
	assert.InDelta(t, 37.7849, stop.Lat, 1e-9)

	route, err := client.Queries.GetRoute(t.Context(), "route_1")
	require.NoError(t, err)
	assert.Equal(t, "R1", nulls.StringOrEmpty(route.ShortName))
	assert.Equal(t, int64(3), route.Type)
	assert.Equal(t, "agency_1", route.AgencyID)

	otherRoute, err := client.Queries.GetRoute(t.Context(), "route_2")
	require.NoError(t, err)
	assert.Equal(t, "agency_2", otherRoute.AgencyID)

	calendar, err := client.Queries.GetCalendarByServiceID(t.Context(), "service_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), calendar.Monday)
	assert.Equal(t, int64(0), calendar.Saturday)

	stopTimes, err := client.Queries.GetStopTimesForTrip(t.Context(), "trip_1")
	require.NoError(t, err)
	require.Len(t, stopTimes, 2)
	assert.Equal(t, "stop_1", stopTimes[0].StopID)
	assert.Equal(t, int64(6*time.Hour), stopTimes[0].ArrivalTime)
	assert.Equal(t, int64(6*time.Hour+1*time.Minute), stopTimes[0].DepartureTime)
	assert.Equal(t, "stop_2", stopTimes[1].StopID)
	assert.Equal(t, int64(6*time.Hour+10*time.Minute), stopTimes[1].ArrivalTime)
	assert.Equal(t, int64(6*time.Hour+11*time.Minute), stopTimes[1].DepartureTime)
}
