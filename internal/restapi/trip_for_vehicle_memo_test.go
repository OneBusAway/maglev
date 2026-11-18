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
	"maglev.onebusaway.org/internal/utils"
)

func tripForVehicleMemoURL(agencyID string) string {
	return "/api/where/trip-for-vehicle/" + utils.FormCombinedID(agencyID, benchTripVehicleID) + ".json" +
		benchTripDetailsKey + "&includeSchedule=true"
}

// TestTripForVehicleQueryBudget fails when a change makes trip-for-vehicle
// run more queries, which benchmarks alone would not catch.
func TestTripForVehicleQueryBudget(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	agencyID := mustGetAgencies(t, api)[0].ID
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	addTestVehicle(t, api, benchTripManyStops)

	counter.count.Store(0)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tripForVehicleMemoURL(agencyID), nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.LessOrEqual(t, counter.count.Load(), int64(20))
}

// TestTripForVehicleLeavesMemoizedRowsUnchanged runs a request with a memo it
// can inspect afterwards, then checks the rows memoized for the vehicle's
// trip still match a fresh query.
func TestTripForVehicleLeavesMemoizedRowsUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	agencyID := mustGetAgencies(t, api)[0].ID
	direct := api.GtfsManager.GtfsDB.Queries
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	serviceDate := arrivalsTestClock.Format("20060102")

	tests := []struct {
		name   string
		tripID string
	}{
		{name: "many stops", tripID: benchTripManyStops},
		{name: "long block", tripID: benchTripLongBlock},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addTestVehicle(t, api, tt.tripID)
			ctx := gtfsdb.WithReadMemo(context.Background())
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tripForVehicleMemoURL(agencyID), nil).WithContext(ctx))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			trips := []memoTrip{{tripID: tt.tripID, serviceDate: serviceDate}}
			assertMemoMatchesFresh(t, ctx, counter, direct, trips, memoCheckOptions{})
		})
	}
}
