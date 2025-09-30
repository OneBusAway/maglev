package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func TestArrivalAndDepartureForStopHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=invalid&tripId="+tripID+"&serviceDate="+
			fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestArrivalAndDepartureForStopHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&tripId=" + tripID + "&serviceDate=" + fmt.Sprintf("%d", serviceDate))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var model models.ResponseModel
	err = json.NewDecoder(resp.Body).Decode(&model)
	require.NoError(t, err)

	// The response might be 404 if the trip doesn't serve this stop, which is acceptable
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)
		assert.Equal(t, "OK", model.Text)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, data)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		// Verify entry fields
		assert.Equal(t, stopID, entry["stopId"])
		assert.Equal(t, tripID, entry["tripId"])
		assert.Equal(t, float64(serviceDate), entry["serviceDate"])
		assert.NotNil(t, entry["scheduledArrivalTime"])
		assert.NotNil(t, entry["scheduledDepartureTime"])
		assert.NotNil(t, entry["arrivalEnabled"])
		assert.NotNil(t, entry["departureEnabled"])
		assert.NotNil(t, entry["stopSequence"])
		assert.NotNil(t, entry["totalStopsInTrip"])

		// Verify references
		references, ok := data["references"].(map[string]interface{})
		assert.True(t, ok)
		assert.NotNil(t, references)

		agencies, ok := references["agencies"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, agencies)

		routes, ok := references["routes"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, routes)

		trips_ref, ok := references["trips"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, trips_ref)

		stops_ref, ok := references["stops"].([]interface{})
		assert.True(t, ok)
		assert.NotEmpty(t, stops_ref)
	case http.StatusNotFound:
		// This is acceptable if the trip doesn't serve this stop
		assert.Equal(t, http.StatusNotFound, model.Code)
	default:
		t.Fatalf("Unexpected status code: %d", resp.StatusCode)
	}
}

func TestArrivalAndDepartureForStopHandlerWithInvalidStopID(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	serviceDate := time.Now().Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/agency_invalid.json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestArrivalAndDepartureForStopHandlerWithTimeParameter(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	if len(stops) == 0 {
		t.Skip("No stops available for testing")
	}

	if len(trips) == 0 {
		t.Skip("No trips available for testing")
	}

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	// Use a specific time (1 hour from now)
	specificTime := time.Now().Add(1 * time.Hour)
	timeMs := specificTime.Unix() * 1000
	serviceDate := specificTime.Unix() * 1000

	_, resp, model := serveAndRetrieveEndpoint(t,
		"/api/where/arrival-and-departure-for-stop/"+stopID+".json?key=TEST&tripId="+tripID+
			"&serviceDate="+fmt.Sprintf("%d", serviceDate)+"&time="+strconv.FormatInt(timeMs, 10))

	// The response might be 404 if the trip doesn't serve this stop, which is acceptable
	switch resp.StatusCode {
	case http.StatusOK:
		assert.Equal(t, http.StatusOK, model.Code)

		data, ok := model.Data.(map[string]interface{})
		assert.True(t, ok)

		entry, ok := data["entry"].(map[string]interface{})
		assert.True(t, ok)

		// The response should be successful
		assert.Equal(t, stopID, entry["stopId"])
		assert.Equal(t, tripID, entry["tripId"])
	case http.StatusNotFound:
		// This is acceptable if the trip doesn't serve this stop
		assert.Equal(t, http.StatusNotFound, model.Code)
	}
}

func TestArrivalAndDepartureForStopHandlerRequiresTripId(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	serviceDate := time.Now().Unix() * 1000

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&serviceDate=" + fmt.Sprintf("%d", serviceDate))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	assert.Contains(t, errorResponse.FieldErrors, "tripId")
	assert.Len(t, errorResponse.FieldErrors["tripId"], 1)
	assert.Equal(t, "missingRequiredField", errorResponse.FieldErrors["tripId"][0])
}

func TestArrivalAndDepartureForStopHandlerRequiresServiceDate(t *testing.T) {
	api := createTestApi(t)

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	trips := api.GtfsManager.GetTrips()

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/where/arrival-and-departure-for-stop/" + stopID +
		".json?key=TEST&tripId=" + tripID)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse struct {
		FieldErrors map[string][]string `json:"fieldErrors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	assert.Contains(t, errorResponse.FieldErrors, "serviceDate")
	assert.Len(t, errorResponse.FieldErrors["serviceDate"], 1)
	assert.Equal(t, "missingRequiredField", errorResponse.FieldErrors["serviceDate"][0])
}
