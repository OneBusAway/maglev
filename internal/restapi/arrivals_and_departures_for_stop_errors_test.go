package restapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
)

// TestArrivalsAndDeparturesForStop_NearbyStopsDBError verifies that a failed
// GetStopIDsWithinBounds query returns 500 instead of silently emitting an
// empty nearbyStopIds list.
func TestArrivalsAndDeparturesForStop_NearbyStopsDBError(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	endpoint := arrivalsAndDeparturesURL(arrivalsTestStopID, nil)

	resp, _ := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)
	require.Equal(t, http.StatusOK, resp.StatusCode, "healthy request must return 200 before injecting failure")

	// getStopIDsWithinBounds is the only spatial query that selects only s.id
	// on a single line before FROM — unique across the stops_rtree queries.
	failingDB := &stopSearchFailureDB{
		DBTX:       api.GtfsManager.GtfsDB.DB,
		queryMatch: "SELECT s.id\nFROM stops s",
		failWith:   errors.New("injected stops-within-bounds failure"),
	}
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(failingDB)

	resp, failed := callAPIHandler[ArrivalsAndDeparturesResponse](t, api, endpoint)
	require.Positive(t, failingDB.failures, "GetStopIDsWithinBounds must actually fail")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, failed.Code)
	assert.Equal(t, "internal server error", failed.Text)
}
