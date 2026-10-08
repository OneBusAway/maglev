package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// arrivalsForLocationCenter is Stop4062's position. A 2.5 km radius around it
// covers a cluster of RABA stops with scheduled service at arrivalsTestClock.
var arrivalsForLocationCenter = url.Values{
	"lat":    {"40.539367"},
	"lon":    {"-122.34952"},
	"radius": {"2500"},
}

func arrivalsForLocationURL(params ...url.Values) string {
	q := url.Values{"key": {"TEST"}, "minutesBefore": {"60"}, "minutesAfter": {"240"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/arrivals-and-departures-for-location.json?" + q.Encode()
}

// callArrivalsForLocation issues a request centred on arrivalsForLocationCenter
// with the given overrides applied on top.
func callArrivalsForLocation(t testing.TB, api *RestAPI, overrides ...url.Values) (*http.Response, ArrivalsAndDeparturesForLocationResponse) {
	t.Helper()
	params := append([]url.Values{arrivalsForLocationCenter}, overrides...)
	return callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api, arrivalsForLocationURL(params...))
}

func TestArrivalsAndDeparturesForLocationRequiresValidAPIKey(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=invalid&lat=40.5&lon=-122.3")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
}

func TestArrivalsAndDeparturesForLocationValidation(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	tests := []struct {
		name       string
		query      url.Values
		wantFields []string
	}{
		{"missing lat and lon", url.Values{}, []string{"lat", "lon"}},
		{"missing lon", url.Values{"lat": {"40.539367"}}, []string{"lon"}},
		{"missing lat", url.Values{"lon": {"-122.34952"}}, []string{"lat"}},
		{"invalid latitude", url.Values{"lat": {"99"}, "lon": {"-122.34952"}}, []string{"lat"}},
		{"invalid longitude", url.Values{"lat": {"40.539367"}, "lon": {"-999"}}, []string{"lon"}},
		{"non-numeric time", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "time": {"soon"}}, []string{"time"}},
		{"zero maxCount", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "maxCount": {"0"}}, []string{"maxCount"}},
		{"negative minutesBefore", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "minutesBefore": {"-5"}}, []string{"minutesBefore"}},
		{"non-numeric routeType", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "routeType": {"bus"}}, []string{"routeType"}},
		{"non-boolean emptyReturnsNotFound", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "emptyReturnsNotFound": {"maybe"}}, []string{"emptyReturnsNotFound"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := url.Values{"key": {"TEST"}}
			maps.Copy(q, tt.query)
			resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
				"/api/where/arrivals-and-departures-for-location.json?"+q.Encode())

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			for _, field := range tt.wantFields {
				assert.Contains(t, model.Data.FieldErrors, field)
			}
		})
	}
}

func TestArrivalsAndDeparturesForLocationEndToEnd(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	resp, model := callArrivalsForLocation(t, api)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, models.APIVersion, model.Version)

	entry := model.Data.Entry
	require.NotEmpty(t, entry.StopIDs, "the 2.5km RABA cluster should contain stops")
	require.NotEmpty(t, entry.ArrivalsAndDepartures, "fixture should produce arrivals in the test window")

	// stopIds must be deduplicated. The Java server emits every matched stop
	// twice; we deliberately do not reproduce that.
	seen := make(map[string]bool, len(entry.StopIDs))
	for _, id := range entry.StopIDs {
		assert.False(t, seen[id], "stopIds must not repeat %s", id)
		seen[id] = true
	}

	// Every stop the entry names must resolve in references.
	referencedStops := make(map[string]bool, len(model.Data.References.Stops))
	for _, s := range model.Data.References.Stops {
		referencedStops[s.ID] = true
	}
	for _, id := range entry.StopIDs {
		assert.True(t, referencedStops[id], "stopIds entry %s must resolve in references.stops", id)
	}
	for _, n := range entry.NearbyStopIDs {
		assert.True(t, referencedStops[n.StopID], "nearby stop %s must resolve in references.stops", n.StopID)
	}

	for i, a := range entry.ArrivalsAndDepartures {
		assert.NotEmpty(t, a.StopID, "arrival[%d].StopID", i)
		assert.NotEmpty(t, a.RouteID, "arrival[%d].RouteID", i)
		assert.NotEmpty(t, a.TripID, "arrival[%d].TripID", i)
		assert.True(t, seen[a.StopID], "arrival[%d] is at %s, which must appear in stopIds", i, a.StopID)
	}

	require.NotEmpty(t, model.Data.References.Agencies)
	require.NotEmpty(t, model.Data.References.Routes)
	require.NotEmpty(t, model.Data.References.Trips)
}

func TestArrivalsAndDeparturesForLocationSortsArrivalsByTime(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api)
	arrivals := model.Data.Entry.ArrivalsAndDepartures
	require.Greater(t, len(arrivals), 1, "need at least two arrivals to check ordering")

	effective := func(a models.ArrivalAndDeparture) int64 {
		if a.PredictedArrivalTime.UnixMilli() > 0 {
			return a.PredictedArrivalTime.UnixMilli()
		}
		return a.ScheduledArrivalTime.UnixMilli()
	}
	for i := 1; i < len(arrivals); i++ {
		assert.LessOrEqual(t, effective(arrivals[i-1]), effective(arrivals[i]),
			"arrivals must be ordered by effective arrival time")
	}
}

func TestArrivalsAndDeparturesForLocationNearbyStopsSortedByDistance(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api)
	nearby := model.Data.Entry.NearbyStopIDs
	require.NotEmpty(t, nearby, "the RABA cluster should have stops within 100m of each other")

	require.True(t, sort.SliceIsSorted(nearby, func(i, j int) bool {
		return nearby[i].DistanceFromQuery < nearby[j].DistanceFromQuery
	}), "nearbyStopIds must be ordered nearest first")

	for _, n := range nearby {
		assert.NotEmpty(t, n.StopID)
		// Zero is legitimate: the stop sitting exactly on the query centre is
		// nearby to its neighbours and so appears in the union.
		assert.GreaterOrEqual(t, n.DistanceFromQuery, 0.0)
	}
}

func TestArrivalsAndDeparturesForLocationMaxCount(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, full := callArrivalsForLocation(t, api)
	require.Greater(t, len(full.Data.Entry.StopIDs), 1, "need several stops to observe truncation")

	_, limited := callArrivalsForLocation(t, api, url.Values{"maxCount": {"1"}})

	entry := limited.Data.Entry
	assert.True(t, entry.LimitExceeded, "truncating any list must set limitExceeded")
	assert.LessOrEqual(t, len(entry.StopIDs), 1)
	assert.LessOrEqual(t, len(entry.ArrivalsAndDepartures), 1)
	assert.LessOrEqual(t, len(entry.NearbyStopIDs), 1)

	// References must cover only the retained entry results, not the stops and
	// arrivals maxCount trimmed away.
	retainedTrips := make(map[string]bool, len(entry.ArrivalsAndDepartures))
	retainedRoutes := make(map[string]bool, len(entry.ArrivalsAndDepartures))
	for _, a := range entry.ArrivalsAndDepartures {
		retainedTrips[a.TripID] = true
		retainedRoutes[a.RouteID] = true
	}
	referencedTrips := make(map[string]bool, len(limited.Data.References.Trips))
	for _, tr := range limited.Data.References.Trips {
		assert.True(t, retainedTrips[tr.ID], "references.trips %s is not in the retained arrivals", tr.ID)
		referencedTrips[tr.ID] = true
	}
	for tripID := range retainedTrips {
		assert.True(t, referencedTrips[tripID], "retained arrival trip %s must resolve in references.trips", tripID)
	}
	referencedRoutes := make(map[string]bool, len(limited.Data.References.Routes))
	for _, r := range limited.Data.References.Routes {
		referencedRoutes[r.ID] = true
	}
	for routeID := range retainedRoutes {
		assert.True(t, referencedRoutes[routeID], "retained arrival route %s must resolve in references.routes", routeID)
	}
	retainedStops := make(map[string]bool, len(entry.StopIDs)+len(entry.NearbyStopIDs))
	for _, id := range entry.StopIDs {
		retainedStops[id] = true
	}
	for _, n := range entry.NearbyStopIDs {
		retainedStops[n.StopID] = true
	}
	referencedStops := make(map[string]bool, len(limited.Data.References.Stops))
	for _, s := range limited.Data.References.Stops {
		assert.True(t, retainedStops[s.ID], "references.stops %s is not in the retained entry", s.ID)
		referencedStops[s.ID] = true
	}
	for stopID := range retainedStops {
		assert.True(t, referencedStops[stopID], "retained stop %s must resolve in references.stops", stopID)
	}
}

// maxCount above the endpoint ceiling clamps rather than erroring, matching the
// 1000 maximum the OpenAPI spec documents for this endpoint (the shared
// utils.ParseMaxCount ceiling of 250 is too low here).
func TestArrivalsAndDeparturesForLocationMaxCountClampsAboveCeiling(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	resp, model := callArrivalsForLocation(t, api, url.Values{"maxCount": {"5000"}})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.FieldErrors)
	assert.False(t, model.Data.Entry.LimitExceeded)
}

// routeType filters the arrivals but deliberately not the stop search, matching
// both the Java implementation and the deployed API.
func TestArrivalsAndDeparturesForLocationRouteTypeFiltersArrivalsNotStops(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, unfiltered := callArrivalsForLocation(t, api)
	require.NotEmpty(t, unfiltered.Data.Entry.ArrivalsAndDepartures)

	// 99 is not a GTFS route type, so nothing survives the arrivals filter.
	_, filtered := callArrivalsForLocation(t, api, url.Values{"routeType": {"99"}})

	assert.Empty(t, filtered.Data.Entry.ArrivalsAndDepartures,
		"an unmatched routeType must filter out every arrival")
	assert.Equal(t, unfiltered.Data.Entry.StopIDs, filtered.Data.Entry.StopIDs,
		"routeType must not change which stops are searched")
	assert.Empty(t, filtered.Data.Entry.NearbyStopIDs,
		"nearby stops serving no route of the requested type are pruned")

	// RABA is a bus agency, so routeType=3 keeps the arrivals.
	_, buses := callArrivalsForLocation(t, api, url.Values{"routeType": {"3"}})
	assert.NotEmpty(t, buses.Data.Entry.ArrivalsAndDepartures)
}

func TestArrivalsAndDeparturesForLocationEmptyArea(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	// Mid-Pacific: valid coordinates, no stops.
	emptyArea := url.Values{"lat": {"0"}, "lon": {"-140"}, "radius": {"1000"}}

	t.Run("returns an empty entry by default", func(t *testing.T) {
		resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
			arrivalsForLocationURL(emptyArea))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		entry := model.Data.Entry
		assert.Empty(t, entry.StopIDs)
		assert.Empty(t, entry.ArrivalsAndDepartures)
		assert.Empty(t, entry.NearbyStopIDs)
		assert.False(t, entry.LimitExceeded)
	})

	t.Run("returns 404 when emptyReturnsNotFound is set", func(t *testing.T) {
		q := url.Values{"emptyReturnsNotFound": {"true"}}
		maps.Copy(q, emptyArea)
		resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
			arrivalsForLocationURL(q))

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, http.StatusNotFound, model.Code)
	})
}

// Only lat and lon are given, so the search falls back to the shared 600m
// default radius rather than the zero-area box the Java server builds.
func TestArrivalsAndDeparturesForLocationDefaultsToSearchRadius(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		arrivalsForLocationURL(url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.Entry.StopIDs, "lat/lon alone must still search a default radius")
}

func TestArrivalsAndDeparturesForLocationLatSpanLonSpan(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		arrivalsForLocationURL(url.Values{
			"lat":     {"40.539367"},
			"lon":     {"-122.34952"},
			"latSpan": {"0.05"},
			"lonSpan": {"0.05"},
		}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.Entry.StopIDs)
}

func TestArrivalsAndDeparturesForLocationExcludesReferences(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api, url.Values{"includeReferences": {"false"}})

	assert.NotEmpty(t, model.Data.Entry.StopIDs, "the entry is still populated")
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Trips)
	assert.Empty(t, model.Data.References.Agencies)
}

// The stop-level agency must be used to namespace IDs, not a single global one.
func TestArrivalsAndDeparturesForLocationCombinedIDs(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api)

	require.NotEmpty(t, model.Data.Entry.StopIDs)
	for _, id := range model.Data.Entry.StopIDs {
		agencyID, _, err := utils.ExtractAgencyIDAndCodeID(id)
		require.NoError(t, err, "stopIds must be combined {agency}_{code} IDs")
		assert.Equal(t, testdata.Raba.ID, agencyID)
	}
}

// radius takes precedence over latSpan/lonSpan: adding spans to a radius
// request must not change the searched stops or their arrivals.
func TestArrivalsAndDeparturesForLocationRadiusTakesPrecedenceOverSpans(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, radiusOnly := callArrivalsForLocation(t, api, url.Values{"radius": {"2500"}})
	_, radiusPlusSpans := callArrivalsForLocation(t, api, url.Values{
		"radius":  {"2500"},
		"latSpan": {"0.05"},
		"lonSpan": {"0.05"},
	})

	require.NotEmpty(t, radiusOnly.Data.Entry.StopIDs)
	assert.Equal(t, radiusOnly.Data.Entry.StopIDs, radiusPlusSpans.Data.Entry.StopIDs,
		"radius must take precedence over latSpan/lonSpan")
	assert.Len(t, radiusPlusSpans.Data.Entry.ArrivalsAndDepartures,
		len(radiusOnly.Data.Entry.ArrivalsAndDepartures),
		"arrivals must be identical regardless of span parameters")
}

// The RABA fixture has 375 stops, so no integration request can produce the
// 1000+ candidates needed to exercise the endpoint ceiling. Cover the clamp at
// the unit level instead: every list is capped and limitExceeded is set.
func TestTruncateLocationListsCapsAtEndpointCeiling(t *testing.T) {
	const candidates = models.MaxCountForArrivalsForLocation + 5

	arrivals := make([]models.ArrivalAndDeparture, candidates)
	stopIDs := make([]string, candidates)
	nearby := make([]models.StopWithDistance, candidates)
	for i := range candidates {
		stopIDs[i] = "25_" + strconv.Itoa(i)
		nearby[i] = models.StopWithDistance{StopID: "25_" + strconv.Itoa(i)}
	}

	lists := truncateLocationLists(locationLists{
		stopIDs:  stopIDs,
		arrivals: arrivals,
		nearby:   nearby,
	}, models.MaxCountForArrivalsForLocation)

	assert.Len(t, lists.stopIDs, models.MaxCountForArrivalsForLocation)
	assert.Len(t, lists.arrivals, models.MaxCountForArrivalsForLocation)
	assert.Len(t, lists.nearby, models.MaxCountForArrivalsForLocation)
	assert.True(t, lists.limitExceeded, "truncating any list must set limitExceeded")
}

func TestParseArrivalsForLocationMaxCountClampsToCeiling(t *testing.T) {
	var collected map[string][]string
	addError := func(field, msg string) {
		if collected == nil {
			collected = make(map[string][]string)
		}
		collected[field] = append(collected[field], msg)
	}

	assert.Equal(t, models.MaxCountForArrivalsForLocation,
		parseArrivalsForLocationMaxCount(url.Values{"maxCount": {"5000"}}, addError))
	assert.Empty(t, collected, "clamping above the ceiling must not error")
}

// minutes=153722868 would overflow time.Duration(minutes)*time.Minute into a
// negative duration; the cap must apply before the conversion.
func TestParseMinutesValueCapsBeforeDurationOverflow(t *testing.T) {
	var collected map[string][]string
	addError := func(field, msg string) {
		if collected == nil {
			collected = make(map[string][]string)
		}
		collected[field] = append(collected[field], msg)
	}

	assert.Equal(t, maxArrivalWindow,
		parseMinutesValue(url.Values{"minutesAfter": {"153722868"}}, "minutesAfter", 35*time.Minute, maxArrivalWindow, addError))
	assert.Empty(t, collected)

	assert.Equal(t, 5*time.Minute,
		parseMinutesValue(url.Values{"minutesBefore": {"-5"}}, "minutesBefore", 5*time.Minute, maxArrivalWindow, addError))
	assert.Contains(t, collected, "minutesBefore")
}
