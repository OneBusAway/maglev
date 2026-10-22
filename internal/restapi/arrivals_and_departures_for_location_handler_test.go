package restapi

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
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
		{"negative time", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "time": {"-1"}}, []string{"time"}},
		{"signed time", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "time": {"+1749837600000"}}, []string{"time"}},
		{"date-only time", url.Values{"lat": {"40.539367"}, "lon": {"-122.34952"}, "time": {"2025-06-13"}}, []string{"time"}},
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

// time accepts yyyy-MM-dd_HH-mm-ss in the agency timezone, like Java. The
// arrivals must cluster around the requested hour, proving the value was read
// as a datetime rather than ignored in favor of the clock.
func TestArrivalsAndDeparturesForLocationAcceptsDateTimeFormat(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		arrivalsForLocationURL(url.Values{
			"lat":    {"40.539367"},
			"lon":    {"-122.34952"},
			"radius": {"2500"},
			"time":   {"2025-06-13_08-00-00"},
		}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.FieldErrors)

	arrivals := model.Data.Entry.ArrivalsAndDepartures
	require.NotEmpty(t, arrivals, "Friday 08:00 must have scheduled service on the fixture")

	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	// The shared URL helper requests minutesBefore=60 and minutesAfter=240.
	windowStart := time.Date(2025, 6, 13, 7, 0, 0, 0, loc).UnixMilli()
	windowEnd := time.Date(2025, 6, 13, 12, 0, 0, 0, loc).UnixMilli()
	for _, a := range arrivals {
		scheduled := a.ScheduledArrivalTime.UnixMilli()
		assert.GreaterOrEqual(t, scheduled, windowStart, "arrival scheduled before the requested window")
		assert.LessOrEqual(t, scheduled, windowEnd, "arrival scheduled after the requested window")
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

func TestArrivalsAndDeparturesForLocationIncludesStopAlertsWithoutArrivals(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agencyID, stopID, err := utils.ExtractAgencyIDAndCodeID(testdata.Stop4062.ID)
	require.NoError(t, err)

	const alertID = "location-no-arrivals-stop-alert"
	api.GtfsManager.AddAlertForTest(gtfs.Alert{
		ID:               alertID,
		InformedEntities: []gtfs.AlertInformedEntity{{StopID: &stopID}},
		Header:           []gtfs.AlertText{{Text: "Stop alert without arrivals", Language: "en"}},
	})

	quietTime := time.Date(2025, 6, 13, 3, 0, 0, 0, arrivalsTestClock.Location())
	resp, model := callArrivalsForLocation(t, api, url.Values{
		"time":          {strconv.FormatInt(quietTime.UnixMilli(), 10)},
		"minutesBefore": {"0"},
		"minutesAfter":  {"0"},
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, model.Data.Entry.ArrivalsAndDepartures,
		"the quiet-time query must have no arrivals")

	wantSituationID := utils.FormCombinedID(agencyID, alertID)
	assert.Contains(t, model.Data.Entry.SituationIDs, wantSituationID)
	referenced := slices.ContainsFunc(model.Data.References.Situations, func(situation models.Situation) bool {
		return situation.ID == wantSituationID
	})
	assert.True(t, referenced, "the stop alert must resolve in references.situations")
}

// stopIds come nearest first so maxCount truncation drops the farthest
// stops; arrivals group by stop in the same order with best-arrival-time
// order inside each stop.
func TestArrivalsAndDeparturesForLocationOrdersNearestStopFirst(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api)
	entry := model.Data.Entry
	require.Greater(t, len(entry.StopIDs), 1, "need at least two stops to check ordering")
	require.Greater(t, len(entry.ArrivalsAndDepartures), 1, "need at least two arrivals to check ordering")

	coords := make(map[string]models.Stop, len(model.Data.References.Stops))
	for _, s := range model.Data.References.Stops {
		coords[s.ID] = s
	}
	distance := func(id string) float64 {
		s, ok := coords[id]
		require.True(t, ok, "stop %s must resolve in references.stops", id)
		return utils.Distance(40.539367, -122.34952, s.Lat, s.Lon)
	}

	for i := 1; i < len(entry.StopIDs); i++ {
		assert.LessOrEqual(t, distance(entry.StopIDs[i-1]), distance(entry.StopIDs[i]),
			"stopIds must be ordered nearest first")
	}

	effective := func(a models.ArrivalAndDeparture) int64 {
		if a.PredictedArrivalTime.UnixMilli() > 0 {
			return a.PredictedArrivalTime.UnixMilli()
		}
		return a.ScheduledArrivalTime.UnixMilli()
	}
	arrivals := entry.ArrivalsAndDepartures
	for i := 1; i < len(arrivals); i++ {
		prev, curr := arrivals[i-1], arrivals[i]
		if prev.StopID == curr.StopID {
			assert.LessOrEqual(t, effective(prev), effective(curr),
				"arrivals at one stop must be ordered by best arrival time")
		} else {
			assert.LessOrEqual(t, distance(prev.StopID), distance(curr.StopID),
				"arrivals must be grouped nearest stop first")
		}
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
	// arrivals maxCount trimmed away. "Retained" spans every ID the kept
	// arrivals name, not just the three lists: maxCount trims stopIDs and
	// arrivals independently, so a kept arrival's own stop can be absent from
	// stopIDs, and its trip status names stops and an active trip that no list
	// carries.
	retainedTrips := make(map[string]bool, len(entry.ArrivalsAndDepartures))
	retainedRoutes := make(map[string]bool, len(entry.ArrivalsAndDepartures))
	for _, a := range entry.ArrivalsAndDepartures {
		retainedTrips[a.TripID] = true
		retainedRoutes[a.RouteID] = true
		if a.TripStatus != nil && a.TripStatus.ActiveTripID != "" {
			retainedTrips[a.TripStatus.ActiveTripID] = true
		}
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
	for _, a := range entry.ArrivalsAndDepartures {
		for _, id := range arrivalStopIDs(a) {
			retainedStops[id] = true
		}
	}
	referencedStops := make(map[string]bool, len(limited.Data.References.Stops))
	for _, s := range limited.Data.References.Stops {
		assert.True(t, retainedStops[s.ID], "references.stops %s is not in the retained entry", s.ID)
		referencedStops[s.ID] = true
	}
	for stopID := range retainedStops {
		assert.True(t, referencedStops[stopID], "retained stop %s must resolve in references.stops", stopID)
	}

	// Trip status is built after the trim. The one arrival that survives must
	// still match the same row from the untrimmed response.
	require.NotEmpty(t, entry.ArrivalsAndDepartures)
	require.NotEmpty(t, full.Data.Entry.ArrivalsAndDepartures)
	kept := entry.ArrivalsAndDepartures[0]
	first := full.Data.Entry.ArrivalsAndDepartures[0]
	assert.Equal(t, first.TripID, kept.TripID)
	assert.Equal(t, first.StopID, kept.StopID)
	assert.Equal(t, first.ScheduledArrivalTime, kept.ScheduledArrivalTime)
	require.NotNil(t, kept.TripStatus, "trip status is built for arrivals that survive maxCount")
	assert.Equal(t, first.TripStatus, kept.TripStatus)
	assert.Equal(t, first.DistanceFromStop, kept.DistanceFromStop)
	assert.Equal(t, first.NumberOfStopsAway, kept.NumberOfStopsAway)
	assert.Equal(t, first.BlockTripSequence, kept.BlockTripSequence)
	assert.Equal(t, first.SituationIDs, kept.SituationIDs)
}

// Java puts only stop alerts and route alerts on the entry's situationIds. An
// arrival's own alerts ride on that arrival and in the references, and an
// arrival maxCount drops contributes nothing at all.
func TestArrivalsAndDeparturesForLocationKeepsTripAlertsOffTheEntry(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, full := callArrivalsForLocation(t, api)
	require.NotEmpty(t, full.Data.Entry.StopIDs)
	require.GreaterOrEqual(t, len(full.Data.Entry.ArrivalsAndDepartures), 2)
	keptStop := full.Data.Entry.StopIDs[0]
	keptTrip := full.Data.Entry.ArrivalsAndDepartures[0].TripID

	var dropped models.ArrivalAndDeparture
	found := false
	for _, arrival := range full.Data.Entry.ArrivalsAndDepartures[1:] {
		if arrival.StopID == keptStop && arrival.TripID != keptTrip {
			dropped = arrival
			found = true
			break
		}
	}
	require.True(t, found, "nearest stop needs a second arrival so maxCount can drop it")

	keptAgencyID, keptTripID, err := utils.ExtractAgencyIDAndCodeID(keptTrip)
	require.NoError(t, err)
	droppedAgencyID, droppedTripID, err := utils.ExtractAgencyIDAndCodeID(dropped.TripID)
	require.NoError(t, err)

	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID:               "kept-trip-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{TripID: &gtfs.TripID{ID: keptTripID}}},
		Header:           []gtfs.AlertText{{Text: "Kept trip alert", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID:               "trimmed-trip-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{TripID: &gtfs.TripID{ID: droppedTripID}}},
		Header:           []gtfs.AlertText{{Text: "Trimmed trip alert", Language: "en"}},
	})
	keptSituationID := utils.FormCombinedID(keptAgencyID, "kept-trip-alert")
	trimmedSituationID := utils.FormCombinedID(droppedAgencyID, "trimmed-trip-alert")

	_, limited := callArrivalsForLocation(t, api, url.Values{"maxCount": {"1"}})
	require.Len(t, limited.Data.Entry.ArrivalsAndDepartures, 1)
	assert.Equal(t, full.Data.Entry.ArrivalsAndDepartures[0].TripID, limited.Data.Entry.ArrivalsAndDepartures[0].TripID)

	// The retained arrival's own alert rides on the arrival and in the
	// references, never on the entry.
	assert.Contains(t, limited.Data.Entry.ArrivalsAndDepartures[0].SituationIDs, keptSituationID)
	assert.NotContains(t, limited.Data.Entry.SituationIDs, keptSituationID)
	assert.Contains(t, situationIDsFromReferences(limited.Data.References.Situations), keptSituationID)

	// The trimmed arrival is not in the response, so nothing it referenced is.
	assert.NotContains(t, limited.Data.Entry.SituationIDs, trimmedSituationID)
	assert.NotContains(t, situationIDsFromReferences(limited.Data.References.Situations), trimmedSituationID)
}

// situationIDsFromReferences returns the IDs of the situation references.
func situationIDsFromReferences(situations []models.Situation) []string {
	ids := make([]string, 0, len(situations))
	for _, s := range situations {
		ids = append(ids, s.ID)
	}
	return ids
}

// arrivalStopIDs returns every stop ID one arrival names: its own stop plus the
// stops its trip status points at.
func arrivalStopIDs(a models.ArrivalAndDeparture) []string {
	ids := []string{a.StopID}
	if a.TripStatus != nil {
		ids = append(ids, a.TripStatus.NextStop, a.TripStatus.ClosestStop)
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			kept = append(kept, id)
		}
	}
	return kept
}

// TestArrivalsAndDeparturesForLocationReferencesResolveArrivalIDs pins the
// invariant recordTripStatusReferences exists for: every ID an arrival emits
// resolves in the references block. The maxCount test covers the truncated
// case; this one covers an untruncated response, where the trip status stops
// and active trip are the IDs no entry list carries.
func TestArrivalsAndDeparturesForLocationReferencesResolveArrivalIDs(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	_, model := callArrivalsForLocation(t, api, url.Values{
		"minutesBefore": {"5"},
		"minutesAfter":  {"35"},
		"maxCount":      {strconv.Itoa(models.MaxCountForArrivalsForLocation)},
	})

	entry := model.Data.Entry
	require.NotEmpty(t, entry.ArrivalsAndDepartures)
	require.False(t, entry.LimitExceeded, "this case must not truncate any list")

	referencedStops := make(map[string]bool, len(model.Data.References.Stops))
	for _, s := range model.Data.References.Stops {
		referencedStops[s.ID] = true
	}
	referencedTrips := make(map[string]bool, len(model.Data.References.Trips))
	for _, tr := range model.Data.References.Trips {
		referencedTrips[tr.ID] = true
	}

	sawTripStatusStop := false
	sawReassignedTrip := false
	for i, a := range entry.ArrivalsAndDepartures {
		for _, id := range arrivalStopIDs(a) {
			assert.True(t, referencedStops[id],
				"arrival[%d] names stop %s, which must resolve in references.stops", i, id)
		}
		assert.True(t, referencedTrips[a.TripID],
			"arrival[%d].tripId %s must resolve in references.trips", i, a.TripID)
		if a.TripStatus == nil {
			continue
		}
		if a.TripStatus.NextStop != "" || a.TripStatus.ClosestStop != "" {
			sawTripStatusStop = true
		}
		if a.TripStatus.ActiveTripID == "" {
			continue
		}
		if a.TripStatus.ActiveTripID != a.TripID {
			sawReassignedTrip = true
		}
		assert.True(t, referencedTrips[a.TripStatus.ActiveTripID],
			"arrival[%d].tripStatus.activeTripId %s must resolve in references.trips", i, a.TripStatus.ActiveTripID)
	}
	require.True(t, sawTripStatusStop,
		"fixture must produce a trip status naming a stop, or this test proves nothing")
	require.True(t, sawReassignedTrip,
		"fixture must produce an activeTripId differing from the arrival's trip")
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

	t.Run("bad time in an empty area still fails validation", func(t *testing.T) {
		q := url.Values{"time": {"garbage"}}
		maps.Copy(q, emptyArea)
		resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
			arrivalsForLocationURL(q))

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, model.Data.FieldErrors, "time")
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

// A neighbour near the corner of the 100 m square box is inside the box on
// both axes but over 100 m away in a straight line. Java still counts it as
// nearby; a straight-line circle check would wrongly drop it.
func TestNearbyCandidateIDsIncludesBoxCornerNeighbor(t *testing.T) {
	center := gtfsdb.Stop{ID: "center", Lat: 40.0, Lon: -122.0}
	corner := gtfsdb.Stop{ID: "corner",
		Lat: 40.0 + 90.0/111320.0,
		// cos(40°) ≈ 0.766 corrects the longitude scale.
		Lon: -122.0 + 90.0/(111320.0*0.766),
	}
	far := gtfsdb.Stop{ID: "far",
		Lat: 40.0 + 150.0/111320.0,
		Lon: -122.0 + 150.0/(111320.0*0.766),
	}

	require.Greater(t, utils.Distance(center.Lat, center.Lon, corner.Lat, corner.Lon), 100.0,
		"corner neighbour must be outside a 100 m circle for the test to mean anything")

	nearIDs, err := nearbyCandidateIDs(context.Background(),
		[]gtfsdb.Stop{corner, far, center}, []gtfsdb.Stop{center})
	require.NoError(t, err)
	assert.True(t, nearIDs["corner"], "box-corner neighbour must be nearby")
	assert.False(t, nearIDs["far"], "stop outside the box must not be nearby")
	assert.False(t, nearIDs["center"], "a stop must never be nearby to itself")
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

// A route that serves a searched stop still contributes its alert when the
// window contains none of that route's arrivals, and a stop alert is recorded
// even when the window matches no stop_time.
func TestArrivalsAndDeparturesForLocationKeepsAlertsWithoutArrivals(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, wide := callArrivalsForLocation(t, api)
	require.NotEmpty(t, wide.Data.Entry.ArrivalsAndDepartures)
	require.Contains(t, wide.Data.Entry.StopIDs, arrivalsTestStopID)

	agencyID, stopCode, err := utils.ExtractAgencyIDAndCodeID(arrivalsTestStopID)
	require.NoError(t, err)

	var routeCode string
	for _, arrival := range wide.Data.Entry.ArrivalsAndDepartures {
		if arrival.StopID != arrivalsTestStopID {
			continue
		}
		_, routeCode, err = utils.ExtractAgencyIDAndCodeID(arrival.RouteID)
		require.NoError(t, err)
		break
	}
	require.NotEmpty(t, routeCode)

	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID: "loc-route-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{
			AgencyID: &agencyID,
			RouteID:  &routeCode,
		}},
		Header: []gtfs.AlertText{{Text: "route without an arrival", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID: "loc-stop-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{
			AgencyID: &agencyID,
			StopID:   &stopCode,
		}},
		Header: []gtfs.AlertText{{Text: "stop with no stop time", Language: "en"}},
	})

	// Ten years on, the same stops are still in the box and none of them have
	// an arrival. The alerts cannot have come from an arrival row.
	far := strconv.FormatInt(arrivalsTestClock.AddDate(10, 0, 0).UnixMilli(), 10)
	resp, narrow := callArrivalsForLocation(t, api, url.Values{"time": {far}})

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, narrow.Data.Entry.ArrivalsAndDepartures)
	require.Contains(t, narrow.Data.Entry.StopIDs, arrivalsTestStopID)

	wantRoute := utils.FormCombinedID(agencyID, "loc-route-alert")
	wantStop := utils.FormCombinedID(agencyID, "loc-stop-alert")
	assert.Contains(t, narrow.Data.Entry.SituationIDs, wantRoute)
	assert.Contains(t, narrow.Data.Entry.SituationIDs, wantStop)

	var sawRoute, sawStop bool
	for _, situation := range narrow.Data.References.Situations {
		switch situation.ID {
		case wantRoute:
			sawRoute = true
			require.NotNil(t, situation.Summary)
			assert.Equal(t, "route without an arrival", situation.Summary.Value)
		case wantStop:
			sawStop = true
			require.NotNil(t, situation.Summary)
			assert.Equal(t, "stop with no stop time", situation.Summary.Value)
		}
	}
	assert.True(t, sawRoute, "references must include the route alert")
	assert.True(t, sawStop, "references must include the stop alert")
}

func TestAddRouteAlertsForRetainedStopsEmpty(t *testing.T) {
	api := createTestApi(t)
	require.NoError(t, api.addRouteAlertsForRetainedStops(context.Background(), nil, newSituationCollector()))
}

func TestAddRouteAlertsForRetainedStopsQueryError(t *testing.T) {
	api := createTestApi(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := api.addRouteAlertsForRetainedStops(ctx, []gtfsdb.Stop{{ID: testdata.Stop4062.Code}}, newSituationCollector())
	require.Error(t, err)
}

// A cancelled request must not be reported as a server failure: client
// cancellation writes nothing, an expired deadline writes 504.
func TestSendArrivalsForLocationErrorDistinguishesCancellation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	newRequest := func(ctx context.Context) (*httptest.ResponseRecorder, *http.Request) {
		req := httptest.NewRequest(http.MethodGet, "/api/where/arrivals-and-departures-for-location.json", nil).WithContext(ctx)
		return httptest.NewRecorder(), req
	}

	t.Run("client cancellation writes nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec, req := newRequest(ctx)

		api.sendArrivalsForLocationError(rec, req, ctx, errors.New("boom"))

		assert.Empty(t, rec.Body.String(), "no body should be written when the client cancels")
	})

	t.Run("expired deadline writes gateway timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		rec, req := newRequest(ctx)

		api.sendArrivalsForLocationError(rec, req, ctx, errors.New("boom"))

		assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
		assert.Contains(t, rec.Body.String(), "gateway timeout")
	})

	t.Run("genuine failure writes server error", func(t *testing.T) {
		ctx := context.Background()
		rec, req := newRequest(ctx)

		api.sendArrivalsForLocationError(rec, req, ctx, errors.New("boom"))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// Nearby stops served only by routes with no service on the query date are
// dropped, matching Java (e.g. a seasonal shuttle outside its season).
func TestBuildNearbyResultsDropsStopsInactiveOnDate(t *testing.T) {
	stops := []gtfsdb.Stop{
		{ID: "active", Lat: 40.0, Lon: -122.0},
		{ID: "inactive", Lat: 40.0, Lon: -122.0},
	}
	combined := map[string]string{"active": "25_active", "inactive": "25_inactive"}
	loc := &internalgtfs.LocationParams{Lat: 40.0, Lon: -122.0}

	results := buildNearbyResults(stops, nil, map[string]bool{"active": true, "inactive": false}, combined, loc)

	require.Len(t, results, 1, "stops with no active routes must be dropped")
	assert.Equal(t, "25_active", results[0].StopID)
}

// An agency id that contains an underscore must stay intact when namespacing
// the alert. Agency and route ids are separate columns, so the alert is keyed
// under the full agency id rather than the first underscore segment.
func TestAddRouteAlertsForRetainedStopsKeepsAgencyUnderscore(t *testing.T) {
	api := createTestApi(t)
	db := api.GtfsManager.GtfsDB.DB

	const (
		agencyID = "a_b"
		routeID  = "r_under"
		tripID   = "t_under"
		service  = "svc_under"
		alertID  = "under-alert"
	)
	// The package shares one GTFS database. These rows have to come back out
	// before the next test reads stop 4062 or the agency list.
	t.Cleanup(func() {
		api.GtfsManager.MockResetRealTimeData()
		_, _ = db.Exec(`DELETE FROM stop_times WHERE trip_id = ?`, tripID)
		_, _ = db.Exec(`DELETE FROM trips WHERE id = ?`, tripID)
		_, _ = db.Exec(`DELETE FROM routes WHERE id = ?`, routeID)
		_, _ = db.Exec(`DELETE FROM calendar WHERE id = ?`, service)
		_, _ = db.Exec(`DELETE FROM agencies WHERE id = ?`, agencyID)
	})
	_, err := db.Exec(`INSERT INTO agencies (id, name, url, timezone) VALUES (?, 'Underscore Agency', 'http://example.com', 'America/Los_Angeles')`, agencyID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO routes (id, agency_id, short_name, type) VALUES (?, ?, 'Under', 3)`, routeID, agencyID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES (?, 1, 1, 1, 1, 1, 1, 1, '20200101', '20991231')`, service)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO trips (id, route_id, service_id) VALUES (?, ?, ?)`, tripID, routeID, service)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) VALUES (?, 0, 0, ?, 1)`, tripID, testdata.Stop4062.Code)
	require.NoError(t, err)

	bareRoute := routeID
	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID: alertID,
		InformedEntities: []gtfs.AlertInformedEntity{{
			RouteID: &bareRoute,
		}},
		Header: []gtfs.AlertText{{Text: "underscore agency", Language: "en"}},
	})

	situations := newSituationCollector()
	require.NoError(t, api.addRouteAlertsForRetainedStops(context.Background(), []gtfsdb.Stop{{ID: testdata.Stop4062.Code}}, situations))

	ids := situationIDsFromRefs(situations.refs)
	assert.Contains(t, ids, utils.FormCombinedID(agencyID, alertID))
	assert.NotContains(t, ids, utils.FormCombinedID("a", alertID))
}

// A route alert for a stop whose agency id contains an underscore must still
// appear in situationIds. Splitting the combined stop id at the first "_"
// (agency a_b, stop 123 → a_b_123 → "b_123") would miss the stop entirely.
func TestArrivalsAndDeparturesForLocationRouteAlertUnderscoreAgency(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	db := api.GtfsManager.GtfsDB.DB

	const (
		agencyID = "a_b"
		stopID   = "123"
		routeID  = "r_stop_under"
		tripID   = "t_stop_under"
		service  = "svc_stop_under"
		alertID  = "stop-under-alert"
	)
	t.Cleanup(func() {
		api.GtfsManager.MockResetRealTimeData()
		_, _ = db.Exec(`DELETE FROM stop_times WHERE trip_id = ?`, tripID)
		_, _ = db.Exec(`DELETE FROM trips WHERE id = ?`, tripID)
		_, _ = db.Exec(`DELETE FROM routes WHERE id = ?`, routeID)
		_, _ = db.Exec(`DELETE FROM calendar WHERE id = ?`, service)
		_, _ = db.Exec(`DELETE FROM stops WHERE id = ?`, stopID)
		_, _ = db.Exec(`DELETE FROM agencies WHERE id = ?`, agencyID)
	})

	_, err := db.Exec(`INSERT INTO agencies (id, name, url, timezone) VALUES (?, 'Underscore Agency', 'http://example.com', 'America/Los_Angeles')`, agencyID)
	require.NoError(t, err)
	// Same coordinates as the query centre so the stop is retained under maxCount.
	_, err = db.Exec(`INSERT INTO stops (id, code, name, lat, lon, location_type, wheelchair_boarding) VALUES (?, ?, 'Underscore Stop', 40.539367, -122.34952, 0, 0)`, stopID, stopID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO routes (id, agency_id, short_name, type) VALUES (?, ?, 'Under', 3)`, routeID, agencyID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO calendar (id, monday, tuesday, wednesday, thursday, friday, saturday, sunday, start_date, end_date)
		VALUES (?, 1, 1, 1, 1, 1, 1, 1, '20200101', '20991231')`, service)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO trips (id, route_id, service_id) VALUES (?, ?, ?)`, tripID, routeID, service)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO stop_times (trip_id, arrival_time, departure_time, stop_id, stop_sequence) VALUES (?, 0, 0, ?, 1)`, tripID, stopID)
	require.NoError(t, err)

	bareRoute := routeID
	api.GtfsManager.MockAddAlert("feed-0", gtfs.Alert{
		ID: alertID,
		InformedEntities: []gtfs.AlertInformedEntity{{
			RouteID: &bareRoute,
		}},
		Header: []gtfs.AlertText{{Text: "underscore agency stop", Language: "en"}},
	})

	// Far-future window: no arrivals, so the route alert can only come from
	// addRouteAlertsForRetainedStops on the retained stop list.
	far := strconv.FormatInt(arrivalsTestClock.AddDate(10, 0, 0).UnixMilli(), 10)
	resp, model := callArrivalsForLocation(t, api, url.Values{"time": {far}})

	require.Equal(t, http.StatusOK, resp.StatusCode)
	combinedStop := utils.FormCombinedID(agencyID, stopID)
	require.Contains(t, model.Data.Entry.StopIDs, combinedStop)
	assert.Empty(t, model.Data.Entry.ArrivalsAndDepartures)
	assert.Contains(t, model.Data.Entry.SituationIDs, utils.FormCombinedID(agencyID, alertID))
}
