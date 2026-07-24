package restapi

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
	"maglev.onebusaway.org/internal/utils"
)

func routeSearchURL(params url.Values) string {
	q := url.Values{"key": {"TEST"}}
	maps.Copy(q, params)
	return "/api/where/search/route.json?" + q.Encode()
}

func TestRouteSearchHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api,
		routeSearchURL(url.Values{"input": {"1"}, "key": {"invalid"}}))

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestRouteSearchHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"shasta"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	require.NotEmpty(t, model.Data.List)

	found := false
	for _, route := range model.Data.List {
		if route.ShortName == "17" {
			assert.True(t, strings.Contains(strings.ToLower(route.LongName), "shasta"))
			found = true
		}
	}
	assert.True(t, found, "expected Shasta route to be returned")

	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
}

func TestRouteSearchHandlerRequiresInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {""}}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSearchHandlerValidatesMaxCount(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"1"}, "maxCount": {"-1"}}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSearchHandlerNoResults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"zzzznonexistent99999"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Empty(t, model.Data.List)
}

func TestRouteSearchHandlerWhitespaceInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"   "}}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSearchHandlerMaxCountBoundaries(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"shasta"}, "maxCount": {"100"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	resp, _ = callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"shasta"}, "maxCount": {"101"}}))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSearchHandlerSorting(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"1"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List)

	// Ensure routes are sorted by natural short name order
	isSortedRoutes := true
	for i := 1; i < len(model.Data.List); i++ {
		prev := model.Data.List[i-1]
		curr := model.Data.List[i]

		namePrev := prev.ShortName
		if namePrev == "" {
			namePrev = prev.LongName
		}

		nameCurr := curr.ShortName
		if nameCurr == "" {
			nameCurr = curr.LongName
		}

		if utils.NaturalCompare(namePrev, nameCurr) > 0 {
			isSortedRoutes = false
			break
		}
	}
	assert.True(t, isSortedRoutes, "Routes should be sorted by short name")

	// Ensure agencies are sorted by ID
	isSortedAgencies := true
	for i := 1; i < len(model.Data.References.Agencies); i++ {
		if strings.Compare(model.Data.References.Agencies[i-1].ID, model.Data.References.Agencies[i].ID) > 0 {
			isSortedAgencies = false
			break
		}
	}
	assert.True(t, isSortedAgencies, "Agencies should be sorted by ID")
}
