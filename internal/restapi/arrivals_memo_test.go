package restapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

const arrivalsMemoKey = "key=org.onebusaway.iphone" // exempt from the test rate limit

func arrivalsForStopMemoURL() string {
	return "/api/where/arrivals-and-departures-for-stop/" + arrivalsTestStopID + ".json?" + arrivalsMemoKey
}

func arrivalsForLocationMemoURL() string {
	return strings.Replace(arrivalsForLocationURL(arrivalsForLocationCenter), "key=TEST", arrivalsMemoKey, 1)
}

// TestArrivalsQueryBudget fails when a change makes the arrivals endpoints
// run more queries, which benchmarks alone would not catch.
func TestArrivalsQueryBudget(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)

	tests := []struct {
		name       string
		endpoint   string
		maxQueries int64
	}{
		{name: "for stop", endpoint: arrivalsForStopMemoURL(), maxQueries: 31},
		{name: "for location", endpoint: arrivalsForLocationMemoURL(), maxQueries: 645},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter.count.Store(0)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.endpoint, nil))

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			assert.LessOrEqual(t, counter.count.Load(), tt.maxQueries)
		})
	}
}

// TestArrivalsLeaveMemoizedRowsUnchanged runs each arrivals request with a
// memo it can inspect afterwards, then checks the rows memoized for every
// trip in the response still match a fresh query.
func TestArrivalsLeaveMemoizedRowsUnchanged(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	direct := api.GtfsManager.GtfsDB.Queries
	counter := swapInQueryCounter(t, api)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	agencyLocation := mustLoadAgencyLocation(t, api)

	tests := []struct {
		name        string
		endpoint    string
		withVehicle bool
		arrivals    func(t *testing.T, body []byte) []models.ArrivalAndDeparture
	}{
		{name: "for stop", endpoint: arrivalsForStopMemoURL(), arrivals: arrivalsForStopFromBody},
		{name: "for stop with vehicle", endpoint: arrivalsForStopMemoURL(), withVehicle: true, arrivals: arrivalsForStopFromBody},
		{name: "for location", endpoint: arrivalsForLocationMemoURL(), arrivals: arrivalsForLocationFromBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.withVehicle {
				addTestVehicle(t, api, firstArrivalTripID(t, mux, tt.endpoint, tt.arrivals))
			}
			ctx := gtfsdb.WithReadMemo(context.Background())
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.endpoint, nil).WithContext(ctx))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			trips := memoTripsFromArrivals(t, tt.arrivals(t, recorder.Body.Bytes()), agencyLocation)
			// Arrivals load every trip's frequencies in one batch query.
			assertMemoMatchesFresh(t, ctx, counter, direct, trips, memoCheckOptions{frequenciesBatched: true})
		})
	}
}

func arrivalsForStopFromBody(t *testing.T, body []byte) []models.ArrivalAndDeparture {
	t.Helper()
	var response ArrivalsAndDeparturesResponse
	require.NoError(t, json.Unmarshal(body, &response))
	return response.Data.Entry.ArrivalsAndDepartures
}

func arrivalsForLocationFromBody(t *testing.T, body []byte) []models.ArrivalAndDeparture {
	t.Helper()
	var response ArrivalsAndDeparturesForLocationResponse
	require.NoError(t, json.Unmarshal(body, &response))
	return response.Data.Entry.ArrivalsAndDepartures
}

// firstArrivalTripID returns the raw ID of the first trip the endpoint lists,
// read with an ordinary request before the one under test.
func firstArrivalTripID(t *testing.T, mux *http.ServeMux, endpoint string, arrivals func(*testing.T, []byte) []models.ArrivalAndDeparture) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	listed := arrivals(t, recorder.Body.Bytes())
	require.NotEmpty(t, listed)
	tripID, err := utils.ExtractCodeID(listed[0].TripID)
	require.NoError(t, err)
	return tripID
}

// memoTripsFromArrivals lists each trip once, with the service date its
// arrival ran on in the agency's time zone.
func memoTripsFromArrivals(t *testing.T, arrivals []models.ArrivalAndDeparture, location *time.Location) []memoTrip {
	t.Helper()
	seen := make(map[memoTrip]bool)
	trips := make([]memoTrip, 0, len(arrivals))
	for _, arrival := range arrivals {
		tripID, err := utils.ExtractCodeID(arrival.TripID)
		require.NoError(t, err)
		trip := memoTrip{tripID: tripID, serviceDate: arrival.ServiceDate.In(location).Format("20060102")}
		if !seen[trip] {
			seen[trip] = true
			trips = append(trips, trip)
		}
	}
	return trips
}

func mustLoadAgencyLocation(t *testing.T, api *RestAPI) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(mustGetAgencies(t, api)[0].Timezone)
	require.NoError(t, err)
	return location
}
