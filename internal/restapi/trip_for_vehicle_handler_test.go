package restapi

import (
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
	api.GtfsManager.MockAddTrip(trip.ID, testdata.Raba.ID, combinedRouteID)
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

	// Trip ref must have a non-empty id/routeId. ServiceID is intentionally not
	// asserted: the mock trip injected by setupTestApiWithMockVehicle has no
	// ServiceID, and asserting on it would be asserting on the mock helper.
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

	t.Run("includeTrip=false omits trip references", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeTrip": {"false"}}))

		assert.Empty(t, model.Data.References.Trips, "trip refs should be empty when includeTrip=false")
	})

	t.Run("includeSchedule=true keeps response well-formed", func(t *testing.T) {
		resp, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{"includeSchedule": {"true"}}))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, model.Data.Entry.TripID)
	})

	t.Run("all-false strips schedule/status/trip refs", func(t *testing.T) {
		_, model := callAPIHandler[TripDetailsResponse](t, api,
			tripForVehicleURL(vehicleID, url.Values{
				"includeTrip":     {"false"},
				"includeSchedule": {"false"},
				"includeStatus":   {"false"},
			}))

		entry := model.Data.Entry
		assert.NotEmpty(t, entry.TripID)
		assert.NotZero(t, entry.ServiceDate.UnixMilli())
		assert.Nil(t, entry.Schedule)
		assert.Nil(t, entry.Status)
		assert.Empty(t, model.Data.References.Trips)
		assert.NotEmpty(t, model.Data.References.Agencies)
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

		params, errs := api.parseTripParams(req, false)

		assert.Nil(t, errs)
		assert.False(t, params.IncludeStatus)
		assert.NotNil(t, params.Time)
	})

	t.Run("defaults for trip-for-vehicle", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)

		params, errs := api.parseTripParams(req, false)

		assert.Nil(t, errs)
		assert.True(t, params.IncludeTrip)
		assert.False(t, params.IncludeSchedule)
		assert.True(t, params.IncludeStatus)
	})

	t.Run("invalid params return field errors", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/?serviceDate=invalid&time=invalid", nil)

		_, errs := api.parseTripParams(req, false)

		require.NotNil(t, errs)
		assert.Contains(t, errs, "serviceDate")
		assert.Contains(t, errs, "time")
		assert.Equal(t, "must be a valid Unix timestamp in milliseconds", errs["serviceDate"][0])
	})
}
