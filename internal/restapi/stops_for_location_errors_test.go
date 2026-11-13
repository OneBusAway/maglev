package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// stopSearchFailureDB fails only the selected lookup so unrelated queries can
// still succeed. Each test uses its own fixture database.
type stopSearchFailureDB struct {
	gtfsdb.DBTX
	queryMatch string
	failWith   error
	failures   int
}

func (db *stopSearchFailureDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, db.queryMatch) {
		db.failures++
		return nil, db.failWith
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

// TestStopsForLocationSearchQueryFailure verifies that failed searches return
// an error instead of an empty list or stops outside the requested route type.
func TestStopsForLocationSearchQueryFailure(t *testing.T) {
	tests := []struct {
		name             string
		queryMatch       string
		stopCodeQuery    bool
		querySuffix      string
		wantEmptyHealthy bool
	}{
		{
			name:       "spatial",
			queryMatch: "INNER JOIN stops_rtree sr",
		},
		{
			name:          "stop-code",
			queryMatch:    "-- name: GetStopsByCode :many",
			stopCodeQuery: true,
		},
		{
			name:             "route-type",
			queryMatch:       "-- name: GetRoutesForStops :many",
			querySuffix:      "&routeType=0",
			wantEmptyHealthy: true, // RABA has bus service, not tram service.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := createTestApiWithFeed(t, models.GetFixturePath(t, "raba.zip"))
			api.Clock = clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
			endpoint := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500&includeReferences=false"
			resp, initial := callAPIHandler[StopsResponse](t, api, endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, initial.Data.List)

			if tt.stopCodeQuery {
				var code string
				for _, stop := range initial.Data.List {
					if stop.Code != "" {
						code = stop.Code
						break
					}
				}
				require.NotEmpty(t, code, "fixture must contain a searchable stop code")
				endpoint += "&query=" + url.QueryEscape(code)
			}
			endpoint += tt.querySuffix

			resp, healthy := callAPIHandler[StopsResponse](t, api, endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, http.StatusOK, healthy.Code)
			if tt.wantEmptyHealthy {
				require.Empty(t, healthy.Data.List)
			} else {
				require.NotEmpty(t, healthy.Data.List)
			}

			for _, failure := range []struct {
				name string
				err  error
				code int
				text string
			}{
				{"database-error", errors.New("injected stop-search database failure"), http.StatusInternalServerError, "internal server error"},
				{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "gateway timeout"},
			} {
				t.Run(failure.name, func(t *testing.T) {
					failingDB := &stopSearchFailureDB{
						DBTX:       api.GtfsManager.GtfsDB.DB,
						queryMatch: tt.queryMatch,
						failWith:   failure.err,
					}
					api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)
					resp, failed := callAPIHandler[StopsResponse](t, api, endpoint)
					require.Positive(t, failingDB.failures, "the intended query must actually fail")
					assert.Equal(t, failure.code, resp.StatusCode)
					assert.Equal(t, failure.code, failed.Code)
					assert.Equal(t, failure.text, failed.Text)
					assert.Empty(t, failed.Data.List, "a failed search must not return unfiltered stops")
				})
			}
		})
	}
}
