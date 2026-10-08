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
	api := &RestAPI{Clock: clock.NewMockClock(frequencyFixtureClock)}
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
