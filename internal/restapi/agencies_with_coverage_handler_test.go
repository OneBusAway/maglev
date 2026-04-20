package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

type CoverageResponse struct {
	Code        int          `json:"code"`
	CurrentTime int64        `json:"currentTime"`
	Data        CoverageData `json:"data"`
	Text        string       `json:"text"`
	Version     int          `json:"version"`
}

type CoverageData struct {
	LimitExceeded bool                    `json:"limitExceeded"`
	List          []models.AgencyCoverage `json:"list"`
	OutOfRange    bool                    `json:"outOfRange"`
	References    models.ReferencesModel  `json:"references"`
}

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

func TestAgenciesWithCoverageHandlerResponseEnvelope(t *testing.T) {
	api := createTestApi(t)

	_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST")

	assert.Equal(t, 2, model.Version, "envelope version must be 2")
	assert.Equal(t, 200, model.Code, "code must mirror HTTP status")
	assert.Equal(t, "OK", model.Text)
	assert.Greater(t, model.CurrentTime, int64(0), "currentTime must be a positive Unix ms timestamp")
}

func TestAgenciesWithCoverageHandlerVersionParameter(t *testing.T) {
	api := createTestApi(t)

	t.Run("version=2 accepted", func(t *testing.T) {
		resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&version=2")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 200, model.Code)
		assert.Len(t, model.Data.List, 1)
	})

	t.Run("version=1 accepted", func(t *testing.T) {
		resp, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&version=1")
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 200, model.Code)
	})

	t.Run("version=3 returns 500", func(t *testing.T) {
		resp, model := callAPIHandler[models.ResponseModel](t, api, "/api/where/agencies-with-coverage.json?key=TEST&version=3")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, 500, model.Code)
		assert.Equal(t, "unknown version: 3", model.Text)
		assert.Nil(t, model.Data, "data must be null on version error")
	})

	t.Run("version=0 returns 500", func(t *testing.T) {
		resp, model := callAPIHandler[models.ResponseModel](t, api, "/api/where/agencies-with-coverage.json?key=TEST&version=0")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "unknown version: 0", model.Text)
	})

	t.Run("version=abc returns 500", func(t *testing.T) {
		resp, model := callAPIHandler[models.ResponseModel](t, api, "/api/where/agencies-with-coverage.json?key=TEST&version=abc")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "unknown version: abc", model.Text)
		assert.Nil(t, model.Data)
	})
}

func TestAgenciesWithCoverageHandlerIncludeReferences(t *testing.T) {
	api := createTestApi(t)

	t.Run("default includes references", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST")
		require.NotEmpty(t, model.Data.References.Agencies, "references.agencies must be populated by default")
		assert.Equal(t, "25", model.Data.References.Agencies[0].ID)
	})

	t.Run("includeReferences=true includes references", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&includeReferences=true")
		require.NotEmpty(t, model.Data.References.Agencies)
	})

	t.Run("includeReferences=false omits references", func(t *testing.T) {
		_, raw := callAPIHandler[map[string]any](t, api, "/api/where/agencies-with-coverage.json?key=TEST&includeReferences=false")
		data, ok := raw["data"].(map[string]any)
		require.True(t, ok, "data must be an object")

		_, hasReferences := data["references"]
		assert.False(t, hasReferences, "references key must be absent when includeReferences=false")

		list, hasList := data["list"].([]any)
		assert.True(t, hasList, "list must still be present")
		assert.Len(t, list, 1, "all agencies should still be returned")

		_, hasLimitExceeded := data["limitExceeded"]
		assert.True(t, hasLimitExceeded, "limitExceeded must still be present")
	})

	t.Run("includeReferences=false still populates agencyId in list", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&includeReferences=false")
		require.Len(t, model.Data.List, 1)
		assert.Equal(t, "25", model.Data.List[0].AgencyID, "agencyId must be present even without references")
	})
}

func TestAgenciesWithCoverageHandlerMaxCount(t *testing.T) {
	// Test data has 1 agency — maxCount is accepted via ParsePaginationParams
	api := createTestApi(t)

	t.Run("maxCount=1 returns all when count fits", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&maxCount=1")
		assert.Len(t, model.Data.List, 1)
		assert.False(t, model.Data.LimitExceeded)
	})

	t.Run("maxCount is ignored when invalid", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&maxCount=abc")
		assert.Len(t, model.Data.List, 1, "invalid maxCount should be ignored, returning all")
	})

	t.Run("maxCount=0 is ignored", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&maxCount=0")
		assert.Len(t, model.Data.List, 1, "maxCount=0 should be ignored, returning all")
	})
}

func TestAgenciesWithCoverageHandlerMissingApiKey(t *testing.T) {
	api := createTestApi(t)

	resp, model := callAPIHandler[models.ResponseModel](t, api, "/api/where/agencies-with-coverage.json")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestAgenciesWithCoverageHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)

	t.Run("false when all agencies fit within limit", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST")
		assert.False(t, model.Data.LimitExceeded)
	})

	t.Run("false when no limit set", func(t *testing.T) {
		_, model := callAPIHandler[CoverageResponse](t, api, "/api/where/agencies-with-coverage.json?key=TEST&maxCount=100")
		assert.False(t, model.Data.LimitExceeded)
	})
}
