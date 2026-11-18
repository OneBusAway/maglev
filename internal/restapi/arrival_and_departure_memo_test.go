package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// arrivalAndDepartureMemoURL asks for the first arrival arrivals-for-stop
// lists at the test stop, by its trip and service date.
func arrivalAndDepartureMemoURL(t *testing.T, mux *http.ServeMux) (string, models.ArrivalAndDeparture) {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, arrivalsForStopMemoURL(), nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	arrivals := arrivalsForStopFromBody(t, recorder.Body.Bytes())
	require.NotEmpty(t, arrivals)
	first := arrivals[0]
	query := url.Values{
		"tripId":      {first.TripID},
		"serviceDate": {strconv.FormatInt(first.ServiceDate.UnixMilli(), 10)},
	}
	return "/api/where/arrival-and-departure-for-stop/" + arrivalsTestStopID + ".json?" + arrivalsMemoKey + "&" + query.Encode(), first
}

// TestArrivalAndDepartureQueryBudget fails when a change makes
// arrival-and-departure-for-stop run more queries, which benchmarks alone
// would not catch.
func TestArrivalAndDepartureQueryBudget(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	endpoint, _ := arrivalAndDepartureMemoURL(t, mux)

	counter.count.Store(0)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.LessOrEqual(t, counter.count.Load(), int64(19))
}

// TestArrivalAndDepartureLeavesMemoizedRowsUnchanged runs a request with a
// memo it can inspect afterwards, then checks the rows memoized for the
// arrival's trip still match a fresh query.
func TestArrivalAndDepartureLeavesMemoizedRowsUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	direct := api.GtfsManager.GtfsDB.Queries
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	agencyLocation := mustLoadAgencyLocation(t, api)
	endpoint, first := arrivalAndDepartureMemoURL(t, mux)

	tests := []struct {
		name        string
		withVehicle bool
	}{
		{name: "scheduled"},
		{name: "with vehicle", withVehicle: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.withVehicle {
				addTestVehicle(t, api, firstArrivalTripID(t, mux, arrivalsForStopMemoURL(), arrivalsForStopFromBody))
			}
			ctx := gtfsdb.WithReadMemo(context.Background())
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil).WithContext(ctx))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			trips := memoTripsFromArrivals(t, []models.ArrivalAndDeparture{first}, agencyLocation)
			assertMemoMatchesFresh(t, ctx, counter, direct, trips, memoCheckOptions{})
		})
	}
}
