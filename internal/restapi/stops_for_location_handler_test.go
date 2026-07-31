package restapi

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

func TestStopsForLocationHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=invalid&lat=47.586556&lon=-122.190396")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopsForLocationHandlerEndToEnd(t *testing.T) {
	// Mock clock set to Dec 26, 2025. This date was chosen by evaluating the test
	// criteria: we need a day with active stops within the queried location.
	// Any date that satisfies the test requirements against the test GTFS data can be used
	// in the test.

	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 00, 00, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.NotEmpty(t, model.Data.List)

	for i, stop := range model.Data.List {
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
		assert.NotNil(t, stop.RouteIDs)
		assert.NotNil(t, stop.StaticRouteIDs)

		if i > 0 {
			assert.GreaterOrEqualf(t, stop.ID, model.Data.List[i-1].ID, "stops should be returned in sorted order by id")
		}
	}

	refs := model.Data.References
	assert.NotEmpty(t, refs.Agencies)
	assert.NotEmpty(t, refs.Routes)

	// Verify all referenced route IDs exist in references
	referencedRouteIDs := make(map[string]bool)
	for _, stop := range model.Data.List {
		for _, id := range stop.RouteIDs {
			referencedRouteIDs[id] = true
		}
		for _, id := range stop.StaticRouteIDs {
			referencedRouteIDs[id] = true
		}
	}
	require.NotEmpty(t, referencedRouteIDs, "Test data must have route references to verify")
	refRouteIDs := make(map[string]bool)
	for _, route := range refs.Routes {
		refRouteIDs[route.ID] = true
	}
	for routeID := range referencedRouteIDs {
		assert.Contains(t, refRouteIDs, routeID, "Stop routeId should reference known route")
	}

	// Verify all route agencyIds exist in references
	refAgencyIDs := make(map[string]bool)
	for _, agency := range refs.Agencies {
		refAgencyIDs[agency.ID] = true
	}
	for _, route := range refs.Routes {
		assert.Contains(t, refAgencyIDs, route.AgencyID, "Route agencyId should reference known agency")
	}

	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Stops)
	assert.Empty(t, refs.Trips)
}

func TestStopsForLocationQuery(t *testing.T) {
	// Stop 2042 only has trips on service c_2713_b_80332_d_56 (Thu/Fri/Sat, May 22 - Sep 6, 2025).
	// Use a Friday within that range to ensure active service.
	clock := clock.NewMockClock(time.Date(2025, 6, 13, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.List, 1)
	assert.Equal(t, "2042", model.Data.List[0].Code)
	assert.Equal(t, "Buenaventura Blvd at Eureka Way", model.Data.List[0].Name)
}

func TestStopsForLocationLatSpanAndLonSpan(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.045&lonSpan=0.059")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.List)
}

func TestStopsForLocationRadius(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.List)
}

func TestStopsForLocationLatAndLan(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&radius=1000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.List)
}

func TestStopsForLocationIsLimitExceeded(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&radius=1000&maxCount=1")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.List, 1)
	assert.True(t, model.Data.LimitExceeded)
}

func TestStopsForLocationActiveRoutesOnly(t *testing.T) {
	futureClock := clock.NewMockClock(time.Date(2031, 1, 1, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, futureClock)

	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List, "Should return empty stops when no routes are active")
}

func TestStopsForLocationHandlerValidatesParameters(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=invalid&lon=-121.74")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationHandlerValidatesLatLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=invalid&lon=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationHandlerValidatesLatLonSpan(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=invalid&lonSpan=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationHandlerValidatesRadius(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationHandlerClampsMaxCountAboveCap(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)

	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000&maxCount=300")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.NotEmpty(t, model.Data.List, "clamped request should still return stops")
	assert.LessOrEqual(t, len(model.Data.List), 250, "results must not exceed the 250 cap")
}

// Raw stop IDs "2" and "9" sort opposite to their combined IDs "SortB_2" and
// "SortA_9", so this distinguishes combined-ID ordering from raw-ID ordering.
func TestStopsForLocationSortsByCombinedStopID(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2024, 6, 12, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries
	lat, lon := 41.5, -123.5 // away from the RABA fixture stops

	for i, tc := range []struct {
		agencyID string
		stopID   string
	}{
		{"SortB", "2"},
		{"SortA", "9"},
	} {
		_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
			ID: tc.agencyID, Name: tc.agencyID, Url: "http://example.com", Timezone: "America/Los_Angeles",
		})
		require.NoError(t, err)

		routeID, svcID, tripID := tc.agencyID+"R", tc.agencyID+"Svc", tc.agencyID+"T"

		_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
			ID: routeID, AgencyID: tc.agencyID, ShortName: nulls.String("S"), Type: 3,
		})
		require.NoError(t, err)

		_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
			ID: tc.stopID, Name: nulls.String("Sort Stop"), Lat: lat + float64(i)*0.001, Lon: lon,
		})
		require.NoError(t, err)

		_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
			ID: svcID, Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, Saturday: 1, Sunday: 1,
			StartDate: "20240101", EndDate: "20241231",
		})
		require.NoError(t, err)

		_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{ID: tripID, RouteID: routeID, ServiceID: svcID})
		require.NoError(t, err)

		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: tripID, StopID: tc.stopID, StopSequence: 1,
			ArrivalTime: 12 * 3600 * int64(time.Second), DepartureTime: 12 * 3600 * int64(time.Second),
		})
		require.NoError(t, err)
	}

	endpoint := fmt.Sprintf("/api/where/stops-for-location.json?key=TEST&lat=%f&lon=%f&radius=2000", lat, lon)
	resp, model := callAPIHandler[StopsResponse](t, api, endpoint)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	ids := make([]string, 0, len(model.Data.List))
	for _, stop := range model.Data.List {
		ids = append(ids, stop.ID)
	}

	// Assert relative order only, so unrelated stops seeded nearby don't break this.
	idxA, idxB := slices.Index(ids, "SortA_9"), slices.Index(ids, "SortB_2")
	require.NotEqual(t, -1, idxA, "SortA_9 should be returned")
	require.NotEqual(t, -1, idxB, "SortB_2 should be returned")
	assert.Less(t, idxA, idxB, "combined ID order puts SortA_9 before SortB_2")
}

func TestStopsForLocationHandlerValidatesMaxCount(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&maxCount=invalid")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationHandlerRouteTypeErrorLimit(t *testing.T) {
	invalidTypes := strings.Repeat("bad,", 14) + "bad"

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + invalidTypes
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, url)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	routeTypeErrors := model.Data.FieldErrors["routeType"]
	assert.Len(t, routeTypeErrors, 1, "Should return a single error for invalid routeType")
	assert.Contains(t, routeTypeErrors[0], "Invalid field value for field", "Error should use standard generic message")
}

func TestStopsForLocationHandlerRouteTypeTooManyTokens(t *testing.T) {
	tokens := make([]string, 150)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("%d", i)
	}
	manyTokens := strings.Join(tokens, ",")

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + manyTokens
	api := createTestApi(t)
	resp, model := callAPIHandler[models.ResponseModel](t, api, url)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := data["fieldErrors"].(map[string]any)
	require.True(t, ok, "data should contain fieldErrors map")

	routeTypeErrors, ok := fieldErrors["routeType"].([]any)
	require.True(t, ok, "fieldErrors should contain routeType errors list")

	assert.Len(t, routeTypeErrors, 1, "Should return single error for too many tokens")

	firstError, ok := routeTypeErrors[0].(string)
	require.True(t, ok)
	assert.Contains(t, firstError, "too many route types", "Error should mention the token limit")
}

func TestStopsForLocationHandlerRouteTypeAtLimit(t *testing.T) {
	tokens := make([]string, 100)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("%d", i)
	}
	validTypes := strings.Join(tokens, ",")

	url := "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=" + validTypes
	api := createTestApi(t)
	resp, _ := callAPIHandler[StopsResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "100 tokens should be accepted (at the limit)")
}

func TestStopsForLocationHandlerRouteTypeMixedValidInvalid(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[models.ResponseModel](t, api,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&routeType=1,bad,2,invalid,3")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	data, ok := model.Data.(map[string]any)
	require.True(t, ok, "response data should be a map")

	fieldErrors, ok := data["fieldErrors"].(map[string]any)
	require.True(t, ok, "data should contain fieldErrors map")

	routeTypeErrors, ok := fieldErrors["routeType"].([]any)
	require.True(t, ok, "fieldErrors should contain routeType errors list")

	assert.Len(t, routeTypeErrors, 1, "Should return a single error for invalid routeType")

	for _, err := range routeTypeErrors {
		errStr, ok := err.(string)
		require.True(t, ok)
		assert.Contains(t, errStr, "Invalid field value for field", "Error should use standard generic message")
	}
}

func TestStopsForLocationHandlerRouteTypeValidMultiple(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := callAPIHandler[StopsResponse](t, api,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=2500&routeType=1,2,3")

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Valid route types should be accepted")
	assert.NotNil(t, model.Data.List)
	assert.NotEmpty(t, model.Data.References.Agencies)
	assert.NotEmpty(t, model.Data.References.Routes)
}

// Stop 2042 runs Thu/Fri/Sat only, so a Monday leaves it with no active service.
func TestStopsForLocationQueryIgnoresActiveService(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 16, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	resp, model := callAPIHandler[StopsResponse](t, api,
		"/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.List, 1, "stop-code lookup should not depend on the queried date")
	assert.Equal(t, "2042", model.Data.List[0].Code)
}

func TestStopsForLocationQueryOutOfArea(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 6, 13, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	// Use coordinates far from the RABA service area to verify global stop code search
	resp, model := callAPIHandler[StopsResponse](t, api,
		"/api/where/stops-for-location.json?key=TEST&lat=0.0&lon=0.0&query=2042")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// curl https://api.pugetsound.onebusaway.org/api/where/stops-for-location.json?key=TEST&lat=0.0&lon=0.0&query=10914
	// returns no results.
	assert.Empty(t, model.Data.List)
}

func TestStopsForLocationMissingLat(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lon=-122.426966")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}

func TestStopsForLocationMissingLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}

func TestStopsForLocationMissingBothLatAndLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.True(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List)
}

func TestStopsForLocationHandlerWithSituations(t *testing.T) {
	// Setup Mock Clock
	mockClock := clock.NewMockClock(time.Date(2025, 6, 13, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)

	// Add a test alert targeting a SPECIFIC STOP (Stop 2042) using the correct gtfs.Alert structure
	stopID := "2042"
	mockAlert := gtfs.Alert{
		ID: "test-alert-stop-2042",
		InformedEntities: []gtfs.AlertInformedEntity{
			{StopID: &stopID},
		},
		Description: []gtfs.AlertText{
			{Text: "Stop 2042 is closed today", Language: "en"},
		},
	}
	api.GtfsManager.AddAlertForTest(mockAlert)

	// Call the API and force it to find Stop 2042 using the query parameter
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&query=2042")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.List, 1)

	// Verify references contain the situation we added
	refs := model.Data.References
	require.NotEmpty(t, refs.Situations, "Expected at least one situation to be returned for Stop 2042")

	// Find our specific test alert in the returned situations
	foundOurAlert := false
	for _, sit := range refs.Situations {
		if sit.Description != nil && strings.Contains(sit.Description.Value, "Stop 2042 is closed today") {
			foundOurAlert = true
			break
		}
	}

	assert.True(t, foundOurAlert, "Expected to find our mock alert in the references.situations")
}
