package restapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForLocationMemoURL covers the stops around arrivalsForLocationCenter.
func tripsForLocationMemoURL(key, query string) string {
	return "/api/where/trips-for-location.json?" + key +
		"&lat=40.539367&lon=-122.34952&latSpan=0.04&lonSpan=0.04" + query
}

// TestTripsForLocationQueryBudget fails when a change makes trips-for-location
// run more queries, which benchmarks alone would not catch. It loads its own
// copy of the feed: the endpoint does work for every agency, and other tests
// leave agencies behind in the shared database.
func TestTripsForLocationQueryBudget(t *testing.T) {
	api := createTestApiWithFeedAndClock(t, filepath.Join("../../testdata", "raba.zip"), clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	tests := []struct {
		name       string
		query      string
		maxQueries int64
	}{
		{name: "schedule only", query: "&includeSchedule=true", maxQueries: 46},
		{name: "status and schedule", query: "&includeStatus=true&includeSchedule=true", maxQueries: 54},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter.count.Store(0)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tripsForLocationMemoURL("key=TEST", tt.query), nil))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.LessOrEqual(t, counter.count.Load(), tt.maxQueries)
		})
	}
}

// TestTripsForLocationLeavesMemoizedRowsUnchanged runs a request with a memo
// it can inspect afterwards, then checks the rows memoized for every trip in
// the response still match a fresh query.
func TestTripsForLocationLeavesMemoizedRowsUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	direct := api.GtfsManager.GtfsDB.Queries
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	agencyLocation := mustLoadAgencyLocation(t, api)

	ctx := gtfsdb.WithReadMemo(context.Background())
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tripsForLocationMemoURL(arrivalsMemoKey, "&includeStatus=true&includeSchedule=true"), nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response TripsForLocationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	trips := make([]memoTrip, 0, len(response.Data.List))
	for _, entry := range response.Data.List {
		tripID, err := utils.ExtractCodeID(entry.TripId)
		require.NoError(t, err)
		serviceDate := time.UnixMilli(entry.ServiceDate).In(agencyLocation).Format("20060102")
		trips = append(trips, memoTrip{tripID: tripID, serviceDate: serviceDate})
	}
	// Trip status reads each trip's rows through the memo, but frequencies for
	// every trip come from one batch query.
	assertMemoMatchesFresh(t, ctx, counter, direct, trips, memoCheckOptions{frequenciesBatched: true})
}
