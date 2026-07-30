package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/utils"
)

func TestDeduplicateAlerts(t *testing.T) {
	alert1 := gtfs.Alert{ID: "alert-1"}
	alert2 := gtfs.Alert{ID: "alert-2"}
	alert3 := gtfs.Alert{ID: "alert-3"}

	slice1 := []gtfs.Alert{alert1, alert2}
	slice2 := []gtfs.Alert{alert2, alert3}
	slice3 := []gtfs.Alert{alert1, alert3}

	result := deduplicateAlerts(slice1, slice2, slice3)

	assert.Len(t, result, 3, "Should deduplicate and return exactly 3 unique alerts")

	idMap := make(map[string]bool)
	for _, a := range result {
		idMap[a.ID] = true
	}

	assert.True(t, idMap["alert-1"], "Missing alert-1")
	assert.True(t, idMap["alert-2"], "Missing alert-2")
	assert.True(t, idMap["alert-3"], "Missing alert-3")
}

func TestShouldIncludeReferences(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "empty string defaults to true",
			url:      "/api/where/route/1.json?key=TEST",
			expected: true,
		},
		{
			name:     "explicit true returns true",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=true",
			expected: true,
		},
		{
			name:     "explicit false returns false",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=false",
			expected: false,
		},
		{
			name:     "garbage string defaults to true",
			url:      "/api/where/route/1.json?key=TEST&includeReferences=banana",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			actual := ShouldIncludeReferences(req)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestDedupeStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty", []string{}, []string{}},
		{"no duplicates", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"duplicates removed preserving order", []string{"b", "a", "b", "c", "a"}, []string{"b", "a", "c"}},
		{"all duplicates", []string{"x", "x", "x"}, []string{"x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, dedupeStrings(tt.input))
		})
	}
}

// firstRabaStopIDs returns raw (un-combined) stop IDs from the RABA test data.
func firstRabaStopIDs(t *testing.T, api *RestAPI, limit int) []string {
	t.Helper()
	rows, err := api.GtfsManager.GtfsDB.DB.QueryContext(context.Background(),
		`SELECT id FROM stops WHERE location_type = 0 OR location_type IS NULL LIMIT ?`, limit)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, ids, "RABA test data should contain stops")
	return ids
}

func TestBuildStopReferencesAndRouteIDsForStops_Empty(t *testing.T) {
	api := createTestApi(t)
	agency := mustGetAgencies(t, api)[0]

	stops, routeMap, err := BuildStopReferencesAndRouteIDsForStops(api, context.Background(), agency.ID, []string{})
	require.NoError(t, err)
	assert.Empty(t, stops)
	assert.Empty(t, routeMap)
	assert.NotNil(t, stops, "should return a non-nil empty slice")
	assert.NotNil(t, routeMap, "should return a non-nil empty map")
}

func TestBuildStopReferencesAndRouteIDsForStops(t *testing.T) {
	api := createTestApi(t)
	agency := mustGetAgencies(t, api)[0]
	stopIDs := firstRabaStopIDs(t, api, 3)

	stops, routeMap, err := BuildStopReferencesAndRouteIDsForStops(api, context.Background(), agency.ID, stopIDs)
	require.NoError(t, err)
	require.Len(t, stops, len(stopIDs), "should return one model per unique stop ID")

	for _, stop := range stops {
		// IDs are agency-combined.
		assert.True(t, strings.HasPrefix(stop.ID, agency.ID+"_"), "stop ID %q should be agency-combined", stop.ID)

		// RouteIDs and StaticRouteIDs mirror each other and are agency-combined.
		assert.Equal(t, stop.RouteIDs, stop.StaticRouteIDs)
		for _, rid := range stop.RouteIDs {
			assert.True(t, strings.HasPrefix(rid, agency.ID+"_"), "route ID %q should be agency-combined", rid)
		}

		// Route IDs are sorted in natural order (no duplicates, stable ordering).
		assert.True(t, slices.IsSortedFunc(stop.RouteIDs, func(a, b string) int {
			return utils.NaturalCompare(a, b)
		}), "route IDs for stop %q should be naturally sorted: %v", stop.ID, stop.RouteIDs)
	}

	// Every combined route ID referenced by a stop appears in the returned route map.
	for _, stop := range stops {
		for _, rid := range stop.RouteIDs {
			_, ok := routeMap[rid]
			assert.True(t, ok, "route %q referenced by stop should be present in routeMap", rid)
		}
	}
}

func TestBuildStopReferencesAndRouteIDsForStops_DeduplicatesStopIDs(t *testing.T) {
	api := createTestApi(t)
	agency := mustGetAgencies(t, api)[0]
	stopIDs := firstRabaStopIDs(t, api, 2)

	// Pass duplicates; the result should contain each stop only once.
	withDupes := append([]string{}, stopIDs...)
	withDupes = append(withDupes, stopIDs...)

	stops, _, err := BuildStopReferencesAndRouteIDsForStops(api, context.Background(), agency.ID, withDupes)
	require.NoError(t, err)
	assert.Len(t, stops, len(stopIDs), "duplicate stop IDs should be collapsed")
}
