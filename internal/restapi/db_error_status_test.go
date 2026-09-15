package restapi

import (
	"io"
	"log/slog"
	"net/http"
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
	api.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return api
}

// CONTRIBUTING.md asks handlers to distinguish "not found" from "the query
// itself failed", so a database failure must not be reported to the client as a
// missing entity.
//
// tripHandler is not covered here: its GetTrip lookup runs first and still maps
// every error to 404, so a closed database never reaches the agency lookup this
// change fixes. That blanket 404 is shared with trip_details_handler.go and is
// left for a separate change.
func TestDatabaseFailureIsNotReportedAsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "shapes", endpoint: shapeURL("agency_shape")},
		{name: "schedule-for-route", endpoint: scheduleForRouteURL("agency_route", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[models.ResponseModel](t, apiWithClosedDB(t), tt.endpoint)

			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			assert.Equal(t, http.StatusInternalServerError, model.Code)
		})
	}
}
