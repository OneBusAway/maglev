package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

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
	assert.Empty(t, model.Data.References.Trips, "trips should always be empty for this endpoint")
	assert.Empty(t, model.Data.References.StopTimes, "stopTimes should always be empty for this endpoint")
	assert.Empty(t, model.Data.References.Situations, "situations should always be empty for this endpoint")
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

// TestStopHandler_StopCodeFallback verifies that when a stop has no stop_code
// in the database (Code is a null NullString), the response falls back to
// returning the raw entity portion of the combined ID as the code field.
func TestStopHandler_StopCodeFallback(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID = "FallbackAgency"
		stopID   = "StopNoCode"
		routeID  = "FallbackRoute"
		tripID   = "FallbackTrip"
		service  = "FallbackService"
	)

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Fallback Transit", Url: "http://fallback.example.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	// Create stop with NO Code set — leave Code as zero-value sql.NullString (Valid=false)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:  stopID,
		Lat: 37.7749,
		Lon: -122.4194,
		// Code intentionally omitted (zero value = null)
	})
	require.NoError(t, err)

	// Need a route + trip + stop_time so GetRoutesForStop returns something
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, ShortName: nulls.String("FB"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: service, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: service,
	})
	require.NoError(t, err)
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripID, StopID: stopID, StopSequence: 1,
		ArrivalTime: 32400, DepartureTime: 32700,
	})
	require.NoError(t, err)

	resp, model := callAPIHandler[StopEntryResponse](t, api,
		stopURL(utils.FormCombinedID(agencyID, stopID)))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	// The code field must fall back to the raw stopID (the entity portion of the
	// combined ID), NOT the full combined ID like "FallbackAgency_StopNoCode".
	assert.Equal(t, stopID, model.Data.Entry.Code)

	// Additional assertions for defaults and empty fields
	assert.Equal(t, "", model.Data.Entry.Direction, "direction should default to empty string when absent")
	assert.Equal(t, 0, model.Data.Entry.LocationType, "locationType should default to 0 when absent")
	assert.Empty(t, model.Data.References.Stops, "references.stops should be empty when there is no parent station")
	require.NotEmpty(t, model.Data.Entry.RouteIDs, "routeIds should contain seeded route")
	assert.Contains(t, model.Data.Entry.RouteIDs, utils.FormCombinedID(agencyID, routeID))
	assert.Equal(t, model.Data.Entry.RouteIDs, model.Data.Entry.StaticRouteIDs, "staticRouteIds should inherit from routeIds when no static list is provided")
}

// TestStopHandler_ParentStation verifies that when a stop has a parent_station
// set, the handler:
//  1. Sets entry.parent to FormCombinedID(agencyID, parentStopID)
//  2. Includes the parent stop in references.stops
//  3. Includes the parent stop's routes and agencies in the references block
func TestStopHandler_ParentStation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	const (
		agencyID          = "ParentStationAgency"
		parentStopID      = "StationParent"
		childStopID       = "StationChild"
		routeID           = "ParentStationRoute"
		tripID            = "ParentStationTrip"
		service           = "ParentStationService"
		parentOnlyRouteID = "ParentOnlyRoute"
		parentOnlyTripID  = "ParentOnlyTrip"
	)

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Parent Station Transit", Url: "http://pst.example.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	// Parent stop — locationType=1 (station)
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:           parentStopID,
		Name:         nulls.String("Central Station"),
		Lat:          47.6062,
		Lon:          -122.3321,
		LocationType: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Child stop pointing at the parent
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID:            childStopID,
		Name:          nulls.String("Platform A"),
		Lat:           47.6063,
		Lon:           -122.3322,
		ParentStation: nulls.String(parentStopID),
	})
	require.NoError(t, err)

	// Route + trip + stop_time linking the CHILD stop
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: routeID, AgencyID: agencyID, ShortName: nulls.String("PS"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: service, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: tripID, RouteID: routeID, ServiceID: service,
	})
	require.NoError(t, err)
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: tripID, StopID: childStopID, StopSequence: 1,
		ArrivalTime: 36000, DepartureTime: 36300,
	})
	require.NoError(t, err)

	// Route + trip + stop_time linking exclusively to the PARENT stop
	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: parentOnlyRouteID, AgencyID: agencyID, ShortName: nulls.String("ParentOnly"), Type: 3,
	})
	require.NoError(t, err)
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: parentOnlyTripID, RouteID: parentOnlyRouteID, ServiceID: service,
	})
	require.NoError(t, err)
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: parentOnlyTripID, StopID: parentStopID, StopSequence: 1,
		ArrivalTime: 37000, DepartureTime: 37300,
	})
	require.NoError(t, err)

	// Seed the shared routeID (already on child stop) onto the PARENT stop as well to test deduplication
	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: "SharedRouteParentTrip", RouteID: routeID, ServiceID: service,
	})
	require.NoError(t, err)
	_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
		TripID: "SharedRouteParentTrip", StopID: parentStopID, StopSequence: 1,
		ArrivalTime: 38000, DepartureTime: 38300,
	})
	require.NoError(t, err)

	resp, model := callAPIHandler[StopEntryResponse](t, api,
		stopURL(utils.FormCombinedID(agencyID, childStopID)))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	// entry.parent must be the combined ID of the parent stop
	expectedParentCombinedID := utils.FormCombinedID(agencyID, parentStopID)
	assert.Equal(t, expectedParentCombinedID, model.Data.Entry.Parent)

	// The parent stop must appear exactly once in references.stops
	require.Len(t, model.Data.References.Stops, 1, "expected exactly one stop in references")
	assert.Equal(t, expectedParentCombinedID, model.Data.References.Stops[0].ID)

	assert.Equal(t, "", model.Data.References.Stops[0].Parent)

	// entry.id must be the child stop, not the parent
	assert.Equal(t, utils.FormCombinedID(agencyID, childStopID), model.Data.Entry.ID)

	// Verify non-default locationType on the parent reference
	assert.Equal(t, 1, model.Data.References.Stops[0].LocationType, "parent stop should correctly retain locationType=1")

	// 1. Assert the parent station's unique route is present in references.routes
	routeFound := false
	expectedParentRouteCombinedID := utils.FormCombinedID(agencyID, parentOnlyRouteID)
	for _, r := range model.Data.References.Routes {
		if r.ID == expectedParentRouteCombinedID {
			routeFound = true
			break
		}
	}
	assert.True(t, routeFound, "parent station route must be included in references.routes")

	// 2. Assert the agency for the parent station's route is present in references.agencies
	agencyFound := false
	for _, a := range model.Data.References.Agencies {
		if a.ID == agencyID {
			agencyFound = true
			break
		}
	}
	assert.True(t, agencyFound, "agency for the parent station route must be included in references.agencies")

	// 3. Assert the shared route (seeded on both child and parent) appears exactly once in references.routes
	sharedRouteCount := 0
	expectedSharedRouteCombinedID := utils.FormCombinedID(agencyID, routeID)
	for _, r := range model.Data.References.Routes {
		if r.ID == expectedSharedRouteCombinedID {
			sharedRouteCount++
		}
	}
	assert.Equal(t, 1, sharedRouteCount, "shared route must appear exactly once due to deduplication")
}

func TestStopHandler_NaturalSorting(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	agencyID := "SortAgency"
	stopID := "SortStop1"

	// Create Agency and Stop
	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Sort Transit", Url: "http://sort.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: stopID, Name: nulls.String("Sorted Stop"), Lat: 47.6, Lon: -122.3,
	})
	require.NoError(t, err)

	// Create Routes intentionally out of natural order
	// We want to verify "2" < "14" < "101" < "B" < "Fallback"
	routeNames := []string{"101", "B", "14", "2", "Fallback"}

	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "serv1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1, StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)

	for i, name := range routeNames {
		routeID := "Route" + name
		tripID := "Trip" + name

		shortName := nulls.String(name)
		longName := nulls.String("")
		if name == "Fallback" {
			shortName = nulls.String("")
			longName = nulls.String(name)
		}

		_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID: routeID, AgencyID: agencyID, ShortName: shortName, LongName: longName, Type: 3,
		})
		require.NoError(t, err)

		_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
			ID: tripID, RouteID: routeID, ServiceID: "serv1",
		})
		require.NoError(t, err)

		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: stopID, StopSequence: int64(i + 1), ArrivalTime: 30000, DepartureTime: 30000,
		})
		require.NoError(t, err)
	}

	// Call Endpoint
	resp, model := callAPIHandler[StopEntryResponse](t, api, stopURL(utils.FormCombinedID(agencyID, stopID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Also assert that model.Data.Entry.RouteIDs matches the same order exactly
	expectedRouteIDs := []string{
		utils.FormCombinedID(agencyID, "Route2"),
		utils.FormCombinedID(agencyID, "Route14"),
		utils.FormCombinedID(agencyID, "Route101"),
		utils.FormCombinedID(agencyID, "RouteB"),
		utils.FormCombinedID(agencyID, "RouteFallback"),
	}
	assert.Equal(t, expectedRouteIDs, model.Data.Entry.RouteIDs, "Entry.RouteIDs should preserve natural order")
}

func TestStopHandler_ParentStationNaturalSorting(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	agencyID := "SortAgency"
	parentStopID := "ParentStop"
	childStopID := "ChildStop"

	// Create Agency
	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agencyID, Name: "Sort Transit", Url: "http://sort.com", Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	// Create Parent Stop
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: parentStopID, Name: nulls.String("Parent Stop"), Lat: 47.6, Lon: -122.3, LocationType: nulls.Int64(1),
	})
	require.NoError(t, err)

	// Create Child Stop pointing to Parent Stop
	_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
		ID: childStopID, Name: nulls.String("Child Stop"), Lat: 47.6, Lon: -122.3, LocationType: nulls.Int64(0), ParentStation: nulls.String(parentStopID),
	})
	require.NoError(t, err)

	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID: "serv1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1, StartDate: "20250101", EndDate: "20251231",
	})
	require.NoError(t, err)

	// Create Routes for the PARENT stop intentionally out of natural order
	routeNames := []string{"101", "B", "14", "2"}
	for i, name := range routeNames {
		routeID := "Route" + name
		tripID := "Trip" + name

		_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID: routeID, AgencyID: agencyID, ShortName: nulls.String(name), Type: 3,
		})
		require.NoError(t, err)

		_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
			ID: tripID, RouteID: routeID, ServiceID: "serv1",
		})
		require.NoError(t, err)

		// Link routes to the PARENT stop
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: parentStopID, StopSequence: int64(i + 1), ArrivalTime: 30000, DepartureTime: 30000,
		})
		require.NoError(t, err)
	}

	// Call Endpoint for the CHILD stop
	resp, model := callAPIHandler[StopEntryResponse](t, api, stopURL(utils.FormCombinedID(agencyID, childStopID)))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Find the parent stop in references.stops
	require.Len(t, model.Data.References.Stops, 1, "Should include exactly one parent stop in references")
	parentRef := model.Data.References.Stops[0]
	assert.Equal(t, utils.FormCombinedID(agencyID, parentStopID), parentRef.ID)

	// Assert that the parent station's RouteIDs are naturally sorted ("2" < "14" < "101" < "B")
	expectedRouteIDs := []string{
		utils.FormCombinedID(agencyID, "Route2"),
		utils.FormCombinedID(agencyID, "Route14"),
		utils.FormCombinedID(agencyID, "Route101"),
		utils.FormCombinedID(agencyID, "RouteB"),
	}
	assert.Equal(t, expectedRouteIDs, parentRef.RouteIDs, "Parent station RouteIDs should preserve natural order")
}

func TestStopHandler_IncludeReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Verify standard behavior (includeReferences=true implicitly)
	respTrue, modelTrue := callAPIHandler[StopEntryResponse](t, api, stopURL(testdata.Stop4062.ID))
	require.Equal(t, http.StatusOK, respTrue.StatusCode)
	assert.Equal(t, testdata.Stop4062, modelTrue.Data.Entry)
	// Verify references are populated
	assert.NotEmpty(t, modelTrue.Data.References.Agencies, "Agencies should be populated when includeReferences is not false")
	assert.NotEmpty(t, modelTrue.Data.References.Routes, "Routes should be populated when includeReferences is not false")

	// Verify explicit explicit includeReferences=false behavior
	respFalse, modelFalse := callAPIHandler[StopEntryResponse](t, api, stopURL(testdata.Stop4062.ID)+"&includeReferences=false")
	require.Equal(t, http.StatusOK, respFalse.StatusCode)

	// Ensure the core entry data is unaffected
	assert.Equal(t, testdata.Stop4062, modelFalse.Data.Entry)

	// Verify the references object is present but all arrays are empty (as per spec)
	assert.Empty(t, modelFalse.Data.References.Agencies, "Agencies must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Routes, "Routes must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Stops, "Stops must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.StopTimes, "StopTimes must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Trips, "Trips must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Situations, "Situations must be empty when includeReferences=false")
}
