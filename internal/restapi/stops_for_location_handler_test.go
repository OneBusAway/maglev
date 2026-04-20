package restapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

type StopsResponse struct {
	Code        int       `json:"code"`
	CurrentTime int64     `json:"currentTime"`
	Data        StopsData `json:"data,omitempty"`
	Text        string    `json:"text"`
	Version     int       `json:"version"`
}

type StopsData struct {
	LimitExceeded bool                   `json:"limitExceeded"`
	Stops         []models.Stop          `json:"list"`
	OutOfRange    bool                   `json:"outOfRange"`
	References    models.ReferencesModel `json:"references"`
	FieldErrors   map[string][]string    `json:"fieldErrors"`
}

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

	assert.NotEmpty(t, model.Data.Stops)

	for i, stop := range model.Data.Stops {
		assert.NotEmpty(t, stop.ID)
		assert.NotEmpty(t, stop.Name)
		assert.NotZero(t, stop.Lat)
		assert.NotZero(t, stop.Lon)
		assert.NotNil(t, stop.RouteIDs)
		assert.NotNil(t, stop.StaticRouteIDs)

		if i > 0 {
			assert.GreaterOrEqualf(t, stop.ID, model.Data.Stops[i-1].ID, "stops should be returned in sorted order by id")
		}
	}

	refs := model.Data.References
	assert.NotEmpty(t, refs.Agencies)
	assert.NotEmpty(t, refs.Routes)

	// Verify all referenced route IDs exist in references
	referencedRouteIDs := make(map[string]bool)
	for _, stop := range model.Data.Stops {
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
	assert.Len(t, model.Data.Stops, 1)
	assert.Equal(t, "2042", model.Data.Stops[0].Code)
	assert.Equal(t, "Buenaventura Blvd at Eureka Way", model.Data.Stops[0].Name)
}

func TestStopsForLocationLatSpanAndLonSpan(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=0.045&lonSpan=0.059")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.Stops)
}

func TestStopsForLocationRadius(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.Stops)
}

func TestStopsForLocationLatAndLan(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&radius=1000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.Stops)
}

func TestStopsForLocationIsLimitExceeded(t *testing.T) {
	clock := clock.NewMockClock(time.Date(2025, 12, 26, 14, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, clock)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.362535&radius=1000&maxCount=1")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, model.Data.Stops, 1)
	assert.True(t, model.Data.LimitExceeded)
}

func TestStopsForLocationActiveRoutesOnly(t *testing.T) {
	futureClock := clock.NewMockClock(time.Date(2031, 1, 1, 12, 0, 0, 0, time.UTC))
	api := createTestApiWithClock(t, futureClock)

	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=5000")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.Stops, "Should return empty stops when no routes are active")
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
	assert.NotNil(t, model.Data.Stops)
	assert.NotEmpty(t, model.Data.References.Agencies)
	assert.NotEmpty(t, model.Data.References.Routes)
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
	assert.Empty(t, model.Data.Stops)
}

func TestStopsForLocationMissingLat(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lon=-122.426966")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationMissingLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST&lat=40.583321")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopsForLocationMissingBothLatAndLon(t *testing.T) {
	api := createTestApi(t)
	resp, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-location.json?key=TEST")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
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
	assert.Len(t, model.Data.Stops, 1)

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
