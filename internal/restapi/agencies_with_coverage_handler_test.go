package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestAgenciesWithCoverageHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestAgenciesWithCoverageHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.Len(t, model.Data.List, 1)
	agencyCoverage := model.Data.List[0]
	assert.Equal(t, "25", agencyCoverage.AgencyID)
	assert.InDelta(t, 40.3304015, agencyCoverage.Lat, 1e-7)
	assert.InDelta(t, 1.2138890, agencyCoverage.LatSpan, 1e-7)
	assert.InDelta(t, -122.0981970, agencyCoverage.Lon, 1e-7)
	assert.InDelta(t, 0.9843940, agencyCoverage.LonSpan, 1e-7)

	assert.ElementsMatch(t, model.Data.References.Agencies, []models.AgencyReference{testdata.Raba})
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Situations)
	assert.Empty(t, model.Data.References.StopTimes)
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Trips)
}

func TestAgenciesWithCoverageHandlerPagination(t *testing.T) {
	// Test data (raba.zip) has 1 agency
	api := createTestApi(t)

	_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&limit=1")
	assert.Len(t, model.Data.List, 1)
	assert.False(t, model.Data.LimitExceeded)

	_, model = callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&limit=0")
	assert.Len(t, model.Data.List, 1)
	assert.False(t, model.Data.LimitExceeded)

	_, model = callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&offset=1")
	assert.Len(t, model.Data.List, 0)
	assert.False(t, model.Data.LimitExceeded)
}
