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

	assert.Len(t, entry.Polylines, 2)
	assert.Equal(t, 250, entry.Polylines[0].Length)
	assert.Equal(t, "", entry.Polylines[0].Levels)
	assert.Contains(t, entry.Polylines[0].Points, "exhwFlt|")
	assert.Equal(t, 250, entry.Polylines[1].Length)
	assert.Equal(t, "", entry.Polylines[1].Levels)
	assert.Contains(t, entry.Polylines[1].Points, "exhwFlt|")

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
	assert.Len(t, outbound.Polylines, 1)

	inbound := grouping.StopGroups[1]
	assert.Equal(t, "1", inbound.ID)
	assert.Equal(t, "Shasta Lake", inbound.Name.Name)
	assert.Equal(t, "destination", inbound.Name.Type)
	assert.Len(t, inbound.StopIds, 22)

	refs := model.Data.References
	require.Len(t, refs.Agencies, 1)
	agency := refs.Agencies[0]
	assert.Equal(t, testdata.Raba.ID, agency.ID)
	assert.Equal(t, testdata.Raba.Name, agency.Name)
	assert.Equal(t, testdata.Raba.URL, agency.URL)
	assert.Equal(t, testdata.Raba.Timezone, agency.Timezone)
	assert.Equal(t, testdata.Raba.Lang, agency.Lang)
	assert.Equal(t, testdata.Raba.Phone, agency.Phone)
	assert.Equal(t, testdata.Raba.PrivateService, agency.PrivateService)

	assert.Len(t, refs.Routes, len(testdata.RabaRoutes))
	assert.Len(t, refs.Stops, 39)
	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Trips)
}

// TestStopsForRouteNoDuplicateStopGroups guards against the regression where
// trips with different headsigns in the same direction produced duplicate group
// IDs (e.g. "0", "0", "1" instead of "0", "1").
func TestStopsForRouteNoDuplicateStopGroups(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/"+testdata.Route1.ID+".json?key=TEST")

	require.Len(t, model.Data.Entry.StopGroupings, 1, "expected exactly one stopGrouping")
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

	resp, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/agencyA_routeA.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	entry := model.Data.Entry
	assert.NotEmpty(t, entry.StopIds, "stops-for-route must return stops even when direction_id is NULL")

	require.Len(t, entry.StopGroupings, 1)
	stopGroups := entry.StopGroupings[0].StopGroups
	require.Len(t, stopGroups, 1, "expected one stop group for the single NULL direction_id")
	assert.Len(t, stopGroups[0].StopIds, 2, "expected both stops to appear in the stop group")
}
