package restapi

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

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
	assert.Equal(t, models.APIVersion, model.Version)
	assert.NotZero(t, model.CurrentTime)
	assert.Empty(t, model.Data.References.Routes)
	assert.False(t, model.Data.OutOfRange, "search-route performs no geographic bounding; outOfRange is always false")

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

func TestRouteSearchHandlerMissingInputParam(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, _ := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{}))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSearchHandlerValidatesMaxCount(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name       string
		maxCount   string
		wantStatus int
	}{
		{"negative", "-1", http.StatusBadRequest},
		{"zero", "0", http.StatusBadRequest},
		{"non-numeric", "abc", http.StatusBadRequest},
		{"at max allowed", "250", http.StatusOK},
		{"above max allowed", "251", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := callAPIHandler[RoutesResponse](t, api,
				routeSearchURL(url.Values{"input": {"shasta"}, "maxCount": {tt.maxCount}}))
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

func TestRouteSearchHandlerNoResults(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"zzzznonexistent99999"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List)
}

func TestRouteSearchHandlerWhitespaceInput(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"   "}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, model.Data.List)
}

func TestRouteSearchHandlerLimitExceeded(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Using a generic search term that should match multiple routes
	// Setting maxCount=1 should force limitExceeded=true
	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"1"}, "maxCount": {"1"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	assert.True(t, model.Data.LimitExceeded, "limitExceeded should be true when results are truncated")
	assert.Equal(t, 1, len(model.Data.List), "results should be truncated to maxCount")
}

func TestRouteSearchHandlerLimitNotExceeded(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"shasta"}, "maxCount": {"250"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.False(t, model.Data.LimitExceeded, "limitExceeded should be false when results fit within maxCount")
}

func TestRouteSearchHandlerIncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"shasta"}, "includeReferences": {"false"}}))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.NotEmpty(t, model.Data.List)

	// When includeReferences is false, the references block should be completely empty
	assert.Empty(t, model.Data.References.Agencies)
	assert.Empty(t, model.Data.References.Routes)
	assert.Empty(t, model.Data.References.Situations)
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

// TestRouteSearchHandlerPaginationBoundary guards against sorting being applied
// after pagination truncates the FTS5 relevance-ordered results, which would let
// a route that belongs on the first sorted page get dropped in favor of one that
// doesn't.
func TestRouteSearchHandlerPaginationBoundary(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, fullModel := callAPIHandler[RoutesResponse](t, api, routeSearchURL(url.Values{"input": {"1"}, "maxCount": {"250"}}))
	require.GreaterOrEqual(t, len(fullModel.Data.List), 3, "need multiple matches to exercise the pagination boundary")

	tests := []struct {
		name     string
		maxCount int
	}{
		{"first page of four", 4},
		{"first page of five", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[RoutesResponse](t, api,
				routeSearchURL(url.Values{"input": {"1"}, "maxCount": {strconv.Itoa(tt.maxCount)}}))

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.Len(t, model.Data.List, tt.maxCount)
			assert.True(t, model.Data.LimitExceeded)

			for i, route := range model.Data.List {
				assert.Equal(t, fullModel.Data.List[i].ID, route.ID,
					"page should match the natural-sort prefix of the full result set")
			}
		})
	}
}

func TestRouteSearchHandlerContextCancellation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	req, err := http.NewRequest("GET", routeSearchURL(url.Values{"input": {"shasta"}}), nil)
	require.NoError(t, err)
	// Use a deadline in the past — context.Err() is DeadlineExceeded immediately,
	// no timer resolution dependency (avoids Windows ~15ms minimum sleep issue).
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGatewayTimeout, w.Code)
}
