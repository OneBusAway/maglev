package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

// routeURL builds the /route endpoint URL with key=TEST baked in. Tests that
// want a different key (auth checks) build their URL inline.
func routeURL(routeID string) string {
	return "/api/where/route/" + routeID + ".json?key=TEST"
}

func TestRouteHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteEntryResponse](t, api,
		"/api/where/route/"+testdata.Route1.ID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RouteEntryResponse](t, api, routeURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	assert.Equal(t, testdata.Route1, model.Data.Entry)
	assert.Equal(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
}

// TestRouteHandler_NotFoundCases covers two 404 paths:
// - valid agency + unknown route code (sql.ErrNoRows from GetRoute)
// - unknown agency (rejected upstream by agency lookup)
func TestRouteHandler_NotFoundCases(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name    string
		routeID string
	}{
		{"Unknown route code", utils.FormCombinedID(testdata.Raba.ID, "invalid_route_id")},
		{"Unknown agency", utils.FormCombinedID("nonexistent_agency", "1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[RouteEntryResponse](t, api, routeURL(tt.routeID))

			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.Equal(t, http.StatusNotFound, model.Code)
			assert.Equal(t, "resource not found", model.Text)
		})
	}
}

func TestRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Hyphen is allowed by ValidateID but '-' isn't the agency-code separator,
	// so the ID has no underscore and ExtractAgencyIDAndCodeID rejects it.
	resp, model := callAPIHandler[RouteEntryResponse](t, api, routeURL("1-SHUTTLE"))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

// TestRouteHandler_EntityIDWithUnderscores verifies that the handler correctly
// processes IDs where the entity portion contains underscores (e.g. KCM_40_100479
// splits into agency "KCM" and entity "40_100479" via SplitN with limit 2).
func TestRouteHandler_EntityIDWithUnderscores(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name    string
		routeID string
	}{
		{"agency with underscore in entity", "KCM_40_100479"},
		{"existing agency with underscore in entity", "25_40_100479"},
		{"multiple underscores in entity", "AGENCY_part1_part2_part3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[RouteEntryResponse](t, api, routeURL(tt.routeID))
			assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			assert.Equal(t, http.StatusNotFound, model.Code)
		})
	}
}

// TestRouteHandlerWithSituations verifies that a real-time alert informing a
// route shows up in references.situations for that route's response.
func TestRouteHandlerWithSituations(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Real-time alerts use the raw (un-prefixed) route ID from the GTFS-RT feed.
	rawRouteID := "151" // Route1 = "25_151"
	const alertID = "test-alert-123"
	api.GtfsManager.AddAlertForTest(gtfs.Alert{
		ID: alertID,
		InformedEntities: []gtfs.AlertInformedEntity{
			{RouteID: &rawRouteID},
		},
		Header: []gtfs.AlertText{
			{Text: "Test Route Alert", Language: "en"},
		},
	})

	resp, model := callAPIHandler[RouteEntryResponse](t, api, routeURL(testdata.Route1.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.References.Situations, 1,
		"expected exactly one situation matching the seeded alert")
	assert.Equal(t, alertID, model.Data.References.Situations[0].ID)
}

// TestRouteHandler_IncludeReferencesFalse verifies that when includeReferences=false,
// the response contains an empty agencies array and skips the agency database lookup.
func TestRouteHandler_IncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	url := "/api/where/route/" + testdata.Route1.ID + ".json?key=TEST&includeReferences=false"
	resp, model := callAPIHandler[RouteEntryResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, testdata.Route1, model.Data.Entry)
	assert.Empty(t, model.Data.References.Agencies,
		"agencies should be empty when includeReferences=false")
}

// TestRouteHandler_IncludeReferencesDefault verifies that the default behaviour
// (includeReferences absent or explicitly true) returns the owning agency.
func TestRouteHandler_IncludeReferencesDefault(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name string
		url  string
	}{
		{"absent", routeURL(testdata.Route1.ID)},
		{"explicit true", "/api/where/route/" + testdata.Route1.ID + ".json?key=TEST&includeReferences=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[RouteEntryResponse](t, api, tt.url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.Len(t, model.Data.References.Agencies, 1,
				"agencies should contain the owning agency")
			assert.Equal(t, testdata.Raba.ID, model.Data.References.Agencies[0].ID)
		})
	}
}
