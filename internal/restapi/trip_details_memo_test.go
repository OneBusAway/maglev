package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// These tests build requests by hand rather than through
// serveApiAndRetrieveEndpoint, because they need a read memo or a query
// counter on the request's context and that helper goes through a real
// HTTP client.

// TestTripDetailsQueryBudget fails when a change makes trip-details run more
// queries, which benchmarks alone would not catch.
func TestTripDetailsQueryBudget(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	agencyID := mustGetAgencies(t, api)[0].ID
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	tests := []struct {
		name        string
		tripID      string
		query       string
		withVehicle bool
		maxQueries  int64
	}{
		{name: "few stops", tripID: benchTripFewStops, maxQueries: 20},
		{name: "many stops", tripID: benchTripManyStops, maxQueries: 20},
		{name: "long block", tripID: benchTripLongBlock, maxQueries: 20},
		{name: "vehicle", tripID: benchTripManyStops, withVehicle: true, maxQueries: 20},
		{name: "no schedule", tripID: benchTripManyStops, query: "&includeSchedule=false", maxQueries: 18},
		{name: "no status", tripID: benchTripManyStops, query: "&includeStatus=false", maxQueries: 13},
		{name: "no references", tripID: benchTripManyStops, query: "&includeReferences=false", maxQueries: 15},
		{name: "service date", tripID: benchTripManyStops, query: "&serviceDate=" + arrivalsTestClock.Format(serviceDateLayout), maxQueries: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.withVehicle {
				addTestVehicle(t, api, tt.tripID)
			}
			endpoint := "/api/where/trip-details/" + utils.FormCombinedID(agencyID, tt.tripID) + ".json" + benchTripDetailsKey + tt.query
			counter.count.Store(0)

			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.LessOrEqual(t, counter.count.Load(), tt.maxQueries)
		})
	}
}

// TestTripDetailsLeavesMemoizedRowsUnchanged runs a full request with a memo
// it can inspect afterwards, then checks every row the request memoized
// still matches a fresh query, so no code on the path edited a shared row.
func TestTripDetailsLeavesMemoizedRowsUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	agencyID := mustGetAgencies(t, api)[0].ID
	direct := api.GtfsManager.GtfsDB.Queries
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	serviceDate := arrivalsTestClock.Format("20060102")

	tests := []struct {
		name        string
		tripID      string
		withVehicle bool
	}{
		{name: "many stops", tripID: benchTripManyStops},
		{name: "vehicle", tripID: benchTripManyStops, withVehicle: true},
		{name: "long block", tripID: benchTripLongBlock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.withVehicle {
				addTestVehicle(t, api, tt.tripID)
			}
			ctx := gtfsdb.WithReadMemo(context.Background())
			endpoint := "/api/where/trip-details/" + utils.FormCombinedID(agencyID, tt.tripID) + ".json" + benchTripDetailsKey
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil).WithContext(ctx))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			counter.count.Store(0)
			memo := api.GtfsManager.GtfsDB.Queries.Memo(ctx)
			memoized := readMemoizedTripRows(t, ctx, memo, tt.tripID, serviceDate)
			require.Zero(t, counter.count.Load(), "every row should already be in the request's memo")

			fresh := readMemoizedTripRows(t, context.Background(), direct.Memo(context.Background()), tt.tripID, serviceDate)
			assert.Equal(t, fresh, memoized)
		})
	}
}

// swapInQueryCounter routes api's sqlc queries through a counter for the
// rest of the test.
func swapInQueryCounter(t testing.TB, api *RestAPI) *queryCountingDB {
	t.Helper()
	counter := &queryCountingDB{DBTX: api.GtfsManager.GtfsDB.DB}
	original := api.GtfsManager.GtfsDB.Queries
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(counter)
	t.Cleanup(func() { api.GtfsManager.GtfsDB.Queries = original })
	return counter
}

func addTestVehicle(t testing.TB, api *RestAPI, tripID string) {
	t.Helper()
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(context.Background(), tripID)
	require.NoError(t, err)
	now := arrivalsTestClock
	api.GtfsManager.MockAddVehicleWithOptions(benchTripVehicleID, trip.ID, trip.RouteID,
		gtfs.MockVehicleOptions{Timestamp: &now})
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
}
