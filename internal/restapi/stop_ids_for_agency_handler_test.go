package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// stopIdsForAgencyURL builds the /stop-ids-for-agency URL with key=TEST baked in.
// Extra query params are merged from optional url.Values arguments.
func stopIdsForAgencyURL(agencyID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/stop-ids-for-agency/" + agencyID + ".json?" + q.Encode()
}

func TestStopIdsForAgencyRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api,
		"/api/where/stop-ids-for-agency/"+testdata.Raba.ID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopIdsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.NotEmpty(t, model.Data.List, "expected stop IDs for agency")
	for i, stopID := range model.Data.List {
		assert.True(t, strings.HasPrefix(stopID, testdata.Raba.ID+"_"),
			"stopIds[%d] should have agency prefix: %s", i, stopID)
	}
	assert.Empty(t, model.Data.References.Agencies)
}

func TestStopIdsForAgencyInvalidAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL("invalid"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestStopIdsForAgencyMalformedAgencyId(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL("bad@agency"))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopIdsForAgencyMissingApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, "/api/where/stop-ids-for-agency/"+testdata.Raba.ID+".json")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopIdsForAgencyEmptyAgencyId(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(""))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestStopIdsForAgencyLimitExceededIsFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.False(t, model.Data.LimitExceeded)
}

func TestStopIdsForAgencyReferencesAlwaysPresentAndEmpty(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotNil(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Agencies)
	assert.NotNil(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Routes)
	assert.NotNil(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Stops)
	assert.NotNil(t, model.Data.References.Trips)
	assert.Empty(t, model.Data.References.Trips)
	assert.NotNil(t, model.Data.References.Situations)
	assert.Empty(t, model.Data.References.Situations)
	assert.NotNil(t, model.Data.References.StopTimes)
	assert.Empty(t, model.Data.References.StopTimes)
}

func TestStopIdsForAgencyVersionIsTwo(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, stopIdsForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, model.Version)
}
