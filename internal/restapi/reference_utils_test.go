package restapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
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

func TestQueryInBatches(t *testing.T) {
	ctx := context.Background()

	t.Run("An empty ID set runs no query", func(t *testing.T) {
		queried := false
		results, err := queryInBatches(ctx, nil, func(context.Context, []string) ([]string, error) {
			queried = true
			return nil, nil
		})

		require.NoError(t, err)
		assert.Empty(t, results)
		assert.False(t, queried, "there is nothing to look up")
	})

	t.Run("A failing batch stops the run", func(t *testing.T) {
		batches := 0
		_, err := queryInBatches(ctx, make([]string, idsPerBatchedQuery+1),
			func(context.Context, []string) ([]string, error) {
				batches++
				return nil, errors.New("query failed")
			})

		require.Error(t, err)
		assert.Equal(t, 1, batches, "the remaining batches must not run once one fails")
	})
}

func TestStopReferences(t *testing.T) {
	api := createTestApi(t)
	ctx := context.Background()

	servedStopID := firstRabaStopIDs(t, api, 1)[0]
	servedStop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, servedStopID)
	require.NoError(t, err)

	// No stop_times point at this stop, so no route resolves for it.
	routelessStop := gtfsdb.Stop{
		ID:            "stop-served-by-no-route",
		Lat:           40.5,
		Lon:           -122.3,
		LocationType:  nulls.Int64(1),
		ParentStation: nulls.String("parent-station"),
	}
	defaultStop := gtfsdb.Stop{ID: "default-stop", Lat: 40.6, Lon: -122.4}
	malformedReferenceStop := gtfsdb.Stop{
		ID:            "malformed-reference-stop",
		Lat:           40.7,
		Lon:           -122.5,
		ParentStation: nulls.String("parent-station"),
	}

	referringIDs := map[string]string{
		servedStopID:              utils.FormCombinedID("referring-agency", servedStopID),
		routelessStop.ID:          utils.FormCombinedID("referring-agency", routelessStop.ID),
		defaultStop.ID:            utils.FormCombinedID("referring-agency", defaultStop.ID),
		malformedReferenceStop.ID: "malformed-reference-id",
	}

	refs, routeIDsByStop := api.stopReferences(ctx,
		[]gtfsdb.Stop{servedStop, routelessStop, defaultStop, malformedReferenceStop}, referringIDs)
	require.Len(t, refs, 4, "a stop with no resolvable routes still gets a reference")

	assert.Equal(t, referringIDs[servedStopID], refs[0].ID, "the referring entry's ID labels the reference")
	assert.NotEmpty(t, refs[0].RouteIDs)
	assert.Equal(t, routeIDsByStop[servedStopID], refs[0].RouteIDs)

	assert.Equal(t, referringIDs[routelessStop.ID], refs[1].ID)
	assert.Equal(t, 1, refs[1].LocationType)
	assert.Equal(t, utils.FormCombinedID("referring-agency", "parent-station"), refs[1].Parent)
	assert.Empty(t, refs[1].RouteIDs)
	assert.NotNil(t, refs[1].RouteIDs, "an unresolved route list is empty, not null")
	assert.Equal(t, refs[1].RouteIDs, refs[1].StaticRouteIDs)
	// No stop_times and no shape, so nothing supports a direction.
	assert.Equal(t, models.UnknownValue, refs[1].Direction)

	assert.Equal(t, 0, refs[2].LocationType)
	assert.Empty(t, refs[2].Parent)

	assert.Equal(t, 0, refs[3].LocationType)
	assert.Empty(t, refs[3].Parent)
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
