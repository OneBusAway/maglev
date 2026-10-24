package restapi

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

func agencyPrefixFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"own,Owner Agency,http://example.com,UTC\n" +
			"other,Other Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"own-route,own,OW,Owner Route,3\n" +
			"other-route,other,OT,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stop-1,Stop One,37.7749,-122.4194\n" +
			"stop-2,Stop Two,37.7849,-122.4094\n",
		"shapes.txt": "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\n" +
			"own-shape,37.7749,-122.4194,1\n" +
			"own-shape,37.7849,-122.4094,2\n",
		"trips.txt": "route_id,service_id,trip_id,block_id,shape_id\n" +
			"own-route,svc,own-trip,own-block,own-shape\n" +
			"other-route,svc,other-trip,other-block,\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"own-trip,09:00:00,09:00:00,stop-1,1\n" +
			"own-trip,09:30:00,09:30:00,stop-2,2\n" +
			"other-trip,10:00:00,10:00:00,stop-1,1\n" +
			"other-trip,10:30:00,10:30:00,stop-2,2\n",
	}
}

func TestEntityHandlers_AgencyPrefixMustOwnEntity(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	entities := []struct {
		endpoint string
		entityID string
		query    string
	}{
		{"trip", "own-trip", ""},
		{"route", "own-route", ""},
		{"shape", "own-shape", ""},
		{"block", "own-block", ""},
		// Java OneBusAway gives each stop one ID, under its feed's first agency,
		// even when another agency's routes serve it.
		{"stop", "stop-1", ""},
		{"schedule-for-stop", "stop-1", ""},
		{"arrivals-and-departures-for-stop", "stop-1", ""},
		{"arrival-and-departure-for-stop", "stop-1", "&tripId=own_own-trip&serviceDate=1735689600000"},
	}
	prefixes := []struct {
		agencyID   string
		wantStatus int
	}{
		{"own", http.StatusOK},
		{"other", http.StatusNotFound},
		{"unknown", http.StatusNotFound},
	}

	for _, entity := range entities {
		for _, prefix := range prefixes {
			path := "/api/where/" + entity.endpoint + "/" + prefix.agencyID + "_" + entity.entityID + ".json?key=TEST" + entity.query
			t.Run(entity.endpoint+"/"+prefix.agencyID, func(t *testing.T) {
				resp, model := serveApiAndRetrieveEndpoint(t, api, path)
				assert.Equal(t, prefix.wantStatus, resp.StatusCode)
				assert.Equal(t, prefix.wantStatus, model.Code)
			})
		}
	}
}

// A stop keeps its own agency in references, even when a trip or route of
// another agency is what refers to it.
func TestStopReferences_UseTheStopsOwnAgency(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	for _, endpoint := range []string{
		"/api/where/trip-details/other_other-trip.json?key=TEST",
		"/api/where/schedule-for-route/other_other-route.json?key=TEST&date=2025-01-01",
	} {
		t.Run(endpoint, func(t *testing.T) {
			resp, model := callAPIHandler[scheduleStopResponse](t, api, endpoint)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var stopIDs []string
			for _, stop := range model.Data.References.Stops {
				stopIDs = append(stopIDs, stop.ID)
			}
			assert.ElementsMatch(t, []string{"own_stop-1", "own_stop-2"}, stopIDs)
		})
	}
}

// Trip status, trip stop times and block stop times name each stop by its own
// agency, not the agency of the trip that visits it.
func TestTripStopIDs_UseTheStopsOwnAgency(t *testing.T) {
	now := time.Date(2025, 1, 1, 10, 15, 0, 0, time.UTC)
	midTrip := clock.NewMockClock(now)
	api := createTestApiWithGTFSFixture(t, midTrip, "agency-prefix.zip", agencyPrefixFiles())
	serviceDate := strconv.FormatInt(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10)
	api.GtfsManager.MockAddVehicleWithOptions("bus", "other-trip", "other-route", internalgtfs.MockVehicleOptions{Timestamp: &now})
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	_, details := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/trip-details/other_other-trip.json?key=TEST&includeStatus=true&serviceDate="+serviceDate)
	entry := details.Data.(map[string]any)["entry"].(map[string]any)
	status := entry["status"].(map[string]any)
	assert.Equal(t, "own_stop-1", status["closestStop"])
	assert.Equal(t, "own_stop-2", status["nextStop"])
	assert.Equal(t, []string{"own_stop-1", "own_stop-2"}, stopIDsOf(entry["schedule"].(map[string]any)["stopTimes"]))

	_, block := serveApiAndRetrieveEndpoint(t, api, "/api/where/block/other_other-block.json?key=TEST")
	configuration := block.Data.(map[string]any)["entry"].(map[string]any)["configurations"].([]any)[0].(map[string]any)
	trip := configuration["trips"].([]any)[0].(map[string]any)
	var blockStopIDs []string
	for _, blockStopTime := range trip["blockStopTimes"].([]any) {
		blockStopIDs = append(blockStopIDs, blockStopTime.(map[string]any)["stopTime"].(map[string]any)["stopId"].(string))
	}
	assert.Equal(t, []string{"own_stop-1", "own_stop-2"}, blockStopIDs)
}

func stopIDsOf(stopTimes any) []string {
	var stopIDs []string
	for _, stopTime := range stopTimes.([]any) {
		stopIDs = append(stopIDs, stopTime.(map[string]any)["stopId"].(string))
	}
	return stopIDs
}

// Route stop lists name each stop by its own agency, not the route's.
func TestRouteStopIDs_UseTheStopsOwnAgency(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	_, stopsForRoute := serveApiAndRetrieveEndpoint(t, api, "/api/where/stops-for-route/other_other-route.json?key=TEST")
	entry := stopsForRoute.Data.(map[string]any)["entry"].(map[string]any)
	assert.ElementsMatch(t, []any{"own_stop-1", "own_stop-2"}, entry["stopIds"])
	grouping := entry["stopGroupings"].([]any)[0].(map[string]any)
	group := grouping["stopGroups"].([]any)[0].(map[string]any)
	assert.ElementsMatch(t, []any{"own_stop-1", "own_stop-2"}, group["stopIds"])

	_, schedule := serveApiAndRetrieveEndpoint(t, api, "/api/where/schedule-for-route/other_other-route.json?key=TEST&date=2025-01-01")
	tripGrouping := schedule.Data.(map[string]any)["entry"].(map[string]any)["stopTripGroupings"].([]any)[0].(map[string]any)
	assert.ElementsMatch(t, []any{"own_stop-1", "own_stop-2"}, tripGrouping["stopIds"])
	tripWithStopTimes := tripGrouping["tripsWithStopTimes"].([]any)[0].(map[string]any)
	assert.Equal(t, []string{"own_stop-1", "own_stop-2"}, stopIDsOf(tripWithStopTimes["stopTimes"]))
}

// Location searches and nearby stops name each stop by its own agency, not the
// first agency whose routes serve it.
func TestLocationStopIDs_UseTheStopsOwnAgency(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	_, stops := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/stops-for-location.json?key=TEST&lat=37.7749&lon=-122.4194&radius=2000")
	var stopIDs []any
	for _, stop := range stops.Data.(map[string]any)["list"].([]any) {
		stopIDs = append(stopIDs, stop.(map[string]any)["id"])
	}
	assert.ElementsMatch(t, []any{"own_stop-1", "own_stop-2"}, stopIDs)

	_, arrivals := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/arrivals-and-departures-for-location.json?key=TEST&lat=37.7749&lon=-122.4194&radius=2000")
	entry := arrivals.Data.(map[string]any)["entry"].(map[string]any)
	assert.ElementsMatch(t, []any{"own_stop-1", "own_stop-2"}, entry["stopIds"])
}

// Block references describe stops and routes the same way every other
// endpoint does: each route under its own agency, each stop with its routes.
func TestBlockReferences_MatchTheSharedBuilders(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	_, model := callAPIHandler[scheduleStopResponse](t, api, "/api/where/block/other_other-block.json?key=TEST")
	references := model.Data.References

	var routeIDs, agencyIDs []string
	for _, route := range references.Routes {
		routeIDs = append(routeIDs, route.ID)
	}
	for _, agency := range references.Agencies {
		agencyIDs = append(agencyIDs, agency.ID)
	}
	assert.ElementsMatch(t, []string{"own_own-route", "other_other-route"}, routeIDs)
	assert.ElementsMatch(t, []string{"own", "other"}, agencyIDs)

	require.NotEmpty(t, references.Stops)
	for _, stop := range references.Stops {
		assert.ElementsMatch(t, []string{"own_own-route", "other_other-route"}, stop.RouteIDs, stop.ID)
	}
}
