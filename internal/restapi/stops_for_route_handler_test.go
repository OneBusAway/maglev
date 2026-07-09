package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"log/slog"
	"maglev.onebusaway.org/internal/logging"
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
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestStopsForRouteHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	assert.Equal(t, testdata.Route1.ID, entry.RouteID)

	// Entry-level polylines are an independent merge over both direction shapes
	// (shared undirected edge set), so opposite directions that retrace the same
	// track de-overlap into multiple segments.
	assert.Len(t, entry.Polylines, 21)
	assert.Equal(t, 47, entry.Polylines[0].Length)
	assert.Equal(t, "", entry.Polylines[0].Levels)
	assert.Contains(t, entry.Polylines[0].Points, "_luvFxw_jV")

	assert.Len(t, entry.StopIds, 39)

	require.Len(t, entry.StopGroupings, 1)
	grouping := entry.StopGroupings[0]
	assert.True(t, grouping.Ordered)
	assert.Equal(t, "direction", grouping.Type)

	require.Len(t, grouping.StopGroups, 2)

	outbound := grouping.StopGroups[0]
	assert.Equal(t, "0", outbound.ID)
	assert.Equal(t, "Shasta Lake", outbound.Name.Name)
	assert.Equal(t, "destination", outbound.Name.Type)
	assert.Equal(t, []string{"Shasta Lake"}, outbound.Name.Names)
	assert.Len(t, outbound.StopIds, 21)
	// Direction-0 shape retraces two of its own edges, splitting into 3 polylines.
	require.Len(t, outbound.Polylines, 3)
	assert.Equal(t, 47, outbound.Polylines[0].Length)
	assert.Equal(t, 31, outbound.Polylines[1].Length)
	assert.Equal(t, 162, outbound.Polylines[2].Length)
	assert.Contains(t, outbound.Polylines[0].Points, "_luvFxw_jV")

	inbound := grouping.StopGroups[1]
	assert.Equal(t, "1", inbound.ID)
	assert.Equal(t, "Shasta Lake", inbound.Name.Name)
	assert.Equal(t, "destination", inbound.Name.Type)
	assert.Len(t, inbound.StopIds, 22)
	// Direction-1 shape retraces one of its own edges, splitting into 2 polylines.
	require.Len(t, inbound.Polylines, 2)
	assert.Equal(t, 23, inbound.Polylines[0].Length)
	assert.Equal(t, 208, inbound.Polylines[1].Length)

	refs := model.Data.References
	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, refs.Agencies)

	assert.Len(t, refs.Routes, len(testdata.RabaRoutes))
	assert.Len(t, refs.Stops, 39)
	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Trips)
}

// TestStopsForRouteIncludePolylinesDefault verifies that with includePolylines
// omitted (default true) the entry and every direction group carry polylines.
func TestStopsForRouteIncludePolylinesDefault(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST")

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.Polylines, "default should return entry-level polylines")

	require.Len(t, entry.StopGroupings, 1)
	for _, g := range entry.StopGroupings[0].StopGroups {
		assert.NotEmpty(t, g.Polylines, "group %s should carry polylines by default", g.ID)
	}
}

// TestStopsForRouteIncludePolylinesFalse verifies that includePolylines=false
// empties both the entry-level and every group-level polylines, and that they
// serialize as [] (not null).
func TestStopsForRouteIncludePolylinesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST&includePolylines=false")

	entry := model.Data.Entry
	assert.NotNil(t, entry.Polylines, "entry polylines should be [] not null")
	assert.Empty(t, entry.Polylines, "entry polylines should be empty when includePolylines=false")

	require.Len(t, entry.StopGroupings, 1)
	for _, g := range entry.StopGroupings[0].StopGroups {
		assert.NotNil(t, g.Polylines, "group %s polylines should be [] not null", g.ID)
		assert.Empty(t, g.Polylines, "group %s polylines should be empty when includePolylines=false", g.ID)
	}
}

// TestStopsForRouteTimeFilter_ActiveDate verifies that supplying a time parameter
// restricts results to trips active on that service date.
func TestStopsForRouteTimeFilter_ActiveDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// 2025-01-06 is a Monday; RABA route 151 runs Mon–Fri (service c_1658_b_18260_d_31).
	_, model := callAPIHandler[StopsForRouteResponse](t, api,
		"/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST&time=2025-01-06")

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.StopIds, "weekday date should return stops")
	require.Len(t, entry.StopGroupings, 1)
	assert.NotEmpty(t, entry.StopGroupings[0].StopGroups, "weekday date should produce direction groups")
}

// TestStopsForRouteTimeFilter_NoServiceDate verifies that when the requested date
// has no active service the stop list and direction groups are empty, but the
// direction grouping element is still present.
func TestStopsForRouteTimeFilter_NoServiceDate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// 2025-01-05 is a Sunday; RABA route 151 has no Sunday service.
	_, model := callAPIHandler[StopsForRouteResponse](t, api,
		"/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST&time=2025-01-05")

	entry := model.Data.Entry
	assert.Empty(t, entry.StopIds, "no-service date should return empty stop list")
	require.Len(t, entry.StopGroupings, 1, "direction grouping element must still be present")
	assert.Empty(t, entry.StopGroupings[0].StopGroups, "no-service date should return empty direction groups")
}

// TestStopsForRouteNoDuplicateStopGroups guards against the regression where
// trips with different headsigns in the same direction produced duplicate group
// IDs (e.g. "0", "0", "1" instead of "0", "1").
func TestStopsForRouteNoDuplicateStopGroups(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST")

	require.Len(t, model.Data.Entry.StopGroupings, 1)
	stopGroups := model.Data.Entry.StopGroupings[0].StopGroups
	require.Len(t, stopGroups, 2, "expected exactly 2 stop groups (one per direction)")

	ids := make(map[string]bool)
	for _, g := range stopGroups {
		assert.False(t, ids[g.ID], "duplicate stop group ID: %s", g.ID)
		ids[g.ID] = true
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

	resp, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/1110.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
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
	api.Logger = logging.NewStructuredLogger(os.Stdout, slog.LevelDebug)
	t.Cleanup(api.Shutdown)

	return api
}

// TestStopsForRouteNullDirectionID guards against the regression where agencies
// that omit direction_id in their GTFS feed receive an empty stop list. When
// direction_id is NULL, the SQL condition `t.direction_id = NULL` evaluates to
// UNKNOWN (not TRUE), so we fall back to single-trip ordering instead.
func TestStopsForRouteNullDirectionID(t *testing.T) {
	api := createTestApiWithNullDirectionID(t)

	resp, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/agencyA_routeA.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.StopIds, "stops-for-route must return stops even when direction_id is NULL")

	require.Len(t, entry.StopGroupings, 1)
	stopGroups := entry.StopGroupings[0].StopGroups
	require.Len(t, stopGroups, 1, "expected one stop group for the single NULL direction_id")
	assert.Len(t, stopGroups[0].StopIds, 2, "expected both stops to appear in the stop group")
}
