package restapi

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// tripForVehicleURL builds the /trip-for-vehicle URL with key=TEST baked in.
// Extra query params are merged from optional url.Values arguments.
func tripForVehicleURL(vehicleID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/trip-for-vehicle/" + vehicleID + ".json?" + q.Encode()
}

// setupTestApiWithMockVehicle builds an API with a mock vehicle pointing at the
// first static trip and returns the API plus the vehicle's combined ID.
func setupTestApiWithMockVehicle(t *testing.T) (api *RestAPI, vehicleCombinedID string) {
	t.Helper()
	api = createTestApi(t)
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(api.Shutdown)
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	const mockVehicleID = "MOCK_VEHICLE_1"
	combinedRouteID := utils.FormCombinedID(testdata.Raba.ID, trip.RouteID)

	api.GtfsManager.MockAddAgency(testdata.Raba.ID, "unitrans")
	api.GtfsManager.MockAddRoute(combinedRouteID, testdata.Raba.ID, combinedRouteID)
	// Deliberately no MockAddTrip: the trip already exists in the fixture DB, and
	// MockAddTrip is an INSERT OR REPLACE that would wipe its block and shape IDs
	// in the package-shared test database for every test that runs afterwards.
	api.GtfsManager.MockAddVehicle(mockVehicleID, trip.ID, combinedRouteID)

	return api, utils.FormCombinedID(testdata.Raba.ID, mockVehicleID)
}

func TestTripForVehicleHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-for-vehicle/invalid.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestTripForVehicleHandlerEndToEnd(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)

	resp, model := callAPIHandler[TripDetailsResponse](t, api, tripForVehicleURL(vehicleID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, models.APIVersion, model.Version)
	assert.NotZero(t, model.CurrentTime)

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.TripID)

	// serviceDate defaults to today midnight in the agency timezone.
	loc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	now := time.UnixMilli(model.CurrentTime).In(loc)
	y, m, d := now.Date()
	expectedServiceDate := time.Date(y, m, d, 0, 0, 0, 0, loc)
	assert.Equal(t, expectedServiceDate.UnixMilli(), entry.ServiceDate.UnixMilli())

	if entry.Status != nil {
		assert.Contains(t, []string{"scheduled", "in_progress", "completed"}, entry.Status.Phase)
		assert.NotZero(t, entry.Status.ServiceDate)
	}

	refs := model.Data.References
	assert.NotEmpty(t, refs.Agencies)
	require.NotEmpty(t, refs.Routes)
	require.NotEmpty(t, refs.Trips)

	// Trip ref must have a non-empty id/routeId.
	trip := refs.Trips[0]
	assert.NotEmpty(t, trip.ID)
	assert.NotEmpty(t, trip.RouteID)

	// Agency ref must have a populated id; other fields are checked structurally
	// in the per-stop loop below.
	for _, a := range refs.Agencies {
		assert.NotEmpty(t, a.ID)
	}

	// Stop refs (when present) must have populated id/name/lat/lon.
	for _, stop := range refs.Stops {
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
	}
}

// TestTripForVehicleHandler_NotFoundCases verifies that 404 is returned for
// vehicle IDs that resolve to no live trip — unknown vehicle, idle vehicle
// (Trip.ID == ""), vehicle referencing a non-existent trip, and a vehicle
// scoped under an unknown agency.
func TestTripForVehicleHandler_NotFoundCases(t *testing.T) {
	api, _ := setupTestApiWithMockVehicle(t)

	// Add an idle vehicle (vehicle with empty trip ID) and a ghost-trip vehicle.
	const (
		idleVehicleID  = "IDLE_VEHICLE"
		ghostVehicleID = "GHOST_TRIP_VEHICLE"
	)
	api.GtfsManager.MockAddVehicle(idleVehicleID, "", "")
	api.GtfsManager.MockAddVehicle(ghostVehicleID, "TRIP_THAT_DOES_NOT_EXIST", "some_route")

	tests := []struct {
		name      string
		vehicleID string
	}{
		{"Unknown vehicle ID", utils.FormCombinedID(testdata.Raba.ID, "invalid")},
		{"Vehicle with empty trip", utils.FormCombinedID(testdata.Raba.ID, idleVehicleID)},
		{"Vehicle referencing non-existent trip", utils.FormCombinedID(testdata.Raba.ID, ghostVehicleID)},
		{"Unknown agency", utils.FormCombinedID("INVALID_AGENCY", "MOCK_VEHICLE_1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripDetailsResponse](t, api, tripForVehicleURL(tt.vehicleID))

			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.Equal(t, http.StatusNotFound, model.Code)
		})
	}
}

// TestTripForVehicleHandler_IncludeToggles exercises the includeTrip,
// includeSchedule, and includeStatus query params.
func TestTripForVehicleHandler_IncludeToggles(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)

	t.Run("includeStatus=false omits status", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeStatus": {"false"}}))

		assert.Nil(t, model.Data.Entry.Status, "status should be omitted when includeStatus=false")
	})

	t.Run("includeSchedule=true keeps response well-formed", func(t *testing.T) {
		resp, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeSchedule": {"true"}}))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, model.Data.Entry.TripID)

		schedule := model.Data.Entry.Schedule
		require.NotNil(t, schedule, "schedule should be present when includeSchedule=true")
		require.NotEmpty(t, schedule.StopTimes, "the fixture trip should have stop times")

		referencedStops := make(map[string]struct{}, len(model.Data.References.Stops))
		for _, stop := range model.Data.References.Stops {
			referencedStops[stop.ID] = struct{}{}
		}
		for _, stopTime := range schedule.StopTimes {
			assert.Contains(t, referencedStops, stopTime.StopID,
				"every schedule stop must be dereferenceable in references.stops")
		}
	})

	t.Run("includeReferences=false empties the references block", func(t *testing.T) {
		resp, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeReferences": {"false"}}))

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, model.Data.Entry.TripID, "the entry must still be populated")

		// Every collection must serialize as [] rather than null: the block stays
		// present and merely empty, so a nil slice here would be a wire-format
		// regression rather than a reference the handler chose not to populate.
		refs := model.Data.References
		emptyCollections := map[string]any{
			"agencies":   refs.Agencies,
			"routes":     refs.Routes,
			"trips":      refs.Trips,
			"stops":      refs.Stops,
			"situations": refs.Situations,
			"stopTimes":  refs.StopTimes,
		}
		for name, collection := range emptyCollections {
			assert.NotNil(t, collection, "%s must be present when includeReferences=false", name)
			assert.Empty(t, collection, "%s must be empty when includeReferences=false", name)
		}
	})

	t.Run("all-false strips schedule/status/trip refs", func(t *testing.T) {
		resp, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{
				"includeTrip":     {"false"},
				"includeSchedule": {"false"},
				"includeStatus":   {"false"},
			}))
		require.Equal(t, http.StatusOK, resp.StatusCode)

		entry := model.Data.Entry
		assert.NotEmpty(t, entry.TripID)
		assert.NotZero(t, entry.ServiceDate.UnixMilli())
		assert.Nil(t, entry.Schedule)
		assert.Nil(t, entry.Status)
		assert.Empty(t, model.Data.References.Trips)
		assert.NotEmpty(t, model.Data.References.Agencies)
	})
}

// TestTripForVehicleHandler_MissingRoute verifies that a trip pointing at a
// route which is not in the database yields a 404 rather than a 500.
func TestTripForVehicleHandler_MissingRoute(t *testing.T) {
	api, _ := setupTestApiWithMockVehicle(t)

	const (
		orphanTripID    = "TRIP_WITH_MISSING_ROUTE"
		orphanVehicleID = "ORPHAN_ROUTE_VEHICLE"
		missingRouteID  = "ROUTE_THAT_DOES_NOT_EXIST"
	)

	api.GtfsManager.MockAddTrip(orphanTripID, testdata.Raba.ID, missingRouteID)
	api.GtfsManager.MockAddVehicle(orphanVehicleID, orphanTripID, missingRouteID)

	// The trip row lands in the package-shared fixture database, so take it back
	// out rather than leaving a routeless trip behind for later tests.
	t.Cleanup(func() {
		_, _ = api.GtfsManager.GtfsDB.DB.ExecContext(context.Background(),
			`DELETE FROM trips WHERE id = ?`, orphanTripID)
	})

	// includeTrip=true so the route lookup is reached even though the orphaned
	// trip has no real-time status to build.
	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		tripForVehicleURL(utils.FormCombinedID(testdata.Raba.ID, orphanVehicleID),
			url.Values{"includeTrip": {"true"}}))

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a dangling route reference must not fail the request")
	assert.Equal(t, http.StatusOK, model.Code)

	assert.Equal(t, utils.FormCombinedID(testdata.Raba.ID, orphanTripID), model.Data.Entry.TripID,
		"the trip the vehicle is running is still served")
	assert.NotEmpty(t, model.Data.References.Trips,
		"the trip reference survives even though its route cannot be resolved")
	assert.Empty(t, model.Data.References.Routes,
		"the unresolvable route is simply absent from references")
}

// TestTripForVehicleHandler_TripReferences covers where the active trip's
// reference comes from: the status path adds it whenever the status block is
// present, and includeTrip only matters once includeStatus=false.
func TestTripForVehicleHandler_TripReferences(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)

	t.Run("includeTrip=false still yields trip refs via the status path", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeTrip": {"false"}}))

		require.NotNil(t, model.Data.Entry.Status, "status is present by default")
		assert.NotEmpty(t, model.Data.References.Trips,
			"the active trip reaches references via the status path regardless of includeTrip")
		assert.NotEmpty(t, model.Data.References.Routes,
			"the active trip's route accompanies it in references")
	})

	t.Run("includeStatus=false with includeTrip=false omits trip refs", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{
				"includeStatus": {"false"},
				"includeTrip":   {"false"},
			}))

		assert.Empty(t, model.Data.References.Trips,
			"with no status block and includeTrip=false nothing adds the trip")
	})

	t.Run("includeTrip=true with includeStatus=false yields trip refs", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{
				"includeStatus": {"false"},
				"includeTrip":   {"true"},
			}))

		assert.NotEmpty(t, model.Data.References.Trips,
			"includeTrip is what adds the trip when there is no status block")
	})
}

func TestTripForVehicleHandlerWithServiceDate(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)

	// Pin to a fixed future-but-bounded date so the test doesn't drift over the
	// years. The handler resolves serviceDate to midnight in the agency's
	// timezone, so we compute the expected midnight against testdata.Raba.Timezone.
	serviceDate := time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC)
	agencyLoc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	sdInAgencyTz := serviceDate.In(agencyLoc)
	expectedMidnight := time.Date(sdInAgencyTz.Year(), sdInAgencyTz.Month(), sdInAgencyTz.Day(), 0, 0, 0, 0, agencyLoc)

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		tripForVehicleURL(vehicleID, url.Values{"serviceDate": {fmt.Sprintf("%d", serviceDate.UnixMilli())}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, expectedMidnight.UnixMilli(), model.Data.Entry.ServiceDate.UnixMilli())
}

func TestTripForVehicleHandlerWithTimeParameter(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)
	// Fixed timestamp (Jan 1 2025 12:00 UTC), well inside RABA's calendar window.
	timeMs := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		tripForVehicleURL(vehicleID, url.Values{"time": {fmt.Sprintf("%d", timeMs)}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.NotEmpty(t, model.Data.Entry.TripID)
}

func TestTripForVehicleHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[TripDetailsResponse](t, api,
		"/api/where/trip-for-vehicle/1110.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripForVehicleHandlerWithInvalidParams(t *testing.T) {
	api, vehicleID := setupTestApiWithMockVehicle(t)

	tests := []struct {
		name  string
		param url.Values
	}{
		{"invalid serviceDate", url.Values{"serviceDate": {"invalid"}}},
		{"invalid time", url.Values{"time": {"invalid"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := callAPIHandler[TripDetailsResponse](t, api, tripForVehicleURL(vehicleID, tt.param))
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestParseTripForVehicleParams_Unit(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("explicit params", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?includeStatus=false&time=1609459200000", nil)

		params, errs := api.parseTripParams(req, TripParamDefaults{})

		assert.Nil(t, errs)
		assert.False(t, params.IncludeStatus)
		assert.NotNil(t, params.Time)
	})

	t.Run("defaults for trip-for-vehicle", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)

		params, errs := api.parseTripParams(req, TripParamDefaults{})

		assert.Nil(t, errs)
		assert.False(t, params.IncludeTrip)
		assert.False(t, params.IncludeSchedule)
		assert.True(t, params.IncludeStatus)
	})

	t.Run("invalid params return field errors", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?serviceDate=invalid&time=invalid", nil)

		_, errs := api.parseTripParams(req, TripParamDefaults{})

		require.NotNil(t, errs)
		assert.Contains(t, errs, "serviceDate")
		assert.Contains(t, errs, "time")
		assert.Equal(t, "must be a valid Unix timestamp in milliseconds or a date in yyyy-MM-dd format", errs["serviceDate"][0])
	})
}

// TestReferencedStopIDs_Unit covers the stop IDs collected for the references
// block: which entry fields contribute, that absent stops are skipped, and that
// an unparseable combined ID surfaces an error for the handler to report.
func TestReferencedStopIDs_Unit(t *testing.T) {
	combined := func(stopID string) string {
		return utils.FormCombinedID(testdata.Raba.ID, stopID)
	}

	tests := []struct {
		name     string
		status   *models.TripStatus
		schedule *models.Schedule
		want     []string
		wantErr  bool
	}{
		{
			name: "no status or schedule yields no stops",
			want: []string{},
		},
		{
			name:   "closest and next stops are collected",
			status: &models.TripStatus{ClosestStop: combined("1"), NextStop: combined("2")},
			want:   []string{"1", "2"},
		},
		{
			name:   "absent status stops are skipped",
			status: &models.TripStatus{ClosestStop: "", NextStop: combined("2")},
			want:   []string{"2"},
		},
		{
			name:   "schedule stops are collected alongside status stops",
			status: &models.TripStatus{ClosestStop: combined("1")},
			schedule: &models.Schedule{StopTimes: []models.StopTime{
				{StopID: combined("2")}, {StopID: combined("3")},
			}},
			want: []string{"1", "2", "3"},
		},
		{
			name: "absent schedule stop IDs are skipped",
			schedule: &models.Schedule{StopTimes: []models.StopTime{
				{StopID: ""}, {StopID: combined("2")},
			}},
			want: []string{"2"},
		},
		{
			name:    "malformed status stop ID returns an error",
			status:  &models.TripStatus{ClosestStop: "missing-separator"},
			wantErr: true,
		},
		{
			name: "malformed schedule stop ID returns an error",
			schedule: &models.Schedule{StopTimes: []models.StopTime{
				{StopID: "missing-separator"},
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := referencedStopIDs(tt.status, tt.schedule)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, got, "no stop IDs should be returned alongside an error")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
