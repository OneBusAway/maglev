package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestAgenciesWithCoverageHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestAgenciesWithCoverageHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

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
	defer api.Shutdown()

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

// TestAgenciesWithCoverageHandlerEmptyReferencesNotNull pins the wire format:
// references.agencies must decode as an empty slice, never nil, so it
// serializes as `[]` rather than `null`.
func TestAgenciesWithCoverageHandlerEmptyReferencesNotNull(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&offset=1")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotNil(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Agencies)
}

func TestAgenciesWithCoverageHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, err := api.GtfsManager.GtfsDB.DB.Exec("INSERT INTO agencies (id, name, url, timezone) VALUES ('MOCK_SECOND', 'Mock Second Agency', 'http://mock.agency', 'America/Los_Angeles')")
	require.NoError(t, err)

	t.Cleanup(func() {
		if _, err := api.GtfsManager.GtfsDB.DB.Exec("DELETE FROM agencies WHERE id = 'MOCK_SECOND'"); err != nil {
			t.Errorf("failed to clean up MOCK_SECOND agency: %v", err)
		}
	})

	tests := []struct {
		name              string
		query             string
		wantLen           int
		wantLimitExceeded bool
	}{
		{"maxCount below total caps the list", "&maxCount=1", 1, true},
		{"maxCount equal to total returns all", "&maxCount=2", 2, false},
		{"maxCount above total returns all", "&maxCount=3", 2, false},
		{"no maxCount returns all", "", 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, model := callAPIHandler[CoverageResponse](t, api,
				"/api/where/agencies-with-coverage.json?key=TEST"+tt.query)
			assert.Len(t, model.Data.List, tt.wantLen)
			assert.Equal(t, tt.wantLimitExceeded, model.Data.LimitExceeded)
		})
	}
}

func TestAgenciesWithCoverageHandlerIncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&includeReferences=false")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	// List should still be present and correct
	assert.Len(t, model.Data.List, 1)

	// But References.Agencies should be explicitly empty, not containing Raba
	assert.NotNil(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Situations)
	assert.Empty(t, model.Data.References.StopTimes)
	assert.Empty(t, model.Data.References.Stops)
	assert.Empty(t, model.Data.References.Trips)
}

func TestAgenciesWithCoverageHandlerZeroStopTimes(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Insert a mock agency with exactly zero stop-times to the test data.
	_, err := api.GtfsManager.GtfsDB.DB.Exec("INSERT INTO agencies (id, name, url, timezone) VALUES ('MOCK_ZERO', 'Mock Agency', 'http://mock.agency', 'America/Los_Angeles')")

	require.NoError(t, err)

	// Clean up after test
	t.Cleanup(func() {
		if _, err := api.GtfsManager.GtfsDB.DB.Exec("DELETE FROM agencies WHERE id = 'MOCK_ZERO'"); err != nil {
			t.Errorf("failed to clean up MOCK_ZERO agency: %v", err)
		}
	})

	resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	var mockAgency *models.AgencyCoverage
	for i, agency := range model.Data.List {
		if agency.AgencyID == "MOCK_ZERO" {
			mockAgency = &model.Data.List[i]
			break
		}
	}

	require.NotNil(t, mockAgency, "mock agency should be in the returned list")

	// The spec mandates that latSpan and lonSpan must evaluate exactly to 0
	// if an agency contains no stop records.
	assert.Equal(t, 0.0, mockAgency.Lat)
	assert.Equal(t, 0.0, mockAgency.Lon)
	assert.Equal(t, 0.0, mockAgency.LatSpan)
	assert.Equal(t, 0.0, mockAgency.LonSpan)
}
