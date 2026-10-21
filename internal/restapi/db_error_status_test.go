package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// apiWithClosedDB builds an API over its own in-memory database and then closes
// it, so every query fails with something that is not sql.ErrNoRows. It
// deliberately does not use createTestApi, whose GTFS manager is shared across
// the whole package and must not be closed.
func apiWithClosedDB(t *testing.T) *RestAPI {
	t.Helper()
	manager := newTestManagerNoData(t)
	manager.MarkReady()
	require.NoError(t, manager.GtfsDB.Close())

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}
	api := NewRestAPI(application)
	return api
}

// CONTRIBUTING.md asks handlers to distinguish "not found" from "the query
// itself failed", so a database failure must not be reported to the client as a
// missing entity.
func TestDatabaseFailureIsNotReportedAsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "shapes", endpoint: shapeURL("agency_shape")},
		{name: "schedule-for-route", endpoint: scheduleForRouteURL("agency_route", "")},
		{name: "trip", endpoint: tripURL("agency_trip")},
		{name: "trip-details", endpoint: "/api/where/trip-details/agency_trip.json?key=TEST"},
		{name: "stop", endpoint: stopURL("agency_stop")},
		{name: "schedule-for-stop", endpoint: "/api/where/schedule-for-stop/agency_stop.json?key=TEST"},
		{name: "stops-for-route", endpoint: "/api/where/stops-for-route/agency_route.json?key=TEST"},
		{name: "arrivals-and-departures-for-stop", endpoint: "/api/where/arrivals-and-departures-for-stop/agency_stop.json?key=TEST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[models.ResponseModel](t, apiWithClosedDB(t), tt.endpoint)

			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			assert.Equal(t, http.StatusInternalServerError, model.Code)
		})
	}
}

// Subsequent lookups that follow an initial successful check (e.g., GetAgency or
// BlockHasTripForAgency) must also report database failures as 500 rather than
// 404. We drop the downstream table on a RABA fixture so the initial lookup
// succeeds and only the second lookup encounters the database failure.
func TestSubsequentDatabaseFailureIsNotReportedAsNotFound(t *testing.T) {
	tests := []struct {
		name      string
		dropTable string
		endpoint  string
	}{
		{
			name:      "stops-for-route",
			dropTable: "routes",
			endpoint:  "/api/where/stops-for-route/25_151.json?key=TEST",
		},
		{
			name:      "schedule-for-stop",
			dropTable: "stops",
			endpoint:  "/api/where/schedule-for-stop/25_4062.json?key=TEST",
		},
		{
			name:      "block",
			dropTable: "stop_times",
			endpoint:  blockURL("25_1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithFeed(t, models.GetFixturePath(t, "raba.zip"))
			_, err := api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF; DROP TABLE "+tt.dropTable)
			require.NoError(t, err)

			resp, model := callAPIHandler[models.ResponseModel](t, api, tt.endpoint)

			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			assert.Equal(t, http.StatusInternalServerError, model.Code)
		})
	}
}

func TestLocationSpatialQueryFailure(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "raba.zip"))
	api.Clock = clock.NewMockClock(arrivalsTestClock)

	req := httptest.NewRequest(http.MethodGet, arrivalsForLocationURL(arrivalsForLocationCenter), nil)
	params, fieldErrors := api.parseArrivalsForLocationParams(req)
	require.Empty(t, fieldErrors)
	stops, err := api.GtfsManager.GetStopsInBounds(t.Context(), params.Location, 0, true)
	require.NoError(t, err)
	require.NotEmpty(t, stops)
	agencies, err := api.agenciesForStops(t.Context(), stops)
	require.NoError(t, err)

	// Break only spatial queries, leaving agency and other lookups operational.
	// This fixture has its own database; the shared RABA database stays intact.
	_, err = api.GtfsManager.GtfsDB.DB.ExecContext(t.Context(), "DROP TABLE stops_rtree")
	require.NoError(t, err)

	tests := []struct {
		name     string
		endpoint string
	}{
		{
			name:     "arrivals-and-departures-for-location",
			endpoint: arrivalsForLocationURL(arrivalsForLocationCenter),
		},
		{
			name:     "arrivals-and-departures-for-location with emptyReturnsNotFound",
			endpoint: arrivalsForLocationURL(arrivalsForLocationCenter) + "&emptyReturnsNotFound=true",
		},
		{
			name:     "trips-for-location",
			endpoint: tripsForLocationURL(0.1, 0.1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[models.ResponseModel](t, api, tt.endpoint)
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			assert.Equal(t, http.StatusInternalServerError, model.Code)
			assert.Equal(t, "internal server error", model.Text)
		})
	}

	t.Run("nearby stops", func(t *testing.T) {
		nearby, err := api.nearbyStopsForLocation(t.Context(), stops, agencies, params)
		require.ErrorContains(t, err, "stops_rtree")
		assert.Nil(t, nearby)
	})
}
