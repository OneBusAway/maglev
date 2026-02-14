package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgenciesWithCoverageHandlerRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestAgenciesWithCoverageHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 1)

	agencyCoverage, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agencyCoverage["agencyId"])
	assert.InDelta(t, 40.328705, agencyCoverage["lat"], 1e-8)
	assert.InDelta(t, 1.2188699999999955, agencyCoverage["latSpan"], 1e-8)
	assert.InDelta(t, -122.101745, agencyCoverage["lon"], 1e-8)
	assert.InDelta(t, 0.9914899999999989, agencyCoverage["lonSpan"], 1e-8)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	refAgencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, refAgencies, 1)

	agencyRef, ok := refAgencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agencyRef["id"])
	assert.Equal(t, "Redding Area Bus Authority", agencyRef["name"])
	assert.Equal(t, "http://www.rabaride.com/", agencyRef["url"])
	assert.Equal(t, "America/Los_Angeles", agencyRef["timezone"])
	assert.Equal(t, "en", agencyRef["lang"])
	assert.Equal(t, "530-241-2877", agencyRef["phone"])
	assert.Equal(t, "", agencyRef["email"])
	assert.Equal(t, "", agencyRef["fareUrl"])
	assert.Equal(t, "", agencyRef["disclaimer"])
	assert.False(t, agencyRef["privateService"].(bool))
	// Ensure no extra fields
	assert.Len(t, agencyRef, 10)

	assert.Empty(t, refs["routes"])
	assert.Empty(t, refs["situations"])
	assert.Empty(t, refs["stopTimes"])
	assert.Empty(t, refs["stops"])
	assert.Empty(t, refs["trips"])
}

func TestAgenciesWithCoverageHandlerPagination(t *testing.T) {
	// Test data (raba.zip) has 1 agency

	// Case 1: Default (Offset 0, Limit -1) -> Should return 1
	_, _, model1 := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=TEST")
	data1, ok := model1.Data.(map[string]interface{})
	require.True(t, ok, "expected Data to be map[string]interface{}")
	list1, ok := data1["list"].([]interface{})
	require.True(t, ok, "expected list to be []interface{}")
	assert.Len(t, list1, 1)

	// Case 2: Limit 1 -> Should return 1
	_, _, model2 := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=TEST&limit=1")
	data2, ok := model2.Data.(map[string]interface{})
	require.True(t, ok, "expected Data to be map[string]interface{}")
	list2, ok := data2["list"].([]interface{})
	require.True(t, ok, "expected list to be []interface{}")
	assert.Len(t, list2, 1)

	// Case 3: Limit 0 (should default to -1/all) -> Should return 1
	// Note: Our new logic treats limit=0 as invalid -> -1 (all)
	_, _, model3 := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=TEST&limit=0")
	data3, ok := model3.Data.(map[string]interface{})
	require.True(t, ok, "expected Data to be map[string]interface{}")
	list3, ok := data3["list"].([]interface{})
	require.True(t, ok, "expected list to be []interface{}")
	assert.Len(t, list3, 1)

	// Case 4: Offset 1 -> Should return 0
	_, _, model4 := serveAndRetrieveEndpoint(t, "/api/where/agencies-with-coverage.json?key=TEST&offset=1")
	data4, ok := model4.Data.(map[string]interface{})
	require.True(t, ok, "expected Data to be map[string]interface{}")
	list4, ok := data4["list"].([]interface{})
	require.True(t, ok, "expected list to be []interface{}")
	assert.Len(t, list4, 0)
}
