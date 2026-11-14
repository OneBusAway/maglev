package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestAgenciesWithCoverageHandlerValidatesReferencesBeforeDatabaseAccess(t *testing.T) {
	// No GTFS manager is configured: invalid input must return before accessing it.
	api := &RestAPI{Application: &app.Application{Clock: clock.RealClock{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/where/agencies-with-coverage.json?includeReferences=abc", nil)
	recorder := httptest.NewRecorder()

	api.agenciesWithCoverageHandler(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var model strictValidationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &model))
	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.NotEmpty(t, model.Data.FieldErrors["includeReferences"])
}

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

func TestAgenciesWithCoverageHandlerValidatesPaginationBeforeDatabaseAccess(t *testing.T) {
	// No GTFS manager is configured: invalid input must return before accessing it.
	api := &RestAPI{Application: &app.Application{Clock: clock.RealClock{}}}
	req := httptest.NewRequest(http.MethodGet, "/api/where/agencies-with-coverage.json?maxCount=2147483648", nil)
	recorder := httptest.NewRecorder()

	api.agenciesWithCoverageHandler(recorder, req)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var model strictValidationResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &model))
	assert.Equal(t, http.StatusBadRequest, model.Code)
	assert.Contains(t, model.Data.FieldErrors["maxCount"], `Invalid field value for field "maxCount".`)
}

func TestAgenciesWithCoverageHandler_PaginationValidation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	api.rateLimiter = NewRateLimitMiddleware(10000, time.Second, nil)

	tests := []struct {
		name            string
		query           string
		expectedStatus  int
		expectError     bool
		errorParam      string
		expectedMessage string
	}{
		{
			name:            "maxCount overflow above int32",
			query:           "maxCount=2147483648",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "maxCount",
			expectedMessage: `Invalid field value for field "maxCount".`,
		},
		{
			name:            "maxCount underflow below int32",
			query:           "maxCount=-2147483649",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "maxCount",
			expectedMessage: `Invalid field value for field "maxCount".`,
		},
		{
			name:            "maxCount non-numeric string",
			query:           "maxCount=abc",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "maxCount",
			expectedMessage: `Invalid field value for field "maxCount".`,
		},
		{
			name:            "maxCount floating point string",
			query:           "maxCount=12.34",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "maxCount",
			expectedMessage: `Invalid field value for field "maxCount".`,
		},
		{
			name:            "offset overflow above int32",
			query:           "offset=2147483648",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "offset",
			expectedMessage: `Invalid field value for field "offset".`,
		},
		{
			name:            "offset non-numeric string",
			query:           "offset=abc",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "offset",
			expectedMessage: `Invalid field value for field "offset".`,
		},
		{
			name:            "limit overflow above int32",
			query:           "limit=2147483648",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "limit",
			expectedMessage: `Invalid field value for field "limit".`,
		},
		{
			name:            "limit non-numeric string",
			query:           "limit=abc",
			expectedStatus:  http.StatusBadRequest,
			expectError:     true,
			errorParam:      "limit",
			expectedMessage: `Invalid field value for field "limit".`,
		},
		{
			name:           "maxCount valid within int32",
			query:          "maxCount=5",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "maxCount max signed int32 boundary",
			query:          "maxCount=2147483647",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "maxCount negative disables filtering",
			query:          "maxCount=-1",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "maxCount zero disables filtering",
			query:          "maxCount=0",
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := "/api/where/agencies-with-coverage.json?key=TEST&" + tt.query
			if tt.expectError {
				resp, model := callAPIHandler[strictValidationResponse](t, api, path)
				assert.Equal(t, tt.expectedStatus, resp.StatusCode)
				assert.Equal(t, tt.expectedStatus, model.Code)
				require.Contains(t, model.Data.FieldErrors, tt.errorParam)
				assert.Contains(t, model.Data.FieldErrors[tt.errorParam], tt.expectedMessage)
			} else {
				resp, model := callAPIHandler[CoverageResponse](t, api, path)
				assert.Equal(t, tt.expectedStatus, resp.StatusCode)
				assert.Equal(t, tt.expectedStatus, model.Code)
				assert.Equal(t, "OK", model.Text)
			}
		})
	}
}
