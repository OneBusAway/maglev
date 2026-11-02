package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

type SituationResponse EntryResponse[models.Situation]

func TestSituationHandler(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agency25 := "25"
	agency40 := "40"
	api.GtfsManager.MockAddAlert("feed-b", gtfs.Alert{
		ID:               "dup",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency40}},
		Header:           []gtfs.AlertText{{Text: "dup from 40", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "dup",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "dup from 25", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "twenty five late", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "25_already",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "already qualified", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "nameless", Language: "en"}},
	})
	routeOnly := "1"
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "detour",
		InformedEntities: []gtfs.AlertInformedEntity{{RouteID: &routeOnly}},
		Header:           []gtfs.AlertText{{Text: "route only", Language: "en"}},
	})

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantID      string
		wantSummary string
	}{
		{
			name:        "route-only alert",
			path:        "/api/where/situation/25_detour.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_detour",
			wantSummary: "route only",
		},
		{
			name:        "qualified id",
			path:        "/api/where/situation/25_late.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_late",
			wantSummary: "twenty five late",
		},
		{
			name:        "raw id that is already qualified",
			path:        "/api/where/situation/25_already.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_already",
			wantSummary: "already qualified",
		},
		{
			name:        "same raw id on another feed",
			path:        "/api/where/situation/25_dup.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_dup",
			wantSummary: "dup from 25",
		},
		{
			name:        "the other feed keeps its own alert",
			path:        "/api/where/situation/40_dup.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "40_dup",
			wantSummary: "dup from 40",
		},
		{
			name:       "malformed id",
			path:       "/api/where/situation/1110.json?key=TEST",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown id",
			path:       "/api/where/situation/25_missing.json?key=TEST",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[SituationResponse](t, api, tt.path)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Equal(t, tt.wantStatus, model.Code)
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, "no-cache, no-store, must-revalidate", resp.Header.Get("Cache-Control"))
				return
			}

			assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
			assert.Equal(t, tt.wantID, model.Data.Entry.ID)
			require.NotNil(t, model.Data.Entry.Summary)
			assert.Equal(t, tt.wantSummary, model.Data.Entry.Summary.Value)
			assert.Empty(t, model.Data.References.Agencies)
			assert.Empty(t, model.Data.References.Routes)
			assert.Empty(t, model.Data.References.Situations)
		})
	}
}

func TestFindSituationAlertPrefersPublishedID(t *testing.T) {
	agency25 := "25"
	agency40 := "40"
	rawCollision := gtfs.Alert{
		ID:               "25_late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency40}},
	}
	published := gtfs.Alert{
		ID:               "late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
	}

	got, ok := findSituationAlert([]gtfs.Alert{rawCollision, published}, "25", "25_late")
	require.True(t, ok)
	assert.Equal(t, "late", got.ID, "the published 25_late must win over an earlier raw id")

	got, ok = findSituationAlert([]gtfs.Alert{rawCollision}, "25", "25_late")
	require.True(t, ok)
	assert.Equal(t, "25_late", got.ID, "a raw id still matches when nothing publishes it")

	_, ok = findSituationAlert([]gtfs.Alert{published}, "25", "")
	assert.False(t, ok)

	_, ok = findSituationAlert([]gtfs.Alert{published}, "40", "40_late")
	assert.False(t, ok)
}

func TestSituationHandler_ParityAllAffectsAndConsequences(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	db := api.GtfsManager.GtfsDB.DB

	// Add an underscore route to DB under agency 25 to test raw underscore qualification
	_, err := db.Exec(`INSERT INTO routes (id, agency_id, short_name, type) VALUES ('r_under', '25', 'Under', 3)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM routes WHERE id = 'r_under'`)
	})

	agency40 := "40"
	route2Line := "2LINE"
	stop20770 := "20770"
	api.GtfsManager.MockAddAlert("feed-40", gtfs.Alert{
		ID:     "20770",
		Effect: 4, // DETOUR -> condition "", empty conditionDetails
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &agency40,
				RouteID:  &route2Line,
				StopID:   &stop20770,
			},
		},
		Header: []gtfs.AlertText{{Text: "Detour on 2LINE", Language: "en"}},
	})

	route151 := "151"
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "alert-route1",
		Effect: 1, // NO_SERVICE
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				// Selector agency is 40, but route 151 belongs to agency 25 in DB.
				// Java looks up the raw route in the DB and uses route agency (25_151).
				AgencyID: &agency40,
				RouteID:  &route151,
			},
		},
		Header: []gtfs.AlertText{{Text: "No service on route 1", Language: "en"}},
	})

	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "alert-no-effect",
		Effect: gtfs.UnknownEffect, // 8 -> proto default / absent effect
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &agency40,
				RouteID:  &route2Line,
			},
		},
		Header: []gtfs.AlertText{{Text: "Info only", Language: "en"}},
	})

	rawUnder := "r_under"
	alreadyPrefixed := "25_151"
	tripID := "TRIP_1"
	tripRoute := "TRIP_ROUTE"
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "alert-underscores",
		Effect: 2, // REDUCED_SERVICE
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &agency40,
				RouteID:  &rawUnder,
			},
			{
				AgencyID: &agency40,
				RouteID:  &alreadyPrefixed,
			},
			{
				AgencyID: &agency40,
				TripID: &gtfs.TripID{
					ID:      tripID,
					RouteID: tripRoute,
				},
			},
		},
		Header: []gtfs.AlertText{{Text: "Underscores and trip route", Language: "en"}},
	})

	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "alert-no-effect-present",
		Effect: gtfs.NoEffect, // 10 -> Java adds consequence for any effect present
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &agency40,
				RouteID:  &route2Line,
			},
		},
		Header: []gtfs.AlertText{{Text: "No effect on service", Language: "en"}},
	})

	tripRouteFallback := "TRIP_ROUTE_ONLY"
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:     "alert-trip-route-fallback",
		Effect: 4, // DETOUR
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &agency40,
				TripID: &gtfs.TripID{
					ID:      "", // empty trip_id
					RouteID: tripRouteFallback,
				},
			},
		},
		Header: []gtfs.AlertText{{Text: "Trip route fallback", Language: "en"}},
	})

	t.Run("prefixes routeId and stopId with agency and sets consequence condition", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_20770.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "40_20770", model.Data.Entry.ID)

		require.Len(t, model.Data.Entry.AllAffects, 1)
		affect := model.Data.Entry.AllAffects[0]
		assert.Equal(t, "40", affect.AgencyID)
		assert.Equal(t, "40_2LINE", affect.RouteID, "routeId must be prefixed with agencyId")
		assert.Equal(t, "40_20770", affect.StopID, "stopId must be prefixed with agencyId")

		require.Len(t, model.Data.Entry.Consequences, 1)
		assert.Equal(t, "", model.Data.Entry.Consequences[0].Condition)
		consequenceJSON, err := json.Marshal(model.Data.Entry.Consequences[0].ConditionDetails)
		require.NoError(t, err)
		assert.JSONEq(t, `{"diversionPath":{"length":0,"levels":"","points":""},"diversionStopIds":[]}`, string(consequenceJSON))

		server := httptest.NewServer(api.SetupAPIRoutes())
		defer server.Close()
		httpResp, err := http.Get(server.URL + "/api/where/situation/40_20770.json?key=TEST")
		require.NoError(t, err)
		body, err := io.ReadAll(httpResp.Body)
		require.NoError(t, err)
		_ = httpResp.Body.Close()
		assert.Contains(t, string(body), `"conditionDetails":{"diversionPath":{"length":0,"levels":"","points":""},"diversionStopIds":[]}`)
	})

	t.Run("resolves route agency from DB even when selector specifies a different agency", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_alert-route1.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "40_alert-route1", model.Data.Entry.ID)

		require.Len(t, model.Data.Entry.AllAffects, 1)
		affect := model.Data.Entry.AllAffects[0]
		assert.Equal(t, "40", affect.AgencyID, "agencyId is preserved from selector")
		assert.Equal(t, "25_151", affect.RouteID, "routeId must be prefixed with route agency from DB")

		require.Len(t, model.Data.Entry.Consequences, 1)
		assert.Equal(t, "", model.Data.Entry.Consequences[0].Condition)
	})

	t.Run("emits empty consequences when alert effect is unset or unknown_effect", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_alert-no-effect.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "40_alert-no-effect", model.Data.Entry.ID)

		assert.Empty(t, model.Data.Entry.Consequences, "consequences must be empty array when effect is unset")
	})

	t.Run("qualifies raw underscore IDs, preserves already-prefixed IDs, and ignores trip routeId when trip_id is present", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_alert-underscores.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)

		require.Len(t, model.Data.Entry.AllAffects, 3)
		assert.Equal(t, "25_r_under", model.Data.Entry.AllAffects[0].RouteID, "raw underscore route ID in DB should be prefixed with DB agency")
		assert.Equal(t, "25_151", model.Data.Entry.AllAffects[1].RouteID, "already prefixed route ID should be preserved")
		assert.Equal(t, "40_TRIP_1", model.Data.Entry.AllAffects[2].TripID)
		assert.Empty(t, model.Data.Entry.AllAffects[2].RouteID, "routeId must not be populated from trip.route_id when trip_id is present")
	})

	t.Run("emits consequence for NO_EFFECT matching Java", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_alert-no-effect-present.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "40_alert-no-effect-present", model.Data.Entry.ID)

		require.Len(t, model.Data.Entry.Consequences, 1)
		assert.Equal(t, "", model.Data.Entry.Consequences[0].Condition)
		consequenceJSON, err := json.Marshal(model.Data.Entry.Consequences[0].ConditionDetails)
		require.NoError(t, err)
		assert.JSONEq(t, `{"diversionPath":{"length":0,"levels":"","points":""},"diversionStopIds":[]}`, string(consequenceJSON))
	})

	t.Run("populates routeId from trip route_id when trip_id is empty", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_alert-trip-route-fallback.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "40_alert-trip-route-fallback", model.Data.Entry.ID)

		require.Len(t, model.Data.Entry.AllAffects, 1)
		affect := model.Data.Entry.AllAffects[0]
		assert.Equal(t, "40_TRIP_ROUTE_ONLY", affect.RouteID)
		assert.Empty(t, affect.TripID)
	})
}
