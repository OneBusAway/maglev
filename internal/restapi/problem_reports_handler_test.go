package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exemptKey is rate-limit exempt (configured in createTestApiWithClock).
const problemReportExemptKey = "org.onebusaway.iphone"

func TestProblemReportsRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/problem-reports.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestProblemReports_EmptyList(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Filter by a unique code that no test uses to guarantee an empty result.
	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/problem-reports.json?key="+problemReportExemptKey+"&code=nonexistent_code_xyz_unique")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	list, ok := data["list"].([]interface{})
	require.True(t, ok, "Data should contain a list")
	assert.Empty(t, list, "List should be empty for an unused code")
	assert.Equal(t, false, data["limitExceeded"])
}

// submitTripReportExempt stores a trip problem report using the exempt API key.
func submitTripReportExempt(t *testing.T, api *RestAPI, tripID, code, comment string) {
	t.Helper()
	url := fmt.Sprintf(
		"/api/where/report-problem-with-trip/%s.json?key=%s&code=%s&userComment=%s",
		tripID, problemReportExemptKey, code, comment,
	)
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	require.Equal(t, http.StatusOK, resp.StatusCode, "trip report submission failed")
	require.Equal(t, 200, model.Code, "trip report submission failed")
}

// submitStopReportExempt stores a stop problem report using the exempt API key.
func submitStopReportExempt(t *testing.T, api *RestAPI, stopID, code, comment string) {
	t.Helper()
	url := fmt.Sprintf(
		"/api/where/report-problem-with-stop/%s.json?key=%s&code=%s&userComment=%s",
		stopID, problemReportExemptKey, code, comment,
	)
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	require.Equal(t, http.StatusOK, resp.StatusCode, "stop report submission failed")
	require.Equal(t, 200, model.Code, "stop report submission failed")
}

// getReportListExempt hits the general problem-reports endpoint using the exempt
// key and returns the list items + the full data map.
func getReportListExempt(t *testing.T, api *RestAPI, query string) ([]interface{}, map[string]interface{}) {
	t.Helper()
	url := "/api/where/problem-reports.json?key=" + problemReportExemptKey
	if query != "" {
		url += "&" + query
	}
	resp, model := serveApiAndRetrieveEndpoint(t, api, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")

	list, ok := data["list"].([]interface{})
	require.True(t, ok, "Data should contain a list")
	return list, data
}

func TestProblemReports_CombinesTripAndStopReports(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "combine_test_unique_001"
	submitTripReportExempt(t, api, "1_12345", uniqueCode, "Trip+report")
	submitStopReportExempt(t, api, "1_75403", uniqueCode, "Stop+report")

	list, _ := getReportListExempt(t, api, "code="+uniqueCode)
	require.Len(t, list, 2, "Should return both trip and stop reports")

	reportTypes := make(map[string]bool)
	for _, item := range list {
		r, ok := item.(map[string]interface{})
		require.True(t, ok)
		rt, ok := r["reportType"].(string)
		require.True(t, ok, "each item should have a reportType field")
		reportTypes[rt] = true
	}
	assert.True(t, reportTypes["trip"], "should include a trip report")
	assert.True(t, reportTypes["stop"], "should include a stop report")
}

func TestProblemReports_FilterByCode(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tripCode := "filter_code_trip_002"
	stopCode := "filter_code_stop_002"

	submitTripReportExempt(t, api, "1_12345", tripCode, "First")
	submitStopReportExempt(t, api, "1_75403", stopCode, "Second")
	submitTripReportExempt(t, api, "1_12346", tripCode, "Third")

	// Filter by tripCode should return only the two trip reports.
	list, _ := getReportListExempt(t, api, "code="+tripCode)
	require.Len(t, list, 2, "Should only return reports with the specified code")

	for _, item := range list {
		r, ok := item.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "trip", r["reportType"], "all filtered results should be trip reports")
		tr, ok := r["tripReport"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, tripCode, tr["code"])
	}

	// Filter by stopCode should return only the one stop report.
	list2, _ := getReportListExempt(t, api, "code="+stopCode)
	require.Len(t, list2, 1, "Should only return the stop report")
	r, ok := list2[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "stop", r["reportType"])
}

func TestProblemReports_Pagination_Limit(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "pagination_limit_003"
	for i := 0; i < 3; i++ {
		submitTripReportExempt(t, api, fmt.Sprintf("1_%d", 30000+i), uniqueCode, "Report")
	}

	list, _ := getReportListExempt(t, api, "code="+uniqueCode+"&limit=2")
	assert.Len(t, list, 2, "limit=2 should cap the result at 2 items")
}

func TestProblemReports_Pagination_Offset(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "pagination_offset_004"
	for i := 0; i < 3; i++ {
		submitTripReportExempt(t, api, fmt.Sprintf("1_%d", 40000+i), uniqueCode, "Report")
	}

	all, _ := getReportListExempt(t, api, "code="+uniqueCode)
	require.Len(t, all, 3, "should have 3 reports total")

	offset1, _ := getReportListExempt(t, api, "code="+uniqueCode+"&offset=1")
	assert.Len(t, offset1, 2, "offset=1 should return items 2 and 3")

	offset3, _ := getReportListExempt(t, api, "code="+uniqueCode+"&offset=3")
	assert.Empty(t, offset3, "offset=3 should return empty list when only 3 items exist")
}

func TestProblemReports_LimitExceeded(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "limit_exceeded_005"
	for i := 0; i < 3; i++ {
		submitTripReportExempt(t, api, fmt.Sprintf("1_%d", 50000+i), uniqueCode, "Report")
	}

	// Requesting limit=2 with 3 results available should set limitExceeded=true.
	_, data := getReportListExempt(t, api, "code="+uniqueCode+"&limit=2")
	assert.Equal(t, true, data["limitExceeded"], "limitExceeded should be true when more results exist")

	// Requesting limit=3 with 3 results available should set limitExceeded=false.
	_, data = getReportListExempt(t, api, "code="+uniqueCode+"&limit=3")
	assert.Equal(t, false, data["limitExceeded"], "limitExceeded should be false when all results fit")
}

func TestProblemReports_ReferencesPresent(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "references_test_006"
	submitTripReportExempt(t, api, "1_12345", uniqueCode, "TripReport")
	submitStopReportExempt(t, api, "1_75403", uniqueCode, "StopReport")

	_, data := getReportListExempt(t, api, "code="+uniqueCode)

	// Verify references structure exists and is a map.
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "references should be present in response")

	// Verify that references has the standard OBA keys (even if empty slices).
	_, hasAgencies := refs["agencies"]
	_, hasRoutes := refs["routes"]
	_, hasStops := refs["stops"]
	_, hasTrips := refs["trips"]
	assert.True(t, hasAgencies, "references should have agencies key")
	assert.True(t, hasRoutes, "references should have routes key")
	assert.True(t, hasStops, "references should have stops key")
	assert.True(t, hasTrips, "references should have trips key")
}

func TestProblemReports_SortedByCreatedAtDesc(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	uniqueCode := "sort_test_007"
	// Submit 3 reports sequentially; each should have an increasing created_at.
	submitTripReportExempt(t, api, "1_12345", uniqueCode, "First")
	submitStopReportExempt(t, api, "1_75403", uniqueCode, "Second")
	submitTripReportExempt(t, api, "1_12346", uniqueCode, "Third")

	list, _ := getReportListExempt(t, api, "code="+uniqueCode)
	require.Len(t, list, 3)

	// Verify the list is sorted by created_at in descending order.
	var prevCreatedAt float64
	for i, item := range list {
		r, ok := item.(map[string]interface{})
		require.True(t, ok)

		var createdAt float64
		if r["reportType"] == "trip" {
			tr, ok := r["tripReport"].(map[string]interface{})
			require.True(t, ok)
			createdAt, ok = tr["createdAt"].(float64)
			require.True(t, ok)
		} else {
			sr, ok := r["stopReport"].(map[string]interface{})
			require.True(t, ok)
			createdAt, ok = sr["createdAt"].(float64)
			require.True(t, ok)
		}

		if i > 0 {
			assert.GreaterOrEqual(t, prevCreatedAt, createdAt,
				"reports should be sorted by created_at descending")
		}
		prevCreatedAt = createdAt
	}
}
