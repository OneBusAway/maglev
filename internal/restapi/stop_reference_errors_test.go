package restapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

type routeIDsForStopsFailingDB struct {
	gtfsdb.DBTX
	failures int
}

func (db *routeIDsForStopsFailingDB) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	if strings.Contains(query, "-- name: GetRouteIDsForStops") {
		db.failures++
		return nil, errors.New("injected GetRouteIDsForStops failure")
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

func TestStopRouteLookupErrorsReturnSingleErrorResponse(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T) (*RestAPI, string)
	}{
		{
			name: "stops-for-location",
			setup: func(t *testing.T) (*RestAPI, string) {
				api := createTestApiWithFeedAndClock(
					t,
					models.GetFixturePath(t, "raba.zip"),
					clock.NewMockClock(time.Date(2025, 6, 13, 14, 0, 0, 0, time.UTC)),
				)
				_, err := api.GtfsManager.GtfsDB.DB.ExecContext(
					t.Context(),
					"UPDATE stops SET parent_station = ? WHERE id = ?",
					"4062",
					"2042",
				)
				require.NoError(t, err)
				return api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042"
			},
		},
		{
			name: "trips-for-location",
			setup: func(t *testing.T) (*RestAPI, string) {
				api := createTestApiWithFeedAndClock(
					t,
					models.GetFixturePath(t, "raba.zip"),
					clock.NewMockClock(tripsForLocationServiceDayClock),
				)
				return api, tripsForLocationURL(2.0, 3.0, "includeSchedule=true")
			},
		},
		{
			name: "trips-for-route",
			setup: func(t *testing.T) (*RestAPI, string) {
				api := createTestApiWithGTFSFixture(
					t,
					clock.NewMockClock(tripsForRouteTestClock),
					"route-reference-errors.zip",
					basicTripsForRouteFiles(),
				)
				endpoint := fmt.Sprintf(
					"/api/where/trips-for-route/%s_%s.json?key=TEST&includeSchedule=true&time=%d",
					tripsForRouteAgencyID,
					tripsForRouteRouteID,
					tripsForRouteTestClock.UnixMilli(),
				)
				return api, endpoint
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, endpoint := tt.setup(t)

			baseline, _ := serveRouteReferenceEndpoint(t, api, endpoint)
			require.Equal(t, http.StatusOK, baseline.Code, "the fixture must reach stop-reference construction")

			failingDB := &routeIDsForStopsFailingDB{DBTX: api.GtfsManager.GtfsDB.DB}
			api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)

			recorder, body := serveRouteReferenceEndpoint(t, api, endpoint)
			require.Equal(t, 1, failingDB.failures, "GetRouteIDsForStops should fail exactly once")
			require.Equal(t, http.StatusInternalServerError, recorder.Code)

			var response struct {
				Code int    `json:"code"`
				Text string `json:"text"`
			}
			require.NoErrorf(t, json.Unmarshal(body, &response),
				"handler must write exactly one JSON document; body=%s", body)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.Equal(t, "internal server error", response.Text)
		})
	}
}

func serveRouteReferenceEndpoint(t *testing.T, api *RestAPI, endpoint string) (*httptest.ResponseRecorder, []byte) {
	t.Helper()

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))
	return recorder, recorder.Body.Bytes()
}
