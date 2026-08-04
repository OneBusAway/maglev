package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
)

// TestServiceDayWindows_PreviousDayDate verifies that the previous-day window's
// date is midnight of yesterday, not today — a trip only found via that window
// (a past-midnight block) must have its schedule position extrapolated against
// yesterday's service day.
func TestServiceDayWindows_PreviousDayDate(t *testing.T) {
	// tfr-svc runs every day of the week, so both the current-day and
	// previous-day windows are populated for any query time.
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))

	windows, err := api.serviceDayWindows(context.Background(), tripsForRouteTestClock)
	require.NoError(t, err)
	require.Len(t, windows, 2, "tfr-svc runs every day, so a previous-day window should be present")

	wantToday := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
	wantYesterday := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)

	assert.True(t, windows[0].date.Equal(wantToday), "windows[0].date should be midnight of the query date, got %v", windows[0].date)
	assert.True(t, windows[1].date.Equal(wantYesterday), "windows[1].date should be midnight of the day before, got %v", windows[1].date)
}

const (
	pastMidnightAgencyID = "pm-agency"
	pastMidnightTripID   = "pm-trip"
	pastMidnightStop1ID  = "pm-s1"
	pastMidnightStop2ID  = "pm-s2"
)

// createTestApiWithPastMidnightFixture builds a RestAPI whose only trip runs
// 24:30:00 -> 25:00:00 on its service day — i.e. 00:30-01:00 the following
// calendar morning. Querying at 00:45 therefore hits a trip that is mid-run
// but belongs to the *previous* service day.
func createTestApiWithPastMidnightFixture(t *testing.T, c clock.Clock) *RestAPI {
	t.Helper()
	ctx := context.Background()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			pastMidnightAgencyID + ",Past Midnight,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"pm-route," + pastMidnightAgencyID + ",PM,Past Midnight Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"pm-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			pastMidnightStop1ID + ",Stop One,37.7749,-122.4194\n" +
			pastMidnightStop2ID + ",Stop Two,37.7759,-122.4184\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			"pm-route,pm-svc," + pastMidnightTripID + ",Past Midnight,0,pm-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			pastMidnightTripID + ",24:30:00,24:30:00," + pastMidnightStop1ID + ",1\n" +
			pastMidnightTripID + ",25:00:00,25:00:00," + pastMidnightStop2ID + ",2\n",
	}
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	zipPath := filepath.Join(t.TempDir(), "past-midnight.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	gtfsConfig := gtfs.Config{GtfsURL: zipPath, GTFSDataPath: ":memory:"}
	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	t.Cleanup(gtfsManager.Shutdown)

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         gtfsManager,
		DirectionCalculator: gtfs.NewAdvancedDirectionCalculator(gtfsManager.GtfsDB.Queries),
		Clock:               c,
	}

	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	t.Cleanup(api.Shutdown)
	return api
}

// TestTripsForLocationHandler_PastMidnightTripUsesPreviousServiceDate covers a
// trip resolved through the past-midnight lookback. Its serviceDate must be
// the previous calendar day (per the spec's data.list[].serviceDate), and its
// reported position must be extrapolated against that same service day —
// otherwise the position in the response contradicts the bounding-box filter
// that admitted the trip in the first place.
func TestTripsForLocationHandler_PastMidnightTripUsesPreviousServiceDate(t *testing.T) {
	// 00:45 on 2025-06-13; the trip runs 00:30-01:00 and belongs to 2025-06-12.
	queryTime := time.Date(2025, 6, 13, 0, 45, 0, 0, time.UTC)
	api := createTestApiWithPastMidnightFixture(t, clock.NewMockClock(queryTime))

	wantServiceDate := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)

	url := fmt.Sprintf(
		"/api/where/trips-for-location.json?key=TEST&lat=37.7754&lon=-122.4189&latSpan=0.05&lonSpan=0.05&includeStatus=true&time=%d",
		queryTime.UnixMilli())
	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, 200, resp.StatusCode)
	require.Len(t, model.Data.List, 1, "the past-midnight trip should be found mid-run")

	entry := model.Data.List[0]
	assert.Equal(t, wantServiceDate.UnixMilli(), entry.ServiceDate,
		"a trip running past midnight belongs to the previous calendar day")

	require.NotNil(t, entry.Status)
	assert.Equal(t, wantServiceDate.UnixMilli(), entry.Status.ServiceDate.UnixMilli(),
		"status.serviceDate must match the entry's service date")

	// At 00:45 the trip is halfway between its two stops. Computing against the
	// wrong service day would place it before its first arrival, snapping the
	// position to stop one exactly.
	assert.NotEqual(t, 37.7749, entry.Status.Position.Lat,
		"position must not collapse onto the first stop")
	assert.InDelta(t, 37.7754, entry.Status.Position.Lat, 1e-3,
		"position should interpolate to roughly halfway between the two stops")
	assert.InDelta(t, -122.4189, entry.Status.Position.Lon, 1e-3,
		"position should interpolate to roughly halfway between the two stops")
}
