package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/models"
)

func getJSON(t *testing.T, api *RestAPI, endpoint string) (int, map[string]any) {
	t.Helper()
	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()
	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed), string(body))
	return resp.StatusCode, parsed
}

func TestFlexOnlyTrips_LegacySurfaces(t *testing.T) {
	api := alexandriaAPI(t)
	const tripID = "5088_t_6124961_b_85952_tn_0"

	t.Run("trip/{id} returns the entity", func(t *testing.T) {
		resp, model := callAPIHandler[TripEntryResponse](t, api, "/api/where/trip/"+tripID+".json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, tripID, model.Data.Entry.ID)
		assert.Equal(t, "5088_77652", model.Data.Entry.RouteID)
		assert.Equal(t, "5088_c_71675_b_85952_d_63", model.Data.Entry.ServiceID)
	})

	t.Run("trip-details/{id} has an empty schedule and no status", func(t *testing.T) {
		status, body := getJSON(t, api, "/api/where/trip-details/"+tripID+".json?key=TEST")
		require.Equal(t, http.StatusOK, status)
		entry := body["data"].(map[string]any)["entry"].(map[string]any)
		assert.Equal(t, tripID, entry["tripId"])
		schedule := entry["schedule"].(map[string]any)
		assert.Equal(t, []any{}, schedule["stopTimes"])
		assert.Equal(t, "America/Los_Angeles", schedule["timeZone"])
		_, hasStatus := entry["status"]
		assert.False(t, hasStatus, "no timed stop_times means no status, not a 404")
		assert.Contains(t, entry, "situationIds")
	})

	t.Run("schedule-for-route has no groupings", func(t *testing.T) {
		resp, model := callAPIHandler[ScheduleForRouteResponse](t, api, "/api/where/schedule-for-route/5088_77652.json?key=TEST&date=2026-03-11")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, []models.StopTripGrouping{}, model.Data.Entry.StopTripGroupings)
		assert.Equal(t, []string{}, model.Data.Entry.ServiceIDs)
		assert.Equal(t, "5088_77652", model.Data.Entry.RouteID)
		assert.Equal(t, []string{"5088_77652"}, model.Data.References.Routes[0].OnDemandServiceIDs)
	})

	t.Run("stops-for-route has no stops", func(t *testing.T) {
		resp, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/5088_77652.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, model.Data.Entry.StopIds)
		// Same envelope as a route with no service on the date: the direction
		// grouping element stays, with no stop groups inside it.
		require.Len(t, model.Data.Entry.StopGroupings, 1)
		assert.Empty(t, model.Data.Entry.StopGroupings[0].StopGroups)
		assert.Empty(t, model.Data.Entry.Polylines)
	})

	t.Run("trips-for-route is empty", func(t *testing.T) {
		resp, model := callAPIHandler[TripsForRouteResponse](t, api, "/api/where/trips-for-route/5088_77652.json?key=TEST&time=1773241200000")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, model.Data.List)
	})

	t.Run("routes-for-agency still lists the flex route", func(t *testing.T) {
		resp, model := callAPIHandler[RoutesResponse](t, api, "/api/where/routes-for-agency/5088.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, model.Data.List, 1)
		assert.Equal(t, []string{"5088_77652"}, model.Data.List[0].OnDemandServiceIDs)
	})
}

func TestDeviatedTrip_TripDetailsShowsTimedRecordsOnly(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-group-deviated.zip", flexfixtures.GroupDeviatedFiles())

	resp, model := callAPIHandler[TripDetailsResponse](t, api, "/api/where/trip-details/gd_her-trip.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotNil(t, model.Data.Entry.Schedule)
	stopIDs := make([]string, 0)
	for _, st := range model.Data.Entry.Schedule.StopTimes {
		stopIDs = append(stopIDs, st.StopID)
	}
	assert.Equal(t, []string{"gd_h1", "gd_h2", "gd_h3"}, stopIDs, "zone records never appear as stop times")
}

func TestWindowedStopRecord_DoesNotCreateStopRouteRelations(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-group-deviated.zip", flexfixtures.GroupDeviatedFiles())

	resp, stop := callAPIHandler[StopEntryResponse](t, api, "/api/where/stop/gd_w1.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stop.Data.Entry.RouteIDs, "a windowed stop record contributes no stop↔route relation (spec §9.1)")
	assert.Equal(t, []string{"gd_winstop"}, stop.Data.Entry.OnDemandServiceIDs, "the stop surfaces only through its pointer")

	_, route := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/gd_winstop.json?key=TEST")
	assert.Empty(t, route.Data.Entry.StopIds)

	_, agencyStops := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-agency/gd.json?key=TEST")
	found := false
	for _, s := range agencyStops.Data.List {
		if s.ID == "gd_w1" {
			found = true
			assert.Equal(t, []string{"gd_winstop"}, s.OnDemandServiceIDs)
		}
	}
	assert.True(t, found, "the stop agency index covers rule-referenced stops with no stop_times")
}
