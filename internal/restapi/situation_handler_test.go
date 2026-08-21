package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSituationHandlerRequiresValidAPIKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/test-alert.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestSituationHandlerErrors(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name           string
		pathID         string
		expectedStatus int
		expectedText   string
	}{
		{
			name:           "unknown but well-formed ID",
			pathID:         "25_nonexistent-alert",
			expectedStatus: http.StatusNotFound,
			expectedText:   "resource not found",
		},
		{
			name:           "malformed ID without agency separator",
			pathID:         "nonexistent-alert",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid characters",
			pathID:         "bad*id",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/"+tt.pathID+".json?key=TEST")
			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Equal(t, tt.expectedStatus, model.Code)
			if tt.expectedText != "" {
				assert.Equal(t, tt.expectedText, model.Text)
			}
		})
	}
}

func TestSituationHandlerWithSituation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := "25"
	const rawAlertID = "situation-handler-alert"
	qualifiedID := "25_situation-handler-alert"
	alert := gtfs.Alert{
		ID: rawAlertID,
		InformedEntities: []gtfs.AlertInformedEntity{
			{AgencyID: &agencyID},
		},
		Header: []gtfs.AlertText{
			{Text: "Service disruption", Language: "en"},
		},
		Description: []gtfs.AlertText{
			{Text: "Detour in effect", Language: "en"},
		},
	}
	api.GtfsManager.AddAlertForTest(alert)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/"+qualifiedID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response should include data object")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "response should include data.entry object")
	assert.Equal(t, qualifiedID, entry["id"])
	assert.Equal(t, "UNKNOWN_CAUSE", entry["reason"])
	assert.Equal(t, "noImpact", entry["severity"])

	summary, ok := entry["summary"].(map[string]interface{})
	require.True(t, ok, "entry should include summary")
	assert.Equal(t, "Service disruption", summary["value"])
	assert.Equal(t, "en", summary["lang"])

	description, ok := entry["description"].(map[string]interface{})
	require.True(t, ok, "entry should include description")
	assert.Equal(t, "Detour in effect", description["value"])
	assert.Equal(t, "en", description["lang"])

	references, ok := data["references"].(map[string]interface{})
	require.True(t, ok, "response should include data.references object")

	agencies, ok := references["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, agencies, 0)

	routes, ok := references["routes"].([]interface{})
	require.True(t, ok)
	assert.Len(t, routes, 0)

	stops, ok := references["stops"].([]interface{})
	require.True(t, ok)
	assert.Len(t, stops, 0)
}

func TestSituationHandlerMatchesPrefixedAlertID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	const prefixedID = "40_situation-handler-prefixed"
	api.GtfsManager.AddAlertForTest(gtfs.Alert{
		ID: prefixedID,
		Header: []gtfs.AlertText{
			{Text: "Already prefixed", Language: "en"},
		},
	})

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/situation/"+prefixedID+".json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, prefixedID, entry["id"])
}
