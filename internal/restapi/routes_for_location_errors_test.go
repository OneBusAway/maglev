package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

// routeSearchFailureDB fails only the selected lookup so unrelated queries can
// still succeed. Each test uses its own fixture database.
type routeSearchFailureDB struct {
	gtfsdb.DBTX
	queryMatch string
	failWith   error
	failures   int
}

func (db *routeSearchFailureDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, db.queryMatch) {
		db.failures++
		return nil, db.failWith
	}
	return db.DBTX.QueryContext(ctx, query, args...)
}

// TestRoutesForLocationQueryFailure verifies that a failed route search is
// served as an error, not as an empty 200: a client must be able to tell a
// database failure apart from "no routes here" (#1728).
func TestRoutesForLocationQueryFailure(t *testing.T) {
	tests := []struct {
		name       string
		queryMatch string
		queryParam string
	}{
		{
			name:       "spatial",
			queryMatch: "JOIN stops_rtree r ON",
		},
		{
			name:       "text-search",
			queryMatch: "routes_fts MATCH",
			queryParam: "&query=19",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Own in-memory fixture: the failure injection below replaces the
			// manager's Queries, and createTestApi's shared RABA database would
			// carry that replacement into every later test in the package.
			api := createTestApiWithFeed(t, models.GetFixturePath(t, "raba.zip"))
			endpoint := "/api/where/routes-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500&includeReferences=false" + tt.queryParam

			resp, healthy := callAPIHandler[RoutesResponse](t, api, endpoint)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, http.StatusOK, healthy.Code)
			require.NotEmpty(t, healthy.Data.List)

			for _, failure := range []struct {
				name string
				err  error
				code int
				text string
			}{
				{"database-error", errors.New("injected route-search database failure"), http.StatusInternalServerError, "internal server error"},
				{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "gateway timeout"},
			} {
				t.Run(failure.name, func(t *testing.T) {
					failingDB := &routeSearchFailureDB{
						DBTX:       api.GtfsManager.GtfsDB.DB,
						queryMatch: tt.queryMatch,
						failWith:   failure.err,
					}
					api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)
					resp, failed := callAPIHandler[RoutesResponse](t, api, endpoint)
					require.Positive(t, failingDB.failures, "the intended query must actually fail")
					assert.Equal(t, failure.code, resp.StatusCode)
					assert.Equal(t, failure.code, failed.Code)
					assert.Equal(t, failure.text, failed.Text)
					assert.Empty(t, failed.Data.List, "a failed search must not be served as an empty list")
				})
			}
		})
	}
}
