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
			assert.Empty(t, model.Data.References.Stops)
			assert.Empty(t, model.Data.References.Routes)
			assert.Empty(t, model.Data.References.Trips)
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
		{"default before boundary", "06:02:00", nil, frequencyTrips},
		{"default before excludes regular-window match", "06:03:00", nil, nil},
		{"default after boundary", "05:30:00", nil, frequencyTrips},
		{"default after excludes regular-window match", "05:29:00", nil, nil},
		{"wider before retrieves frequency trips", "08:00:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesBefore": {"120"}, "frequencyMinutesAfter": {"0"},
		}, []string{freqTripID, freqExactTripID, freqNormalTripD}},
		{"wider after does not widen regular window", "05:00:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"180"},
		}, frequencyTrips},
		{"narrower before excludes frequency trips", "06:05:00", url.Values{
			"minutesBefore": {"180"}, "frequencyMinutesBefore": {"4"},
		}, nil},
		{"custom before boundary", "06:05:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesBefore": {"5"},
		}, frequencyTrips},
		{"custom after boundary", "05:55:00", url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"5"},
		}, frequencyTrips},
		{"narrower after preserves regular trips", "05:55:00", url.Values{
			"minutesAfter": {"180"}, "frequencyMinutesAfter": {"4"},
		}, []string{freqNormalTripD}},
		{"zero frequency window includes exact instant", "06:00:00", url.Values{
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
		}, frequencyTrips},
		{"zero frequency window excludes nearby instant", "06:00:01", url.Values{
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
				assertFrequencyWindowTrips(t, frequencyWindowArrivals(t, api, endpoint, params), []string{freqTripID})
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
				"minutesBefore": {"0"}, "minutesAfter": {"0"}, "frequencyMinutesAfter": {"1"},
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
	queryTime := time.Date(2025, 6, 12, 6, 1, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "frequency-departure.zip", files)
	defer api.Shutdown()
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			assertFrequencyWindowTrips(t, frequencyWindowArrivals(t, api, endpoint, url.Values{
				"minutesBefore": {"0"}, "minutesAfter": {"0"},
				"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"0"},
			}), []string{freqTripID, freqExactTripID})
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
		{"capped", url.Values{"frequencyMinutesBefore": {"999999999"}, "frequencyMinutesAfter": {"999999999"}}, time.Duration(maxArrivalWindowMinutes) * time.Minute, time.Duration(maxArrivalWindowMinutes) * time.Minute, nil},
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
		params       url.Values
		want         []string
	}{
		{"wider frequency after", 5, 0, url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"},
			"frequencyMinutesBefore": {"0"}, "frequencyMinutesAfter": {"60"},
		}, frequencyTrips},
		{"wider frequency before and regular match", 8, 0, url.Values{
			"minutesBefore": {"0"}, "minutesAfter": {"0"},
			"frequencyMinutesBefore": {"120"}, "frequencyMinutesAfter": {"0"},
		}, append(append([]string{}, frequencyTrips...), regularTrips...)},
		{"narrow frequency window preserves regular trips", 6, 1, url.Values{
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
					wantHour := 6
					if arrival.Frequency == nil {
						wantHour = 8
					}
					assert.Equal(t, time.Date(2025, 6, 12, wantHour, 0, 0, 0, time.UTC).UnixMilli(), arrival.ScheduledArrivalTime.UnixMilli())
				}
			})
		}
	}
}

// TestArrivalFrequencyWindowReachesServiceDayTwo verifies the day scan is driven
// by the window the day queries use, not the ordinary minutes: a frequency window
// reaching two days out must surface the third service day's frequency arrivals,
// which the old after/24h+1 range cut off.
func TestArrivalFrequencyWindowReachesServiceDayTwo(t *testing.T) {
	files := frequencyFixtureFiles()
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:00:00", "24:00:00")
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:10:00", "24:10:00")
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "06:15:00", "24:15:00")
	files["frequencies.txt"] = strings.ReplaceAll(files["frequencies.txt"], "06:00:00,09:00:00", "23:00:00,26:00:00")
	queryTime := time.Date(2025, 6, 12, 23, 0, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "frequency-day-two.zip", files)
	defer api.Shutdown()

	// The frequency block runs 23:00-26:00 on every service day. Querying at
	// 23:00 with frequencyMinutesAfter=2960 reaches 49h20m out: inside the
	// third service day's (Jun 14) block, whose 24:00 row lands 49h after the
	// query. The fixed range (after/24h+1 while after=0) never reached it.
	params := url.Values{
		"minutesBefore":          {"0"},
		"minutesAfter":           {"0"},
		"frequencyMinutesBefore": {"0"},
		"frequencyMinutesAfter":  {"2960"},
	}
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, params)
			serviceDays := make(map[string]int)
			for _, arrival := range arrivals {
				serviceDays[arrival.ServiceDate.UTC().Format("2006-01-02")]++
			}
			// The frequency block runs 23:00-26:00 every service day, so its
			// departures land in the window on day 0 (Jun 12), day 1 (Jun 13)
			// and day 2 (Jun 14): the block starts 48h after the query.
			assert.Positive(t, serviceDays["2025-06-12"], "day-0 frequency arrivals expected")
			assert.Positive(t, serviceDays["2025-06-13"], "day-one frequency arrivals expected")
			// The third service day (Jun 14) departs 48h after the query: the
			// fixed range (after/24h+1 while after=0) never scanned it.
			assert.Positive(t, serviceDays["2025-06-14"], "day-two frequency arrivals expected but never scanned")
			beyondTwoDays := 0
			for _, arrival := range arrivals {
				if arrival.ScheduledArrivalTime.Time.Sub(queryTime) >= 48*time.Hour {
					beyondTwoDays++
				}
			}
			assert.Positive(t, beyondTwoDays, "expected an arrival at least 48h out")
		})
	}
}

// TestArrivalWindowLookbackSpansMidnight verifies the lookback side of the day
// scan: a trip whose 24:30 stop time departs two days and 22 hours behind the
// query must surface when minutesBefore extends the window just past it, which
// needs the service day of the window start (minus one spillover day) on the
// lookback side, not the fixed before/24h+1 estimate.
func TestArrivalWindowLookbackSpansMidnight(t *testing.T) {
	files := frequencyFixtureFiles()
	const lookbackTripID = "lookback-trip"
	files["trips.txt"] += freqRouteID + "," + freqServiceID + "," + lookbackTripID + ",Lookback Trip,0,lookback-block\n"
	files["stop_times.txt"] += lookbackTripID + ",24:30:00,24:30:00," + freqStopAID + ",1\n"

	queryTime := time.Date(2025, 6, 13, 0, 10, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "lookback-midnight.zip", files)
	defer api.Shutdown()

	// minutesBefore=2865 reaches 47h45m back: Jun 11 00:25, just before the
	// 24:30 departure on the Jun 10 service date (Jun 11 00:30 absolute).
	// int(before/24h)+1 still misses that service day entirely.
	params := url.Values{"minutesBefore": {"2865"}, "minutesAfter": {"0"}}
	for _, endpoint := range []string{"stop", "location"} {
		t.Run(endpoint, func(t *testing.T) {
			arrivals := frequencyWindowArrivals(t, api, endpoint, params)
			// The window covers Jun 11 00:25 to Jun 13 00:10. Midnight-spanning
			// rows can match two service days ("24:30" departs Jun 11 00:30 on
			// the Jun 10 date and Jun 12 00:30 on the Jun 11 date), and the
			// scheduled 08:00 runs of Jun 11 and Jun 12 are also inside. All
			// four must be present; the fixed lookback only found the day -2
			// base and dropped the Jun 10 instance.
			assertFrequencyWindowTrips(t, arrivals, []string{
				lookbackTripID, lookbackTripID, freqNormalTripD, freqNormalTripD,
			})
		})
	}
}

// TestArrivalWindow_StopTimeSpanningQueryWindow verifies that a scheduled stop time
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

// TestArrivalWindow_RunningEarlyCrossesMidnight verifies that the service-day
// scan covers the padded candidate window: a trip scheduled on the next
// service date whose predicted time falls before midnight is still returned.
func TestArrivalWindow_RunningEarlyCrossesMidnight(t *testing.T) {
	// Query at 23:50 with a ±5 minute window: 23:45–23:55. Padded by
	// runningLate (30m) and runningEarly (10m), the day queries search
	// 23:15–00:05, which reaches the next service date. A trip scheduled
	// 00:02 running 9 minutes early is predicted at 23:53, inside the
	// requested window. Unpadded, the window ends at 23:55 and maxDay is 0,
	// so service day +1 is never scanned and the arrival is dropped.
	files := frequencyFixtureFiles()
	files["trips.txt"] += freqRouteID + "," + freqServiceID + ",early-midnight-trip,Buffered Trip,0,early-midnight-block\n"
	files["stop_times.txt"] += "early-midnight-trip,00:02:00,00:02:00," + freqStopAID + ",1\n"
	files["stop_times.txt"] += "early-midnight-trip,11:00:00,11:00:00," + freqStopBID + ",2\n"

	queryTime := time.Date(2025, 6, 12, 23, 50, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(queryTime), "early-midnight-window.zip", files)
	defer api.Shutdown()

	stopID := freqStopAID
	firstStop := uint32(1)
	api.GtfsManager.MockAddTripUpdate("early-midnight-trip", nil, []gtfs.StopTimeUpdate{
		{StopID: &stopID, StopSequence: &firstStop, Arrival: &gtfs.StopTimeEvent{Delay: ptrDuration(-9 * time.Minute)}},
	})
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	params := url.Values{
		"time":                   {fmt.Sprint(queryTime.UnixMilli())},
		"minutesBefore":          {"5"},
		"minutesAfter":           {"5"},
		"frequencyMinutesBefore": {"5"},
		"frequencyMinutesAfter":  {"5"},
	}
	for _, endpoint := range []string{"stop", "location"} {
		arrivals := frequencyWindowArrivals(t, api, endpoint, params)
		assert.True(t, arrivalsIncludeTrip(t, arrivals, "early-midnight-trip"),
			"arrival predicted at 23:53 (scheduled 00:02 on the next service day, 9 min early) should be returned")
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
