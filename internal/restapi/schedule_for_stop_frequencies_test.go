package restapi

import (
	"archive/zip"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFrequencyFeed writes a GTFS feed with headway-based service to a temporary zip and
// returns its path. The RABA fixture has no frequencies.txt, so frequency behaviour needs
// a feed of its own.
//
// At stop_1 on a weekday, route_1 runs headway-based service in both directions, and
// route_2 mixes a headway-based trip with a fixed-schedule one in the same direction.
func writeFrequencyFeed(t testing.TB) string {
	t.Helper()

	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"1,Frequency Transit,http://example.com,America/Los_Angeles\n",

		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"route_1,1,R1,Route One,3\n" +
			"route_2,1,R2,Route Two,3\n" +
			"route_3,1,R3,Route Three,3\n",

		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"service_1,1,1,1,1,1,0,0,20240101,20251231\n",

		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stop_1,First Stop,37.7749,-122.4194\n" +
			"stop_2,Second Stop,37.7849,-122.4094\n",

		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id\n" +
			"route_1,service_1,trip_freq_out,Downtown via Freq,0\n" +
			"route_1,service_1,trip_freq_in,Uptown via Freq,1\n" +
			"route_2,service_1,trip_freq_mixed,Express,0\n" +
			"route_2,service_1,trip_fixed,Express,0\n" +
			"route_3,service_1,trip_exact,Exact Express,0\n",

		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"trip_freq_out,06:00:00,06:00:00,stop_1,1\n" +
			"trip_freq_out,06:10:00,06:10:00,stop_2,2\n" +
			"trip_freq_in,07:00:00,07:00:00,stop_2,1\n" +
			"trip_freq_in,07:10:00,07:10:00,stop_1,2\n" +
			"trip_freq_mixed,06:00:00,06:00:00,stop_1,1\n" +
			"trip_freq_mixed,06:20:00,06:20:00,stop_2,2\n" +
			"trip_fixed,08:00:00,08:00:00,stop_1,1\n" +
			"trip_fixed,08:20:00,08:20:00,stop_2,2\n" +
			// trip_exact starts at stop_2, so stop_1 sits five minutes into the trip.
			"trip_exact,06:00:00,06:00:00,stop_2,1\n" +
			"trip_exact,06:05:00,06:05:00,stop_1,2\n",

		// trip_freq_out runs every 10 minutes in the morning and every 15 in the evening;
		// the evening window is listed first so the response's ordering is load-bearing.
		// trip_exact is the only exact_times=1 trip: 06:00-08:00 every 30 minutes.
		"frequencies.txt": "trip_id,start_time,end_time,headway_secs,exact_times\n" +
			"trip_freq_out,16:00:00,19:00:00,900,0\n" +
			"trip_freq_out,06:00:00,09:00:00,600,0\n" +
			"trip_freq_in,07:00:00,10:00:00,720,0\n" +
			"trip_freq_mixed,06:00:00,09:00:00,1800,0\n" +
			"trip_exact,06:00:00,08:00:00,1800,1\n",
	}

	feedPath := filepath.Join(t.TempDir(), "frequencies.zip")
	feedFile, err := os.Create(feedPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, feedFile.Close()) }()

	archive := zip.NewWriter(feedFile)
	for name, content := range files {
		entry, err := archive.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())

	return feedPath
}

// directionSchedulesByHeadsign indexes a response's direction groups by their headsign,
// across every route, so assertions don't depend on route or direction ordering.
func directionSchedulesByHeadsign(t testing.TB, entry map[string]any) map[string]map[string]any {
	t.Helper()

	byHeadsign := make(map[string]map[string]any)
	routeSchedules, ok := entry["stopRouteSchedules"].([]any)
	require.True(t, ok, "stopRouteSchedules should be an array")

	for _, routeSchedule := range routeSchedules {
		directionSchedules, ok := routeSchedule.(map[string]any)["stopRouteDirectionSchedules"].([]any)
		require.True(t, ok, "stopRouteDirectionSchedules should be an array")

		for _, directionSchedule := range directionSchedules {
			group, ok := directionSchedule.(map[string]any)
			require.True(t, ok)
			headsign, _ := group["tripHeadsign"].(string)
			byHeadsign[headsign] = group
		}
	}

	return byHeadsign
}

func TestScheduleForStopHandlerFrequencies(t *testing.T) {
	api := createTestApiWithFeed(t, writeFrequencyFeed(t))

	// NOTE: Hardcoded date is a Thursday inside the fixture's service period.
	endpoint := "/api/where/schedule-for-stop/1_stop_1.json?key=TEST&date=2025-06-12"
	resp, model := serveApiAndRetrieveEndpoint(t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]any)
	require.True(t, ok)

	groups := directionSchedulesByHeadsign(t, entry)

	t.Run("headway-based trips are reported as frequencies", func(t *testing.T) {
		outbound, ok := groups["Downtown via Freq"]
		require.True(t, ok, "expected a direction group for the outbound frequency trip")

		frequencies, ok := outbound["scheduleFrequencies"].([]any)
		require.True(t, ok)
		require.Len(t, frequencies, 2, "one entry per frequency window")
		assert.Empty(t, outbound["scheduleStopTimes"], "a frequency trip contributes no fixed stop times")

		morning := frequencies[0].(map[string]any)
		evening := frequencies[1].(map[string]any)
		assert.Less(t, morning["startTime"], evening["startTime"], "windows must be sorted by start time")
		assert.Equal(t, float64(600), morning["headway"])
		assert.Equal(t, float64(900), evening["headway"])
		assert.Equal(t, "1_trip_freq_out", morning["tripId"])
		assert.Equal(t, "1_service_1", morning["serviceId"])

		// The stop is the first in the trip's block, so there is no inbound arrival.
		assert.Equal(t, false, morning["arrivalEnabled"])
		assert.Equal(t, true, morning["departureEnabled"])

		serviceDate, ok := morning["serviceDate"].(float64)
		require.True(t, ok)
		assert.Equal(t, serviceDate, entry["date"], "serviceDate is the queried service day")
	})

	t.Run("both kinds of service coexist in one direction group", func(t *testing.T) {
		mixed, ok := groups["Express"]
		require.True(t, ok, "expected a direction group for route_2")

		frequencies, ok := mixed["scheduleFrequencies"].([]any)
		require.True(t, ok)
		require.Len(t, frequencies, 1)
		assert.Equal(t, "1_trip_freq_mixed", frequencies[0].(map[string]any)["tripId"])

		stopTimes, ok := mixed["scheduleStopTimes"].([]any)
		require.True(t, ok)
		require.Len(t, stopTimes, 1, "the fixed-schedule trip stays in stop times")
		assert.Equal(t, "1_trip_fixed", stopTimes[0].(map[string]any)["tripId"])
	})

	t.Run("exact_times trips expand into individual stop times", func(t *testing.T) {
		exact, ok := groups["Exact Express"]
		require.True(t, ok, "expected a direction group for the exact_times trip")

		assert.Empty(t, exact["scheduleFrequencies"], "exact_times service is not a headway window")

		stopTimes, ok := exact["scheduleStopTimes"].([]any)
		require.True(t, ok)
		// 06:00-08:00 every 30 minutes, and this stop is five minutes into the trip.
		require.Len(t, stopTimes, 4)

		loc, err := time.LoadLocation("America/Los_Angeles")
		require.NoError(t, err)
		serviceDay := time.Date(2025, 6, 12, 0, 0, 0, 0, loc)

		for i, stopTime := range stopTimes {
			run, ok := stopTime.(map[string]any)
			require.True(t, ok)

			expected := serviceDay.Add(time.Duration(i)*30*time.Minute + 6*time.Hour + 5*time.Minute)
			assert.Equal(t, float64(expected.UnixMilli()), run["departureTime"])
			assert.Equal(t, run["departureTime"], run["arrivalTime"])
			assert.Equal(t, "1_trip_exact", run["tripId"])
		}
	})

	t.Run("the opposite direction keeps its own frequencies", func(t *testing.T) {
		inbound, ok := groups["Uptown via Freq"]
		require.True(t, ok, "expected a direction group for the inbound frequency trip")

		frequencies, ok := inbound["scheduleFrequencies"].([]any)
		require.True(t, ok)
		require.Len(t, frequencies, 1)
		assert.Equal(t, float64(720), frequencies[0].(map[string]any)["headway"])

		// The stop is the last in the trip's block, so there is no onward departure.
		assert.Equal(t, false, frequencies[0].(map[string]any)["departureEnabled"])
	})
}
