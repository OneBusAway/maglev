package restapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// stopURL builds the /stop endpoint URL with key=TEST baked in. Tests that
// want a different key (auth checks) build their URL inline.
func stopURL(stopID string) string {
	return "/api/where/stop/" + stopID + ".json?key=TEST"
}

func TestStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopEntryResponse](t, api,
		"/api/where/stop/"+testdata.Stop4062.ID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopEntryResponse](t, api, stopURL(testdata.Stop4062.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.Equal(t, testdata.Stop4062, model.Data.Entry)

	// References should include exactly the routes named in entry.RouteIDs and
	// the single agency that owns the stop.
	require.Len(t, model.Data.References.Routes, len(testdata.Stop4062.RouteIDs),
		"references.routes count should match entry.routeIds count")
	assert.Equal(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
}

func TestStopHandler_NotFoundAndMalformed(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name           string
		stopID         string
		expectedStatus int
		expectedText   string
	}{
		{
			"Unknown stop",
			utils.FormCombinedID(testdata.Raba.ID, "invalid_stop_id"),
			http.StatusNotFound,
			"resource not found",
		},
		{
			"Malformed (no agency separator)",
			"1110",
			http.StatusBadRequest,
			"", // bad-request text varies; just check code
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[StopEntryResponse](t, api, stopURL(tt.stopID))

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)
			if tt.expectedText != "" {
				assert.Equal(t, tt.expectedText, model.Text)
			}
		})
	}
}

// TestStopHandlerMultiAgencyScenario verifies that a stop shared between two
// agencies returns routeIds prefixed by each route's own agency (not the stop's
// agency) and includes both agencies in references.
func TestStopHandlerMultiAgencyScenario(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	const (
		agencyA = "AgencyA"
		agencyB = "AgencyB"
		stopID  = "Stop1"
		routeA  = "RouteA"
		routeB  = "RouteB"
		tripA   = "TripA"
		tripB   = "TripB"
		service = "service1"
	)

	// Two agencies + one shared stop.
	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyA, Name: "Transit Agency A", Url: "http://agency-a.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	_, err = q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyB, Name: "Transit Agency B", Url: "http://agency-b.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopID, Name: nulls.String("Shared Transit Center"),
		Lat: 47.6062, Lon: -122.3321,
	})
	require.NoError(t, err)

	// One route per agency, both serving the shared stop.
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeA, AgencyID: agencyA, ShortName: nulls.String("A-Line"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeB, AgencyID: agencyB, ShortName: nulls.String("B-Line"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: service, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)
	for _, t2 := range []struct {
		tripID, routeID string
		arrivalSec      int64
	}{
		{tripA, routeA, 32400}, // 09:00:00 — owned by AgencyA
		{tripB, routeB, 28800}, // 08:00:00 — owned by AgencyB
	} {
		_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
			ID: t2.tripID, RouteID: t2.routeID, ServiceID: service,
		})
		require.NoError(t, err)
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: t2.tripID, StopID: stopID, StopSequence: 1,
			ArrivalTime: t2.arrivalSec, DepartureTime: t2.arrivalSec + 300,
		})
		require.NoError(t, err)
	}

	resp, model := callAPIHandler[StopEntryResponse](t, api,
		stopURL(utils.FormCombinedID(agencyA, stopID)))

	require.Equal(t, http.StatusOK, resp.StatusCode)

	// routeIds use each route's own agency prefix, not the requesting stop's
	// agency. This is the regression case.
	assert.Contains(t, model.Data.Entry.RouteIDs, utils.FormCombinedID(agencyA, routeA),
		"Route A's id must be prefixed with AgencyA")
	assert.Contains(t, model.Data.Entry.RouteIDs, utils.FormCombinedID(agencyB, routeB),
		"Route B's id must be prefixed with AgencyB")

	// Both agencies must show up in references.
	agencyIDs := make(map[string]bool)
	for _, a := range model.Data.References.Agencies {
		agencyIDs[a.ID] = true
	}
	assert.True(t, agencyIDs[agencyA], "AgencyA should be in references")
	assert.True(t, agencyIDs[agencyB], "AgencyB should be in references")
}

// TestStopHandlerWithSituations verifies that an alert informing the same
// situation against multiple entities (stop + route) deduplicates to one
// situation in references.
func TestStopHandlerWithSituations(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Real-time alerts use raw (un-prefixed) ids from the GTFS-RT feed.
	rawStopID := "4062" // Stop4062 = "25_4062"
	rawRouteID := "154" // Stop4062 is on route 25_154
	const alertID = "test-cross-entity-alert-789"
	api.GtfsManager.AddAlertForTest(gtfs.Alert{
		ID: alertID,
		InformedEntities: []gtfs.AlertInformedEntity{
			{StopID: &rawStopID},
			{RouteID: &rawRouteID},
		},
	})

	resp, model := callAPIHandler[StopEntryResponse](t, api, stopURL(testdata.Stop4062.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.References.Situations, 1,
		"expected exactly one deduplicated situation despite matching multiple entities")
	assert.Equal(t, alertID, model.Data.References.Situations[0].ID)
}
