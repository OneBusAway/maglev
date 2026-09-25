package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// flexOnlyJSONKeys must never appear in a /where response for a feed without flex data.
var flexOnlyJSONKeys = []string{"onDemandServiceIds", "serviceAreas", "locationGroups", "bookingRules", "calendars"}

// whereKeyPathsSnapshot is the committed key-path set per endpoint.
var whereKeyPathsSnapshot = filepath.Join("testdata", "where_keypaths.json")

// regressionClock is noon PDT on Thursday 2025-06-12, a service day of RABA's
// c_1658_b_18260_d_31 calendar, so time-windowed endpoints are deterministic.
var regressionClock = time.Date(2025, 6, 12, 19, 0, 0, 0, time.UTC)

// whereRegressionEndpoints is the fixed endpoint set; names key the snapshot.
var whereRegressionEndpoints = []struct{ name, endpoint string }{
	{"agencies-with-coverage", "/api/where/agencies-with-coverage.json"},
	{"agency", "/api/where/agency/25.json"},
	{"routes-for-agency", "/api/where/routes-for-agency/25.json"},
	{"route", "/api/where/route/25_151.json"},
	{"stop", "/api/where/stop/25_1030.json"},
	{"stops-for-location", "/api/where/stops-for-location.json?lat=40.58&lon=-122.39"},
	{"routes-for-location", "/api/where/routes-for-location.json?lat=40.58&lon=-122.39"},
	{"stops-for-route", "/api/where/stops-for-route/25_151.json"},
	{"stops-for-agency", "/api/where/stops-for-agency/25.json"},
	{"schedule-for-stop", "/api/where/schedule-for-stop/25_1030.json?date=2025-06-12"},
	{"schedule-for-route", "/api/where/schedule-for-route/25_151.json?date=2025-06-12"},
	{"trip", "/api/where/trip/25_84f4520e-88b6-4ee6-8975-856799bc1359.json"},
	{"trip-details", "/api/where/trip-details/25_84f4520e-88b6-4ee6-8975-856799bc1359.json"},
	{"trips-for-route", "/api/where/trips-for-route/25_151.json"},
	{"trips-for-location", "/api/where/trips-for-location.json?lat=40.58&lon=-122.39&latSpan=0.2&lonSpan=0.2"},
	{"arrivals-and-departures-for-stop", "/api/where/arrivals-and-departures-for-stop/25_1030.json"},
	{"block", "/api/where/block/25_1.json"},
	{"shape", "/api/where/shape/25_t580.json"},
	{"search-stop", "/api/where/search/stop.json?input=Shasta"},
	{"search-route", "/api/where/search/route.json?input=Churn"},
}

// containsQuery reports whether endpoint already has a query string, so the
// test knows whether to append the API key with "?" or "&".
func containsQuery(endpoint string) bool {
	return strings.Contains(endpoint, "?")
}

// collectKeyPaths records every JSON key path in the document, using "[]" for
// array elements, and reports any flex-only key it meets.
func collectKeyPaths(value any, prefix string, paths map[string]struct{}, flexKeys *[]string) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			for _, flexKey := range flexOnlyJSONKeys {
				if key == flexKey {
					*flexKeys = append(*flexKeys, prefix+key)
				}
			}
			path := prefix + key
			paths[path] = struct{}{}
			collectKeyPaths(child, path+".", paths, flexKeys)
		}
	case []any:
		for _, child := range v {
			collectKeyPaths(child, prefix[:len(prefix)-1]+"[].", paths, flexKeys)
		}
	}
}

func TestWhereResponses_StayAdditiveOnNonFlexFeed(t *testing.T) {
	api := createTestApiWithFeedAndClock(t, filepath.Join("../../testdata", "raba.zip"), clock.NewMockClock(regressionClock))
	defer api.Shutdown()
	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	observed := make(map[string][]string, len(whereRegressionEndpoints))
	for _, tt := range whereRegressionEndpoints {
		t.Run(tt.name, func(t *testing.T) {
			separator := "?"
			if containsQuery(tt.endpoint) {
				separator = "&"
			}
			// org.onebusaway.iphone is exempt from rate limiting (see
			// createTestApiWithClock); this test fires 20 sequential requests
			// against one shared RestAPI/limiter, which would otherwise trip
			// the per-key rate limiter well before all endpoints are hit.
			resp, err := http.Get(server.URL + tt.endpoint + separator + "key=org.onebusaway.iphone")
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

			var document any
			require.NoError(t, json.Unmarshal(body, &document))
			paths := make(map[string]struct{})
			var flexKeys []string
			collectKeyPaths(document, "", paths, &flexKeys)
			assert.Empty(t, flexKeys, "a non-flex feed must never emit a flex key on /where")

			sorted := make([]string, 0, len(paths))
			for path := range paths {
				sorted = append(sorted, path)
			}
			sort.Strings(sorted)
			observed[tt.name] = sorted
		})
	}

	if os.Getenv("UPDATE_WHERE_KEYPATHS") == "1" {
		encoded, err := json.MarshalIndent(observed, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(whereKeyPathsSnapshot, append(encoded, '\n'), 0o644))
	}

	snapshotBytes, err := os.ReadFile(whereKeyPathsSnapshot)
	require.NoError(t, err, "run with UPDATE_WHERE_KEYPATHS=1 to generate the snapshot")
	var snapshot map[string][]string
	require.NoError(t, json.Unmarshal(snapshotBytes, &snapshot))
	for _, tt := range whereRegressionEndpoints {
		assert.Equal(t, snapshot[tt.name], observed[tt.name], "key paths changed for %s; if intended, regenerate with UPDATE_WHERE_KEYPATHS=1 and review the diff", tt.name)
	}
	for name := range snapshot {
		_, stillTested := observed[name]
		assert.True(t, stillTested, "snapshot has an endpoint the test no longer covers: %s", name)
	}
}
