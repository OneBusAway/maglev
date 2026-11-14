package restapi

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Exercise both the single-stop and batched location pipelines with the same
// mixed scheduled/headway/exact-times feed. The radius contains only stop A.
func frequencyWindowArrivals(t *testing.T, api *RestAPI, endpoint string, params url.Values) []models.ArrivalAndDeparture {
	t.Helper()
	if endpoint == "stop" {
		resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
			arrivalsAndDeparturesURL(utils.FormCombinedID(freqAgencyID, freqStopAID), params))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		if len(model.Data.Entry.ArrivalsAndDepartures) == 0 {
			assert.Empty(t, model.Data.Entry.NearbyStopIDs)
			assert.Empty(t, model.Data.Entry.SituationIDs)
			assert.Empty(t, model.Data.References.Trips)
			// With no arrivals, the requested stop and its routes are still referenced (as Java does).
			assert.Contains(t, referencedStopCombinedIDs(model.Data.References.Stops), utils.FormCombinedID(freqAgencyID, freqStopAID))
			assert.NotEmpty(t, model.Data.References.Routes)
		}
		return model.Data.Entry.ArrivalsAndDepartures
	}
	q := url.Values{"key": {"TEST"}, "lat": {"37.7749"}, "lon": {"-122.4194"}, "radius": {"100"}}
	maps.Copy(q, params)
	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		"/api/where/arrivals-and-departures-for-location.json?"+q.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return model.Data.Entry.ArrivalsAndDepartures
}

func assertFrequencyWindowTrips(t *testing.T, arrivals []models.ArrivalAndDeparture, want []string) {
	t.Helper()
	got := make([]string, 0, len(arrivals))
	for _, arrival := range arrivals {
		_, tripID, err := utils.ExtractAgencyIDAndCodeID(arrival.TripID)
		require.NoError(t, err)
		got = append(got, tripID)
	}
	assert.ElementsMatch(t, want, got)
}

func TestArrivalFrequencyWindows(t *testing.T) {
	api := createTestApiWithFrequencyData(t)
	defer api.Shutdown()

	frequencyTrips := []string{freqTripID, freqExactTripID}
	tests := []struct {
		name      string
		timeOfDay string
		params    url.Values
		want      []string
	}{
		{"default before boundary", "09:02:00", nil, frequencyTrips},
		{"default before excludes regular-window match", "09:03:00", nil, nil},
		{"default after boundary", "05:30:00", nil, frequencyTrips},
		{"default after excludes regular-window match", "05:29:00", nil, nil},
		{"wider before does not widen regular window", "10:00:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesBefore": {"60"}, "frequencyMinutesAfter": {"0"},
		}, frequencyTrips},
		{"wider before retrieves both frequency and normal trips", "09:05:00", url.Values{
			"minutesBefore": {"65"}, "minutesAfter": {"0"}, "frequencyMinutesBefore": {"10"}, "frequencyMinutesAfter": {"0"},
		}, []string{freqTripID, freqExactTripID, freqNormalTripD}},
		{"wider after does not widen regular window", "05:00:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"180"},
		}, frequencyTrips},
		{"narrower before excludes frequency trips", "09:05:00", url.Values{
			"minutesBefore": {"10"}, "frequencyMinutesBefore": {"4"},
		}, nil},
		{"custom before boundary", "09:05:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesBefore": {"5"},
		}, frequencyTrips},
		{"custom after boundary", "05:45:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"15"},
		}, frequencyTrips},
		{"narrower after preserves regular trips", "05:55:00", url.Values{
			"minutesAfter": {"180"}, "frequencyMinutesAfter": {"4"},
		}, []string{freqNormalTripD}},
		{"zero frequency window includes exact instant", "09:00:00", url.Values{
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
		}, frequencyTrips},
		{"zero frequency window excludes nearby instant", "09:00:01", url.Values{
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
		}, nil},
		{"default frequency window independent of zero regular window", "05:30:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"},
		}, frequencyTrips},
	}
	for _, endpoint := range []string{"stop", "location"} {
		for _, tt := range tests {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				queryTime, err := time.Parse("2006-01-02 15:04:05", "2025-06-12 "+tt.timeOfDay)
				require.NoError(t, err)
				params := url.Values{"time": {fmt.Sprint(queryTime.UnixMilli())}}
				maps.Copy(params, tt.params)
				assertFrequencyWindowTrips(t, frequencyWindowArrivals(t, api, endpoint, params), tt.want)
			})
		}
	}
}

func TestArrivalFrequencyWindowPredictions(t *testing.T) {
	for _, endpoint := range []string{"stop", "location"} {
		for _, event := range []string{"arrival", "departure"} {
			t.Run(endpoint+"/"+event, func(t *testing.T) {
				api := createTestApiWithFrequencyData(t)
				defer api.Shutdown()
				delay := 5 * time.Minute
				update := gtfs.StopTimeUpdate{StopID: new(freqStopAID), StopSequence: new(uint32(1))}
				if event == "arrival" {
					update.Arrival = &gtfs.StopTimeEvent{Delay: &delay}
				} else {
					update.Departure = &gtfs.StopTimeEvent{Delay: &delay}
				}
				api.GtfsManager.MockAddTripUpdate(freqTripID, nil, []gtfs.StopTimeUpdate{update})
				params := url.Values{
					"time":                   {fmt.Sprint(time.Date(2025, 6, 12, 6, 5, 0, 0, time.UTC).UnixMilli())},
					"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
				}
				arrivals := frequencyWindowArrivals(t, api, endpoint, params)
				assertFrequencyWindowTrips(t, arrivals, []string{freqTripID})
				require.Len(t, arrivals, 1)
				if event == "arrival" {
					assert.Equal(t, arrivals[0].PredictedArrivalTime.UnixMilli(), arrivals[0].ScheduledArrivalTime.UnixMilli())
				} else {
					assert.Equal(t, arrivals[0].PredictedDepartureTime.UnixMilli(), arrivals[0].ScheduledDepartureTime.UnixMilli())
				}
			})
		}
	}
}

// TestArrivalFrequencyWindowRunningLateCandidate verifies that the padded
// candidate search also reaches a late frequency-based trip: its 06:00 stop
// time lies outside a zero-width window at 06:12, but its prediction does not.
func TestArrivalFrequencyWindowRunningLateCandidate(t *testing.T) {
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			api := createTestApiWithFrequencyData(t)
			defer api.Shutdown()
			delay := 12 * time.Minute
			api.GtfsManager.MockAddTripUpdate(freqTripID, nil, []gtfs.StopTimeUpdate{{
				StopID:       new(freqStopAID),
				StopSequence: new(uint32(1)),
				Arrival:      &gtfs.StopTimeEvent{Delay: &delay},
			}})
			params := url.Values{
				"time":          {fmt.Sprint(time.Date(2025, 6, 12, 6, 12, 0, 0, time.UTC).UnixMilli())},
				"minutesBefore": {"0"}, "minutesAfter": {"0"},
				"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
			}
			assertFrequencyWindowTrips(t, frequencyWindowArrivals(t, api, endpoint, params), []string{freqTripID})
		})
	}
}

func TestArrivalFrequencyWindowAcrossMidnight(t *testing.T) {
	files := frequencyFixtureFiles()
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:00:00", "24:01:00")
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:10:00", "24:11:00")
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:15:00", "24:16:00")
	files["frequencies.txt"] = strings.ReplaceAll(files["frequencies.txt"], "06:00:00,09:00:00", "23:00:00,25:00:00")
	queryTime := time.Date(2025, 6, 13, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "frequency-midnight.zip", files)
	defer api.Shutdown()
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, url.Values{
				"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"15"},
			})
			assertFrequencyWindowTrips(t, arrivals, []string{freqTripID, freqExactTripID})
			for _, arrival := range arrivals {
				assert.Equal(t, time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC).UnixMilli(), arrival.ServiceDate.UnixMilli())
			}
		})
	}
}

func TestArrivalFrequencyWindowScheduledDeparture(t *testing.T) {
	files := frequencyFixtureFiles()
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:00:00,06:00:00", "06:00:00,06:01:00")
	// At end_time (09:00:00), projected departure clamps to 09:00:00 while projected
	// arrival is 08:59:00 due to the 1m dwell, matching [09:00, 09:00] via departure alone.
	queryTime := time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "frequency-departure.zip", files)
	defer api.Shutdown()
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, url.Values{
				"minutesBefore": {"0"}, "minutesAfter": {"0"},
				"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
			})
			assertFrequencyWindowTrips(t, arrivals, []string{freqTripID, freqExactTripID})
			for _, arrival := range arrivals {
				assert.Equal(t, time.Date(2025, 6, 12, 8, 59, 0, 0, time.UTC).UnixMilli(), arrival.ScheduledArrivalTime.UnixMilli())
				assert.Equal(t, time.Date(2025, 6, 12, 9, 0, 0, 0, time.UTC).UnixMilli(), arrival.ScheduledDepartureTime.UnixMilli())
			}
		})
	}
}

func TestParseArrivalFrequencyWindows(t *testing.T) {
	api := &RestAPI{Application: &app.Application{Clock: clock.NewMockClock(frequencyFixtureClock)}}
	tests := []struct {
		name          string
		params        url.Values
		before, after time.Duration
		wantErrors    []string
	}{
		{"defaults", nil, 2 * time.Minute, 30 * time.Minute, nil},
		{"empty uses defaults", url.Values{"frequencyMinutesBefore": {""}, "frequencyMinutesAfter": {""}}, 2 * time.Minute, 30 * time.Minute, nil},
		{"custom", url.Values{"frequencyMinutesBefore": {"10"}, "frequencyMinutesAfter": {"45"}}, 10 * time.Minute, 45 * time.Minute, nil},
		{"zero", url.Values{"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"}}, 0, 0, nil},
		{"capped", url.Values{"frequencyMinutesBefore": {"999999999"}, "frequencyMinutesAfter": {"999999999"}}, maxArrivalWindow, maxArrivalWindow, nil},
		{"invalid", url.Values{"frequencyMinutesBefore": {"abc"}, "frequencyMinutesAfter": {"1.5"}}, 2 * time.Minute, 30 * time.Minute, []string{"frequencyMinutesBefore", "frequencyMinutesAfter"}},
		{"negative", url.Values{"frequencyMinutesBefore": {"-1"}, "frequencyMinutesAfter": {"-2"}}, 2 * time.Minute, 30 * time.Minute, []string{"frequencyMinutesBefore", "frequencyMinutesAfter"}},
		{"integer overflow", url.Values{"frequencyMinutesBefore": {"999999999999999999999"}}, 2 * time.Minute, 30 * time.Minute, []string{"frequencyMinutesBefore"}},
	}
	for _, endpoint := range []string{"stop", "location"} {
		for _, tt := range tests {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				q := url.Values{"lat": {"37.7749"}, "lon": {"-122.4194"}}
				maps.Copy(q, tt.params)
				req := httptest.NewRequest(http.MethodGet, "/test?"+q.Encode(), nil)
				var before, after time.Duration
				var errors map[string][]string
				if endpoint == "stop" {
					params, errs := api.parseArrivalsAndDeparturesParams(req)
					before, after, errors = params.FrequencyBefore, params.FrequencyAfter, errs
				} else {
					params, errs := api.parseArrivalsForLocationParams(req)
					before, after, errors = params.FrequencyBefore, params.FrequencyAfter, errs
				}
				assert.Equal(t, tt.before, before)
				assert.Equal(t, tt.after, after)
				assert.Len(t, errors, len(tt.wantErrors))
				for _, field := range tt.wantErrors {
					assert.Contains(t, errors, field)
				}
			})
		}
	}
}

// Each agency has the same absolute arrivals, expressed in its own timezone.
// Querying either agency prefix must preserve both regular and frequency trips.
func TestArrivalFrequencyWindowsSharedStopTimezones(t *testing.T) {
	const secondAgency = "paris-agency"
	files := frequencyFixtureFiles()
	files["agency.txt"] += secondAgency + ",Paris Agency,http://example.com,Europe/Paris\n"
	for _, name := range []string{"routes.txt", "trips.txt", "stop_times.txt", "frequencies.txt"} {
		_, rows, ok := strings.Cut(files[name], "\n")
		require.True(t, ok)
		rows = strings.NewReplacer(
			freqAgencyID, secondAgency,
			freqRouteID, "paris-route",
			freqTripID, "paris-trip",
			freqExactTripID, "paris-exact-trip",
			freqNormalTripD, "paris-normal-trip",
			"freq-block-", "paris-block-",
		).Replace(rows)
		if name == "stop_times.txt" || name == "frequencies.txt" {
			// Paris is UTC+2 on this service date.
			rows = strings.NewReplacer("06:", "08:", "08:", "10:", "09:", "11:").Replace(rows)
		}
		files[name] += rows
	}
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(frequencyFixtureClock), "frequency-shared-timezones.zip", files)
	defer api.Shutdown()

	frequencyTrips := []string{freqTripID, freqExactTripID, "paris-trip", "paris-exact-trip"}
	regularTrips := []string{freqNormalTripD, "paris-normal-trip"}
	tests := []struct {
		name         string
		hour, second int
		wantFreqHour int
		params       url.Values
		want         []string
	}{
		{"wider frequency after", 5, 0, 6, url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"},
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"60"},
		}, frequencyTrips},
		{"wider frequency before and regular match", 10, 0, 9, url.Values{
			"minutesBefore": {"120"}, "minutesAfter": {"0"},
			"frequencyMinutesBefore": {"60"}, "frequencyMinutesAfter": {"0"},
		}, append(append([]string{}, frequencyTrips...), regularTrips...)},
		{"narrow frequency window preserves regular trips", 6, 1, 0, url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"120"},
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
		}, regularTrips},
	}
	for _, agency := range []string{freqAgencyID, secondAgency} {
		for _, tt := range tests {
			t.Run(agency+"/"+tt.name, func(t *testing.T) {
				queryTime := time.Date(2025, 6, 12, tt.hour, 0, tt.second, 0, time.UTC)
				params := url.Values{"time": {fmt.Sprint(queryTime.UnixMilli())}}
				maps.Copy(params, tt.params)
				resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
					arrivalsAndDeparturesURL(utils.FormCombinedID(agency, freqStopAID), params))
				require.Equal(t, http.StatusOK, resp.StatusCode)
				assertFrequencyWindowTrips(t, model.Data.Entry.ArrivalsAndDepartures, tt.want)
				for _, arrival := range model.Data.Entry.ArrivalsAndDepartures {
					wantHour := tt.wantFreqHour
					if arrival.Frequency == nil {
						wantHour = 8
					}
					assert.Equal(t, time.Date(2025, 6, 12, wantHour, 0, 0, 0, time.UTC).UnixMilli(), arrival.ScheduledArrivalTime.UnixMilli())
				}
			})
		}
	}
}

// TestArrivalWindow_StopTimeSpanningQueryWindow verifies that a scheduled stop time
// spanning the entire query window is excluded by the endpoint filter (matching Java parity),
// but when its predicted arrival falls within the window, the wider SQL candidate query
// ensures the trip is retained.
func TestArrivalWindow_StopTimeSpanningQueryWindow(t *testing.T) {
	files := frequencyFixtureFiles()
	const spanningTripID = "spanning-trip"
	files["trips.txt"] += freqRouteID + "," + freqServiceID + "," + spanningTripID + ",Spanning Trip,0,span-block\n"
	files["stop_times.txt"] += spanningTripID + ",10:00:00,10:50:00," + freqStopAID + ",1\n"
	files["stop_times.txt"] += spanningTripID + ",11:00:00,11:00:00," + freqStopBID + ",2\n"

	queryTime := time.Date(2025, 6, 12, 10, 15, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "spanning-window.zip", files)
	defer api.Shutdown()

	params := url.Values{
		"time":          {fmt.Sprint(queryTime.UnixMilli())},
		"minutesBefore": {"5"},
		"minutesAfter":  {"5"},
	}

	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint+"/scheduled_spanning_excluded", func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, params)
			assertFrequencyWindowTrips(t, arrivals, []string{})
		})
	}

	delay := 12 * time.Minute
	stopID := freqStopAID
	seq := uint32(1)
	api.GtfsManager.MockAddTripUpdate(spanningTripID, nil, []gtfs.StopTimeUpdate{
		{StopID: &stopID, StopSequence: &seq, Arrival: &gtfs.StopTimeEvent{Delay: &delay}},
	})
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint+"/predicted_in_window_included", func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, params)
			assertFrequencyWindowTrips(t, arrivals, []string{spanningTripID})
		})
	}
}

// TestArrivalWindow_RunningLateAndEarlyCandidates verifies that, like Java,
// the candidate search is padded by runningLate before and runningEarly after
// the requested window, so a bus whose scheduled time falls outside the window
// is still returned when its predicted time falls inside it.
func TestArrivalWindow_RunningLateAndEarlyCandidates(t *testing.T) {
	// Query at 10:15 with a ±5 minute window: 10:10–10:20. Padded by
	// runningLate (30m) and runningEarly (10m), the search is 09:40–10:30.
	tests := []struct {
		name         string
		tripID       string
		scheduledAtA string
		delay        *time.Duration
		wantIncluded bool
	}{
		{"late_bus_predicted_in_window", "late-trip", "10:00:00", ptrDuration(12 * time.Minute), true},
		{"early_bus_predicted_in_window", "early-trip", "10:25:00", ptrDuration(-7 * time.Minute), true},
		{"late_bus_without_prediction", "unpredicted-trip", "10:00:00", nil, false},
		{"beyond_running_late_buffer", "very-late-trip", "09:35:00", ptrDuration(40 * time.Minute), false},
		{"beyond_running_early_buffer", "very-early-trip", "10:31:00", ptrDuration(-12 * time.Minute), false},
	}

	files := frequencyFixtureFiles()
	for _, tc := range tests {
		files["trips.txt"] += freqRouteID + "," + freqServiceID + "," + tc.tripID + ",Buffered Trip,0," + tc.tripID + "-block\n"
		files["stop_times.txt"] += tc.tripID + "," + tc.scheduledAtA + "," + tc.scheduledAtA + "," + freqStopAID + ",1\n"
		files["stop_times.txt"] += tc.tripID + ",11:00:00,11:00:00," + freqStopBID + ",2\n"
	}

	queryTime := time.Date(2025, 6, 12, 10, 15, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "running-window.zip", files)
	defer api.Shutdown()

	stopID := freqStopAID
	firstStop := uint32(1)
	for _, tc := range tests {
		if tc.delay == nil {
			continue
		}
		api.GtfsManager.MockAddTripUpdate(tc.tripID, nil, []gtfs.StopTimeUpdate{
			{StopID: &stopID, StopSequence: &firstStop, Arrival: &gtfs.StopTimeEvent{Delay: tc.delay}},
		})
	}
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// The frequency window is pinned too: its 30-minute default would
	// otherwise widen the search past the early bus on its own.
	params := url.Values{
		"time":                   {fmt.Sprint(queryTime.UnixMilli())},
		"minutesBefore":          {"5"},
		"minutesAfter":           {"5"},
		"frequencyMinutesBefore": {"5"},
		"frequencyMinutesAfter":  {"5"},
	}
	for _, endpoint := range []string{"stop", "location"} {
		arrivals := frequencyWindowArrivals(t, api, endpoint, params)
		for _, tc := range tests {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				assert.Equal(t, tc.wantIncluded, arrivalsIncludeTrip(t, arrivals, tc.tripID))
			})
		}
	}
}

func arrivalsIncludeTrip(t *testing.T, arrivals []models.ArrivalAndDeparture, tripID string) bool {
	t.Helper()
	for _, arrival := range arrivals {
		_, id, err := utils.ExtractAgencyIDAndCodeID(arrival.TripID)
		require.NoError(t, err)
		if id == tripID {
			return true
		}
	}
	return false
}

// referencedStopCombinedIDs returns the combined IDs of the stops in a references block.
func referencedStopCombinedIDs(stops []models.Stop) []string {
	ids := make([]string, 0, len(stops))
	for _, stop := range stops {
		ids = append(ids, stop.ID)
	}
	return ids
}

// TestArrivalFrequencyMidInterval verifies that frequency-based trips remain
// surfaced throughout their active frequencies.txt interval after template
// stop_times pass, projecting scheduled times as clamp(queryTime+headway/2,
// startTime, endTime) across both stop and location endpoints.
func TestArrivalFrequencyMidInterval(t *testing.T) {
	api := createTestApiWithFrequencyData(t)
	defer api.Shutdown()

	stopBURL := func(params url.Values) string {
		return arrivalsAndDeparturesURL(utils.FormCombinedID(freqAgencyID, freqStopBID), params)
	}

	tests := []struct {
		name      string
		timeOfDay string
		endpoint  string
		wantTimes map[string]string
	}{
		{
			name:      "early mid-interval stop",
			timeOfDay: "06:40:00",
			endpoint:  "stop",
			wantTimes: map[string]string{
				freqTripID:      "06:45:00",
				freqExactTripID: "06:55:00",
			},
		},
		{
			name:      "early mid-interval location",
			timeOfDay: "06:40:00",
			endpoint:  "location",
			wantTimes: map[string]string{
				freqTripID:      "06:45:00",
				freqExactTripID: "06:55:00",
			},
		},
		{
			name:      "mid-interval with normal trip stop",
			timeOfDay: "07:30:00",
			endpoint:  "stop",
			wantTimes: map[string]string{
				freqTripID:      "07:35:00",
				freqExactTripID: "07:45:00",
				freqNormalTripD: "08:00:00",
			},
		},
		{
			name:      "mid-interval with normal trip location",
			timeOfDay: "07:30:00",
			endpoint:  "location",
			wantTimes: map[string]string{
				freqTripID:      "07:35:00",
				freqExactTripID: "07:45:00",
				freqNormalTripD: "08:00:00",
			},
		},
		{
			name:      "late interval clamps to end_time stop",
			timeOfDay: "08:50:00",
			endpoint:  "stop",
			wantTimes: map[string]string{
				freqTripID:      "08:55:00",
				freqExactTripID: "09:00:00",
			},
		},
		{
			name:      "late interval clamps to end_time location",
			timeOfDay: "08:50:00",
			endpoint:  "location",
			wantTimes: map[string]string{
				freqTripID:      "08:55:00",
				freqExactTripID: "09:00:00",
			},
		},
		{
			name:      "pre-start clamps to start_time",
			timeOfDay: "05:35:00",
			endpoint:  "stop",
			wantTimes: map[string]string{
				freqTripID:      "06:00:00",
				freqExactTripID: "06:00:00",
			},
		},
		{
			name:      "after interval excludes frequency trips",
			timeOfDay: "09:05:00",
			endpoint:  "stop",
			wantTimes: map[string]string{},
		},
		{
			name:      "downstream stop B stays active mid-interval",
			timeOfDay: "07:30:00",
			endpoint:  "stopB",
			wantTimes: map[string]string{
				freqTripID:      "07:35:00",
				freqExactTripID: "07:45:00",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryTime, err := time.Parse("2006-01-02 15:04:05", "2025-06-12 "+tt.timeOfDay)
			require.NoError(t, err)
			params := url.Values{"time": {fmt.Sprint(queryTime.UnixMilli())}}

			var arrivals []models.ArrivalAndDeparture
			if tt.endpoint == "stopB" {
				resp, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, stopBURL(params))
				require.Equal(t, http.StatusOK, resp.StatusCode)
				arrivals = model.Data.Entry.ArrivalsAndDepartures
			} else {
				arrivals = frequencyWindowArrivals(t, api, tt.endpoint, params)
			}

			gotTimes := make(map[string]string, len(arrivals))
			for _, arrival := range arrivals {
				_, id, err := utils.ExtractAgencyIDAndCodeID(arrival.TripID)
				require.NoError(t, err)
				gotTimes[id] = time.UnixMilli(arrival.ScheduledArrivalTime.UnixMilli()).UTC().Format("15:04:05")
				if id != freqNormalTripD {
					require.NotNil(t, arrival.Frequency)
					assert.Equal(t, arrival.ScheduledArrivalTime.UnixMilli(), arrival.ScheduledDepartureTime.UnixMilli())
				} else {
					assert.Nil(t, arrival.Frequency)
				}
			}
			assert.Equal(t, tt.wantTimes, gotTimes)
		})
	}
}

// TestArrivalFrequencyMultipleWindowsAndDwell verifies selecting the active
// frequency window among multiple windows on a single trip and preserving stop
// dwell time between projected arrival and departure.
func TestArrivalFrequencyMultipleWindowsAndDwell(t *testing.T) {
	files := frequencyFixtureFiles()
	files["trips.txt"] = "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
		freqRouteID + "," + freqServiceID + "," + freqTripID + ",Downtown via Freq,0,freq-block-1\n"
	files["stop_times.txt"] = "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		freqTripID + ",06:00:00,06:02:00," + freqStopAID + ",1\n" +
		freqTripID + ",06:10:00,06:10:00," + freqStopBID + ",2\n"
	files["frequencies.txt"] = "trip_id,start_time,end_time,headway_secs,exact_times\n" +
		freqTripID + ",06:00:00,09:00:00,600,0\n" +
		freqTripID + ",16:00:00,19:00:00,1200,0\n"

	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(frequencyFixtureClock), "frequency-multi-window.zip", files)
	defer api.Shutdown()

	tests := []struct {
		name        string
		timeOfDay   string
		wantCount   int
		wantArr     string
		wantDep     string
		wantHeadway int64
	}{
		{
			name:        "morning window selected with dwell preserved",
			timeOfDay:   "07:30:00",
			wantCount:   1,
			wantArr:     "07:33:00",
			wantDep:     "07:35:00",
			wantHeadway: 600,
		},
		{
			name:      "midday gap returns no arrivals",
			timeOfDay: "12:00:00",
			wantCount: 0,
		},
		{
			name:        "evening window selected with dwell preserved",
			timeOfDay:   "17:30:00",
			wantCount:   1,
			wantArr:     "17:38:00",
			wantDep:     "17:40:00",
			wantHeadway: 1200,
		},
	}

	for _, endpoint := range []string{"stop", "location"} {
		for _, tt := range tests {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				queryTime, err := time.Parse("2006-01-02 15:04:05", "2025-06-12 "+tt.timeOfDay)
				require.NoError(t, err)
				arrivals := frequencyWindowArrivals(t, api, endpoint, url.Values{
					"time": {fmt.Sprint(queryTime.UnixMilli())},
				})
				require.Len(t, arrivals, tt.wantCount)
				if tt.wantCount == 0 {
					return
				}
				a := arrivals[0]
				require.NotNil(t, a.Frequency)
				assert.Equal(t, time.Duration(tt.wantHeadway)*time.Second, a.Frequency.Headway.Duration)
				assert.Equal(t, tt.wantArr, time.UnixMilli(a.ScheduledArrivalTime.UnixMilli()).UTC().Format("15:04:05"))
				assert.Equal(t, tt.wantDep, time.UnixMilli(a.ScheduledDepartureTime.UnixMilli()).UTC().Format("15:04:05"))
			})
		}
	}
}
