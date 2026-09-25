package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/models"
)

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
		// Decoded untyped: the typed model cannot tell a missing status key from a null one.
		resp, body := callAPIHandler[map[string]any](t, api, "/api/where/trip-details/"+tripID+".json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
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

	resp, trips := callAPIHandler[TripsForRouteResponse](t, api, "/api/where/trips-for-route/gd_hermann.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, tripIDsOf(trips.Data.List), "gd_her-trip", "a trip mixing timed and zone records keeps its fixed-route presence")
}

func TestWindowedStopRecord_DoesNotCreateStopRouteRelations(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-group-deviated.zip", flexfixtures.GroupDeviatedFiles())

	resp, stop := callAPIHandler[StopEntryResponse](t, api, "/api/where/stop/gd_w1.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, stop.Data.Entry.RouteIDs, "a windowed stop record contributes no stop↔route relation (spec §9.1)")
	assert.Equal(t, []string{"gd_winstop"}, stop.Data.Entry.OnDemandServiceIDs, "the stop surfaces only through its pointer")

	resp, route := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/gd_winstop.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, route.Data.Entry.StopIds)

	resp, agencyStops := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-agency/gd.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	found := false
	for _, s := range agencyStops.Data.List {
		if s.ID == "gd_w1" {
			found = true
			assert.Equal(t, []string{"gd_winstop"}, s.OnDemandServiceIDs)
		}
	}
	assert.True(t, found, "the stop agency index covers rule-referenced stops with no stop_times")
}

// blockSharingFlexFiles is the group/deviated fixture with a flex-only trip,
// her-flex, added to her-block. Its NULL cached time bounds sort before every
// timed trip in block-ordered queries.
func blockSharingFlexFiles() map[string]string {
	files := flexfixtures.GroupDeviatedFiles()
	files["trips.txt"] += "hermann,svc,her-flex,her-block,\n"
	files["stop_times.txt"] += "her-flex,,,,zone_a,,1,2,1,08:00:00,12:00:00,br_ruf,br_ruf\n" +
		"her-flex,,,,zone_a,,2,1,2,08:00:00,12:00:00,br_ruf,br_ruf\n"
	return files
}

func TestFlexOnlyTripInTimedBlock_StaysOffBlockSurfaces(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-shared-block.zip", blockSharingFlexFiles())
	const flexTripID = "gd_her-flex"

	t.Run("trips-for-location still finds the timed trip", func(t *testing.T) {
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, "/api/where/trips-for-location.json?key=TEST&lat=44.32&lon=-94.45&latSpan=0.1&lonSpan=0.1")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		tripIDs := tripIDsOf(model.Data.List)
		assert.Contains(t, tripIDs, "gd_her-trip")
		assert.NotContains(t, tripIDs, flexTripID)
	})

	t.Run("trip-details links only timed block neighbours", func(t *testing.T) {
		resp, model := callAPIHandler[TripDetailsResponse](t, api, "/api/where/trip-details/gd_her-trip.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotNil(t, model.Data.Entry.Schedule)
		assert.Empty(t, model.Data.Entry.Schedule.PreviousTripID)
		assert.Equal(t, "gd_her-trip-2", model.Data.Entry.Schedule.NextTripID)
	})

	t.Run("block lists only the timed trips", func(t *testing.T) {
		resp, model := callAPIHandler[BlockEntryResponse](t, api, "/api/where/block/gd_her-block.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var tripIDs []string
		for _, config := range model.Data.Entry.Configurations {
			for _, trip := range config.Trips {
				tripIDs = append(tripIDs, trip.TripId)
			}
		}
		assert.Equal(t, []string{"gd_her-trip", "gd_her-trip-2"}, tripIDs)
	})

	t.Run("block trip index skips the flex-only trip", func(t *testing.T) {
		var entries int
		row := api.GtfsManager.GtfsDB.DB.QueryRow("SELECT COUNT(*) FROM block_trip_entry WHERE trip_id = 'her-flex'")
		require.NoError(t, row.Scan(&entries))
		assert.Zero(t, entries)
	})
}

func tripIDsOf[E interface{ GetTripId() string }](entries []E) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetTripId())
	}
	return ids
}
