package restapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// tripsForLocationReferenceFailureDB fails a selected generated query after
// the requested number of matching calls. This lets the test target reference
// construction without failing the handler's earlier trip lookup.
type tripsForLocationReferenceFailureDB struct {
	gtfsdb.DBTX
	queryMarker string
	failOnCall  int
	calls       int
	failures    int
	failWith    error
}

func (db *tripsForLocationReferenceFailureDB) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	if strings.Contains(query, db.queryMarker) {
		db.calls++
		if db.calls == db.failOnCall {
			db.failures++
			return nil, db.failWith
		}
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

func TestTripsForLocationHandler_ReferenceBuildErrorsReturnSingleErrorResponse(t *testing.T) {
	tests := []struct {
		name       string
		queryMatch string
		failOnCall int
	}{
		{
			name:       "trip enrichment lookup",
			queryMatch: "-- name: GetTripsByIDs",
			// The handler fetches visible trips first; reference enrichment is
			// the next GetTripsByIDs call.
			failOnCall: 2,
		},
		{
			name:       "agency reference lookup",
			queryMatch: "-- name: GetAgenciesByIDs",
			failOnCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithFeedAndClock(
				t,
				models.GetFixturePath(t, "raba.zip"),
				clock.NewMockClock(tripsForLocationServiceDayClock),
			)

			baselineResp, baseline := callAPIHandler[TripsForLocationResponse](
				t, api, tripsForLocationURL(2.0, 3.0),
			)
			require.Equal(t, http.StatusOK, baselineResp.StatusCode)
			require.NotEmpty(t, baseline.Data.List, "fixture must produce trips to build references for")

			injectedErr := errors.New("injected reference lookup failure")
			failingDB := &tripsForLocationReferenceFailureDB{
				DBTX:        api.GtfsManager.GtfsDB.DB,
				queryMarker: tt.queryMatch,
				failOnCall:  tt.failOnCall,
				failWith:    injectedErr,
			}
			api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				tripsForLocationURL(2.0, 3.0),
				nil,
			)
			api.tripsForLocationHandler(recorder, request)

			require.Equal(t, 1, failingDB.failures, "the targeted reference query must fail exactly once")
			require.Equal(t, http.StatusInternalServerError, recorder.Code)

			var response struct {
				Code int    `json:"code"`
				Text string `json:"text"`
			}
			err := json.Unmarshal(recorder.Body.Bytes(), &response)
			require.NoErrorf(t, err, "handler must write exactly one JSON document; body=%s", recorder.Body.String())
			require.Equal(t, http.StatusInternalServerError, response.Code)
			require.Equal(t, "internal server error", response.Text)
		})
	}
}
