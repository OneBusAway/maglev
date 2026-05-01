package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestStopIdsForAgencyRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, "/api/where/stop-ids-for-agency/test.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestStopIdsForAgencyEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, "/api/where/stop-ids-for-agency/"+testdata.Raba.ID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.NotEmpty(t, model.Data.List)
	for _, stopID := range model.Data.List {
		assert.True(t, strings.HasPrefix(stopID, testdata.Raba.ID+"_"),
			"Stop ID should start with agency ID prefix: %s", stopID)
	}
	assert.Empty(t, model.Data.References.Agencies)
}

func TestInvalidAgencyId(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, "/api/where/stop-ids-for-agency/invalid.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "", model.Text)
}

func TestMalformedAgencyIdForStopIds(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopIDsForAgencyResponse](t, api, "/api/where/stop-ids-for-agency/bad@agency.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}
