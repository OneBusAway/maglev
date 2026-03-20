package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestStopsForRouteHandlerEndToEnd(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/25_151.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	assert.Greater(t, model.CurrentTime, int64(0))

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25_151", entry["routeId"])

	polylines, ok := entry["polylines"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(polylines))

	firstPolyline, ok := polylines[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 250, int(firstPolyline["length"].(float64)))
	assert.Equal(t, "", firstPolyline["levels"])
	assert.Contains(t, firstPolyline["points"], "exhwFlt|")

	secondPolyline, ok := polylines[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 250, int(secondPolyline["length"].(float64)))
	assert.Equal(t, "", secondPolyline["levels"])
	assert.Contains(t, secondPolyline["points"], "exhwFlt|")

	stopIds, ok := entry["stopIds"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 39, len(stopIds))
	// Verify stopGroupings
	stopGroupings, ok := entry["stopGroupings"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(stopGroupings))

	grouping, ok := stopGroupings[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, grouping["ordered"])
	assert.Equal(t, "direction", grouping["type"])

	stopGroups, ok := grouping["stopGroups"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 2, len(stopGroups))

	// Verify inbound group (direction 1 in normalized 0-based index)
	inboundGroup, ok := stopGroups[1].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "1", inboundGroup["id"])

	inboundName, ok := inboundGroup["name"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Shasta Lake", inboundName["name"])
	assert.Equal(t, "destination", inboundName["type"])

	inboundNames, ok := inboundName["names"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(inboundNames))
	assert.Equal(t, "Shasta Lake", inboundNames[0])

	inboundStopIds, ok := inboundGroup["stopIds"].([]interface{})
	require.True(t, ok)

	// With deterministic sorting, checks should be consistent
	assert.Equal(t, 22, len(inboundStopIds))

	inboundPolylines, ok := inboundGroup["polylines"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(inboundPolylines))

	// Verify outbound group (direction 0 in normalized 0-based index)
	outboundGroup, ok := stopGroups[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "0", outboundGroup["id"])

	outboundName, ok := outboundGroup["name"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Shasta Lake", outboundName["name"])
	assert.Equal(t, "destination", outboundName["type"])

	outboundStopIds, ok := outboundGroup["stopIds"].([]interface{})
	require.True(t, ok)
	// With deterministic sorting, checks should be consistent
	assert.Equal(t, 21, len(outboundStopIds))

	// Verify references
	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Verify agencies
	agencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 1, len(agencies))

	agency, ok := agencies[0].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "25", agency["id"])
	assert.Equal(t, "Redding Area Bus Authority", agency["name"])
	assert.Equal(t, "http://www.rabaride.com/", agency["url"])
	assert.Equal(t, "America/Los_Angeles", agency["timezone"])
	assert.Equal(t, "en", agency["lang"])
	assert.Equal(t, "530-241-2877", agency["phone"])
	assert.Equal(t, false, agency["privateService"])

	routes, ok := refs["routes"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 13, len(routes))

	// Verify stops
	stops, ok := refs["stops"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 39, len(stops))
	require.True(t, ok)

	// Verify empty arrays
	situations, ok := refs["situations"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(situations))

	stopTimes, ok := refs["stopTimes"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(stopTimes))

	trips, ok := refs["trips"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, 0, len(trips))
}

// TestStopsForRouteNoDuplicateStopGroups guards against the regression where
// trips with different headsigns in the same direction produced duplicate group
// IDs (e.g. "0", "0", "1" instead of "0", "1").
func TestStopsForRouteNoDuplicateStopGroups(t *testing.T) {
	_, _, model := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/25_151.json?key=TEST")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	stopGroupings, ok := entry["stopGroupings"].([]interface{})
	require.True(t, ok)
	require.Equal(t, 1, len(stopGroupings), "expected exactly one stopGrouping")

	grouping, ok := stopGroupings[0].(map[string]interface{})
	require.True(t, ok)

	stopGroups, ok := grouping["stopGroups"].([]interface{})
	require.True(t, ok)
	require.Equal(t, 2, len(stopGroups), "expected exactly 2 stop groups (one per direction)")

	// Verify IDs are unique and normalized to "0" and "1"
	ids := make(map[string]bool)
	for _, g := range stopGroups {
		group, ok := g.(map[string]interface{})
		require.True(t, ok)
		id, ok := group["id"].(string)
		require.True(t, ok)
		assert.False(t, ids[id], "duplicate stop group ID: %s", id)
		ids[id] = true
	}
	assert.True(t, ids["0"], "expected stop group with id '0'")
	assert.True(t, ids["1"], "expected stop group with id '1'")
}

func TestStopsForRouteHandlerInvalidRouteID(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/invalid_route.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerMissingRouteIDComponent(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/_FMS.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerNonExistentAgency(t *testing.T) {
	_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/fake_Raba.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestStopsForRouteHandlerWithInvalidTimeFormats(t *testing.T) {
	invalidFormats := []string{
		"yesterday",       // Relative time
		"16868172xx",      // Invalid epoch
		"not-a-timestamp", // Random string
		"2099-01-01",      //Time in the future
	}

	for _, format := range invalidFormats {
		t.Run("Invalid format: "+format, func(t *testing.T) {
			_, resp, _ := serveAndRetrieveEndpoint(t, "/api/where/stops-for-route/25-151.json?key=TEST&time="+format)

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestStopsForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/stops-for-route/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

// createTestApiWithNullDirectionID creates a RestAPI backed by a minimal in-memory
// GTFS dataset whose trips intentionally omit direction_id (NULL in the database).
func createTestApiWithNullDirectionID(t *testing.T) *RestAPI {
	t.Helper()
	ctx := context.Background()

	// Build a minimal GTFS zip. Omitting direction_id from trips.txt causes the
	// column to be NULL in the database, which is the case we need to guard against.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"agencyA,Test Agency,http://example.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"routeA,agencyA,RA,Route A,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc1,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stopA1,Stop One,37.7749,-122.4194\n" +
			"stopA2,Stop Two,37.7849,-122.4094\n",
		// No direction_id column — all trips will have NULL direction_id in the DB.
		"trips.txt": "route_id,service_id,trip_id,trip_headsign\n" +
			"routeA,svc1,tripA,Downtown\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tripA,08:00:00,08:00:00,stopA1,1\n" +
			"tripA,08:10:00,08:10:00,stopA2,2\n",
	}

	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	zipPath := filepath.Join(t.TempDir(), "null-direction.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	gtfsConfig := gtfs.Config{
		GtfsURL:      zipPath,
		GTFSDataPath: ":memory:",
	}

	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	t.Cleanup(gtfsManager.Shutdown)

	dirCalc := gtfs.NewAdvancedDirectionCalculator(gtfsManager.GtfsDB.Queries)

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         gtfsManager,
		DirectionCalculator: dirCalc,
		Clock:               clock.RealClock{},
	}

	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(api.Shutdown)

	return api
}

// TestStopsForRouteNullDirectionID guards against the regression where agencies
// that omit direction_id in their GTFS feed receive an empty stop list. When
// direction_id is NULL, the SQL condition `t.direction_id = NULL` evaluates to
// UNKNOWN (not TRUE), so we fall back to single-trip ordering instead.
func TestStopsForRouteNullDirectionID(t *testing.T) {
	api := createTestApiWithNullDirectionID(t)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stops-for-route/agencyA_routeA.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	stopIds, ok := entry["stopIds"].([]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, stopIds, "stops-for-route must return stops even when direction_id is NULL")

	stopGroupings, ok := entry["stopGroupings"].([]interface{})
	require.True(t, ok)
	require.Len(t, stopGroupings, 1)

	grouping, ok := stopGroupings[0].(map[string]interface{})
	require.True(t, ok)

	stopGroups, ok := grouping["stopGroups"].([]interface{})
	require.True(t, ok)
	require.Len(t, stopGroups, 1, "expected one stop group for the single NULL direction_id")

	group, ok := stopGroups[0].(map[string]interface{})
	require.True(t, ok)

	groupStopIds, ok := group["stopIds"].([]interface{})
	require.True(t, ok)
	assert.Len(t, groupStopIds, 2, "expected both stops to appear in the stop group")
}
