package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForRouteTestClock is the clock used by the synthetic-fixture tests below.
// The fixture inserts a trip with stop_times at 11:55 and 12:05, so a clock at
// 12:00 falls inside the handler's (-30min/+10min) active window.
var tripsForRouteTestClock = time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC)

// afterMidnightClock is 00:30 UTC on 2025-06-13 — after midnight.
// Used by the overnight interline fixture where the previous day's trips
// (23:30–24:30) are active but today's (23:00–23:30) are not.
var afterMidnightClock = time.Date(2025, 6, 13, 0, 30, 0, 0, time.UTC)

// loopRouteClock is 10:15 UTC on 2025-06-12 — used by the looping-route
// and gap-case fixtures.
var loopRouteClock = time.Date(2025, 6, 12, 10, 15, 0, 0, time.UTC)

// blockSequenceClock is 09:50 UTC on 2025-06-12, when the middle trip of the
// block-sequence fixture (tfr-trip-2, 09:35–10:05) is active.
var blockSequenceClock = time.Date(2025, 6, 12, 9, 50, 0, 0, time.UTC)

const (
	tripsForRouteAgencyID = "tfr-agency"
	tripsForRouteRouteID  = "tfr-route"
	tripsForRouteTripID   = "tfr-trip"
	tripsForRouteStop1ID  = "tfr-stop1"
	tripsForRouteStop2ID  = "tfr-stop2"
	tripsForRouteHeadsign = "Test Headsign"
	orphanRouteAgencyID   = "tfr-agency-x"
	orphanRouteID         = "tfr-route-x"
	orphanTripID          = "tfr-trip-x"
)

// createTestApiWithGTFSFixture builds a RestAPI backed by an in-memory GTFS
// dataset from the given file-content map. This eliminates the duplicated
// boilerplate across the various per-scenario fixture builders.
func createTestApiWithGTFSFixture(t *testing.T, c clock.Clock, zipName string, files map[string]string) *RestAPI {
	t.Helper()
	ctx := context.Background()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	zipPath := filepath.Join(t.TempDir(), zipName)
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	gtfsConfig := gtfs.Config{GtfsURL: zipPath, GTFSDataPath: ":memory:"}
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
		Clock:               c,
	}

	api := NewRestAPI(application)
	api.Logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(api.Shutdown)
	return api
}

// --- Fixture file maps ---

func basicTripsForRouteFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc," + tripsForRouteTripID + "," + tripsForRouteHeadsign + ",0,tfr-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			tripsForRouteTripID + ",11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
			tripsForRouteTripID + ",12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n",
	}
}

func interlineFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-trip-a,Headsign A,0,tfr-interline\n" +
			"tfr-route-otr,tfr-svc,tfr-trip-b,Headsign B,0,tfr-interline\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-trip-a,11:20:00,11:20:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-a,11:50:00,11:50:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-trip-b,11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-b,12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n",
	}
}

// twoServiceIDsInterlineFiles models a block whose active (other-route) trip
// and queried-route trip run under two different service_ids that are both
// active on the same calendar day (unlike overnightInterlineFiles, this isn't
// a midnight split). GTFS allows more than one service_id to be active on a
// given date, and nothing requires a block's trips to share one literal
// service_id, so this doesn't require special calendar handling to trigger.
func twoServiceIDsInterlineFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc-a,1,1,1,1,1,1,1,20240101,20991231\n" +
			"tfr-svc-b,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc-a,tfr-trip-a,Headsign A,0,tfr-interline\n" +
			"tfr-route-otr,tfr-svc-b,tfr-trip-b,Headsign B,0,tfr-interline\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-trip-a,11:20:00,11:20:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-a,11:50:00,11:50:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-trip-b,11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-b,12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n",
	}
}

func overnightInterlineFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc-yest,0,0,0,1,0,0,0,20250612,20250612\n" +
			"tfr-svc-today,0,0,0,0,1,0,0,20250613,20250613\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc-yest,tfr-yest-a,Headsign A,0,tfr-overnight\n" +
			"tfr-route-otr,tfr-svc-yest,tfr-yest-b,Headsign B,0,tfr-overnight\n" +
			tripsForRouteRouteID + ",tfr-svc-today,tfr-today-a,Headsign A,0,tfr-overnight\n" +
			"tfr-route-otr,tfr-svc-today,tfr-today-b,Headsign B,0,tfr-overnight\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-yest-a,23:00:00,23:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-yest-a,24:10:00,24:10:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-yest-b,23:55:00,23:55:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-yest-b,24:45:00,24:45:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-today-a,23:00:00,23:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-today-a,23:30:00,23:30:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-today-b,23:00:00,23:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-today-b,23:30:00,23:30:00," + tripsForRouteStop2ID + ",2\n",
	}
}

func loopingRouteFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-loop-a,Headsign A,0,tfr-loop-block\n" +
			"tfr-route-otr,tfr-svc,tfr-loop-b,Headsign B,0,tfr-loop-block\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-loop-c,Headsign C,0,tfr-loop-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-loop-a,09:00:00,09:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-loop-a,09:45:00,09:45:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-loop-b,10:00:00,10:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-loop-b,10:30:00,10:30:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-loop-c,10:30:00,10:30:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-loop-c,11:15:00,11:15:00," + tripsForRouteStop2ID + ",2\n",
	}
}

func gapFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-gap-a,Headsign A,0,tfr-gap-block\n" +
			"tfr-route-otr,tfr-svc,tfr-gap-b,Headsign B,0,tfr-gap-block\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-gap-c,Headsign C,0,tfr-gap-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-gap-a,09:30:00,09:30:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-gap-a,09:50:00,09:50:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-gap-b,10:00:00,10:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-gap-b,10:30:00,10:30:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-gap-c,10:35:00,10:35:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-gap-c,10:50:00,10:50:00," + tripsForRouteStop2ID + ",2\n",
	}
}

// crossDayBlockReuseFiles reuses block_id "tfr-overnight" for two otherwise
// unrelated service_ids on consecutive calendar days, and deliberately gives
// today's occurrence (tfr-today-a) a time-of-day closer to the active trip's
// midpoint than yesterday's actual match (tfr-yest-a). This defeats a pure
// nearest-midpoint search across all same-block candidates and requires
// preferring same-service_id candidates first.
func crossDayBlockReuseFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			"tfr-route-otr," + tripsForRouteAgencyID + ",OR,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc-yest,0,0,0,1,0,0,0,20250612,20250612\n" +
			"tfr-svc-today,0,0,0,0,1,0,0,20250613,20250613\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc-yest,tfr-yest-a,Headsign A,0,tfr-overnight\n" +
			"tfr-route-otr,tfr-svc-yest,tfr-yest-b,Headsign B,0,tfr-overnight\n" +
			tripsForRouteRouteID + ",tfr-svc-today,tfr-today-a,Headsign A,0,tfr-overnight\n" +
			"tfr-route-otr,tfr-svc-today,tfr-today-b,Headsign B,0,tfr-overnight\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			// yest-a: mid 22:05 — the correct match, but farther from yest-b's mid.
			"tfr-yest-a,22:00:00,22:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-yest-a,22:10:00,22:10:00," + tripsForRouteStop2ID + ",2\n" +
			// yest-b (active): mid 24:20.
			"tfr-yest-b,23:55:00,23:55:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-yest-b,24:45:00,24:45:00," + tripsForRouteStop2ID + ",2\n" +
			// today-a: mid 24:20 — numerically identical to yest-b's mid, despite
			// being an unrelated trip from a different calendar day's block.
			"tfr-today-a,24:15:00,24:15:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-today-a,24:25:00,24:25:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-today-b,23:00:00,23:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-today-b,23:30:00,23:30:00," + tripsForRouteStop2ID + ",2\n",
	}
}

// blockSequenceFiles models three trips in one block: tfr-trip-1
// (09:00–09:30), tfr-trip-2 (09:35–10:05), and tfr-trip-3 (10:10–10:40). At
// blockSequenceClock only tfr-trip-2 is active; its schedule references the
// adjacent block trips, which are not part of the handler's fetched trips.
func blockSequenceFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-trip-1,Headsign 1,0,tfr-seq-block\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-trip-2,Headsign 2,0,tfr-seq-block\n" +
			tripsForRouteRouteID + ",tfr-svc,tfr-trip-3,Headsign 3,0,tfr-seq-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"tfr-trip-1,09:00:00,09:00:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-1,09:30:00,09:30:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-trip-2,09:35:00,09:35:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-2,10:05:00,10:05:00," + tripsForRouteStop2ID + ",2\n" +
			"tfr-trip-3,10:10:00,10:10:00," + tripsForRouteStop1ID + ",1\n" +
			"tfr-trip-3,10:40:00,10:40:00," + tripsForRouteStop2ID + ",2\n",
	}
}

// orphanStopRouteFiles models a stop served by a route no returned trip runs
// on: tfr-trip (the queried route, tfr-agency) is active at the pinned clock,
// while orphanTripID (orphanRouteID, owned by a second agency) served the same
// stops hours earlier and shares no block, so neither it, its route, nor its
// agency appears among the entries or their trips. The stop references still
// name orphanRouteID in their routeIds, so both that route and its agency must
// resolve in the references block.
func orphanStopRouteFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			tripsForRouteAgencyID + ",Test Agency,http://example.com,UTC\n" +
			orphanRouteAgencyID + ",Other Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			tripsForRouteRouteID + "," + tripsForRouteAgencyID + ",TR,Test Route,3\n" +
			orphanRouteID + "," + orphanRouteAgencyID + ",XR,Orphan Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"tfr-svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			tripsForRouteStop1ID + ",Stop One,37.7749,-122.4194\n" +
			tripsForRouteStop2ID + ",Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			tripsForRouteRouteID + ",tfr-svc," + tripsForRouteTripID + "," + tripsForRouteHeadsign + ",0,tfr-block\n" +
			orphanRouteID + ",tfr-svc," + orphanTripID + ",Orphan Headsign,0,tfr-block-x\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			tripsForRouteTripID + ",11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
			tripsForRouteTripID + ",12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n" +
			orphanTripID + ",08:00:00,08:00:00," + tripsForRouteStop1ID + ",1\n" +
			orphanTripID + ",08:10:00,08:10:00," + tripsForRouteStop2ID + ",2\n",
	}
}

func TestTripsForRouteHandler_DifferentRoutes(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name         string
		routeID      string
		minExpected  int
		maxExpected  int
		expectStatus int
	}{
		{
			name:         "Main Route",
			routeID:      combinedRouteID,
			minExpected:  1, // fixture guarantees exactly one active trip.
			maxExpected:  10,
			expectStatus: http.StatusOK,
		},
		{
			name:         "Unknown Route in Known Agency",
			routeID:      utils.FormCombinedID(tripsForRouteAgencyID, "NONEXISTENT_ROUTE"),
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusOK,
		},
		{
			name:         "Unknown Agency",
			routeID:      utils.FormCombinedID("UNKNOWN_AGENCY", "NONEXISTENT"),
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusOK,
		},
		{
			name:         "Malformed ID — No Underscore",
			routeID:      "NONEXISTENT",
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusBadRequest,
		},
		{
			name:         "Empty Route ID",
			routeID:      "",
			minExpected:  0,
			maxExpected:  0,
			expectStatus: http.StatusBadRequest,
		},
	}

	// Pass time explicitly to pin the handler's time window to our fixture.
	timeMs := tripsForRouteTestClock.UnixMilli()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d",
				tt.routeID, timeMs)

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, tt.expectStatus, resp.StatusCode)
			if tt.expectStatus != http.StatusOK {
				assert.Equal(t, tt.expectStatus, model.Code)
				return
			}

			assert.Equal(t, http.StatusOK, model.Code)
			assert.Equal(t, "OK", model.Text)
			assert.Equal(t, 2, model.Version)
			assert.NotZero(t, model.CurrentTime)
			assert.False(t, model.Data.LimitExceeded)

			assert.GreaterOrEqual(t, len(model.Data.List), tt.minExpected)
			assert.LessOrEqual(t, len(model.Data.List), tt.maxExpected)

			if len(model.Data.List) == 0 {
				return
			}

			expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteTripID)
			for i, entry := range model.Data.List {
				assert.Equal(t, expectedTripID, entry.TripId, "list[%d].tripId should be combined ID", i)
				assert.NotZero(t, entry.ServiceDate, "list[%d].serviceDate should be a non-zero unix-ms", i)
				assert.NotNil(t, entry.SituationIds, "list[%d].situationIds should never be null", i)

				require.NotNil(t, entry.Schedule, "list[%d].schedule should be present when includeSchedule=true", i)
				assert.Equal(t, "UTC", entry.Schedule.TimeZone,
					"list[%d].schedule.timeZone should match the agency's timezone", i)
				require.Len(t, entry.Schedule.StopTimes, 2, "list[%d].schedule should have both stop times", i)
				for j, st := range entry.Schedule.StopTimes {
					assert.Contains(t, st.StopID, "_", "list[%d].schedule.stopTimes[%d].stopId should be combined ID", i, j)
					assert.GreaterOrEqual(t, st.DepartureTime.Duration, st.ArrivalTime.Duration,
						"list[%d].schedule.stopTimes[%d] departure must be >= arrival", i, j)
				}

				if entry.Status != nil {
					assert.Contains(t, []string{"scheduled", "in_progress", "completed"}, entry.Status.Phase,
						"list[%d].status.phase should be a known value", i)
					assert.NotEmpty(t, entry.Status.Status, "list[%d].status.status should be set", i)
				}
			}

			refs := model.Data.References
			require.Len(t, refs.Agencies, 1, "response should reference the single fixture agency")
			assert.Equal(t, tripsForRouteAgencyID, refs.Agencies[0].ID)
			require.Len(t, refs.Routes, 1, "response should reference the single fixture route")
			assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID), refs.Routes[0].ID)
			require.Len(t, refs.Stops, 2, "response should reference both fixture stops when includeSchedule=true")

			expectedStopIDs := make(map[string]bool)
			for _, trip := range model.Data.List {
				if trip.Schedule != nil {
					for _, st := range trip.Schedule.StopTimes {
						expectedStopIDs[st.StopID] = true
					}
				}
			}

			actualStopIDs := make(map[string]bool)
			for _, s := range refs.Stops {
				actualStopIDs[s.ID] = true
			}

			assert.Equal(t, expectedStopIDs, actualStopIDs, "reference stop IDs must exactly match the deduped schedule stop IDs")
		})
	}
}

// TestTripsForRouteHandler_TimeOmitted verifies that omitting the time parameter
// correctly falls back to the injected api.Clock to resolve active trips.
func TestTripsForRouteHandler_TimeOmitted(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true", combinedRouteID)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)
	// currentTime comes from the API clock, as does the omitted time= lookup.
	assert.Equal(t, tripsForRouteTestClock.UnixMilli(), model.CurrentTime)
	assert.False(t, model.Data.LimitExceeded)
	assert.False(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.FieldErrors)

	require.Len(t, model.Data.List, 1, "the fixture trip must be active at the pinned clock time")
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteTripID)
	assert.Equal(t, expectedTripID, model.Data.List[0].TripId)
	assert.NotZero(t, model.Data.List[0].ServiceDate)
}

// TestTripsForRouteHandler_InvalidTimeParameter verifies that malformed time
// parameter values correctly trigger a 400 Bad Request validation error.
func TestTripsForRouteHandler_InvalidTimeParameter(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name string
		time string
	}{
		{name: "Garbage String", time: "garbage"},
		{name: "Overflowing Epoch", time: "99999999999999999999"},
		{name: "Invalid Calendar Date", time: "2024-13-45"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%s",
				combinedRouteID, tt.time)

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			assert.Equal(t, "Invalid field value for field \"time\".", model.Text)
			assert.Equal(t, 2, model.Version)
			require.Contains(t, model.Data.FieldErrors, "time")
			assert.Equal(t, []string{"Invalid field value for field \"time\"."}, model.Data.FieldErrors["time"])
		})
	}
}

func TestTripsForRouteHandler_ScheduleInclusion(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name            string
		includeSchedule bool
	}{
		{name: "With Schedule", includeSchedule: true},
		{name: "Without Schedule", includeSchedule: false},
	}

	timeMs := tripsForRouteTestClock.UnixMilli()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=%v&time=%d",
				combinedRouteID, tt.includeSchedule, timeMs)

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, model.Data.List,
				"fixture guarantees a trip at the pinned clock — without entries the per-entry assertions never fire")
			for i, entry := range model.Data.List {
				if tt.includeSchedule {
					require.NotNil(t, entry.Schedule, "list[%d].schedule should be present when includeSchedule=true", i)
					assert.Equal(t, "UTC", entry.Schedule.TimeZone,
						"list[%d].schedule.timeZone should match the agency's timezone", i)
					require.Len(t, entry.Schedule.StopTimes, 2,
						"list[%d].schedule should have both stop times from the fixture", i)
					for j, st := range entry.Schedule.StopTimes {
						assert.Contains(t, st.StopID, "_",
							"list[%d].schedule.stopTimes[%d].stopId should be combined ID", i, j)
					}
				} else {
					assert.Nil(t, entry.Schedule,
						"list[%d].schedule should be omitted when includeSchedule=false", i)
				}
			}
		})
	}
}

func TestTripsForRouteHandler_TripInclusion(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name            string
		includeTrip     string
		includeSchedule string
		wantTripsLen    int
	}{
		{
			name:            "Include Trip (default)",
			includeTrip:     "",
			includeSchedule: "true",
			wantTripsLen:    1,
		},
		{
			name:            "Include Trip Explicit",
			includeTrip:     "true",
			includeSchedule: "true",
			wantTripsLen:    1,
		},
		{
			name:            "Exclude Trip",
			includeTrip:     "false",
			includeSchedule: "true",
			wantTripsLen:    0,
		},
		{
			name:            "No Schedule But Still Include Trip",
			includeTrip:     "",
			includeSchedule: "false",
			wantTripsLen:    1,
		},
		{
			name:            "Exclude Trip (Uppercase FALSE)",
			includeTrip:     "FALSE",
			includeSchedule: "true",
			wantTripsLen:    0,
		},
		{
			name:            "Exclude Trip (Numeric 0)",
			includeTrip:     "0",
			includeSchedule: "true",
			wantTripsLen:    0,
		},
	}

	timeMs := tripsForRouteTestClock.UnixMilli()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=%s&time=%d",
				combinedRouteID, tt.includeSchedule, timeMs)
			if tt.includeTrip != "" {
				url += "&includeTrip=" + tt.includeTrip
			}

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, model.Data.List,
				"fixture guarantees a trip at the pinned clock")
			assert.Equal(t, tt.wantTripsLen, len(model.Data.References.Trips),
				"references.trips should have the expected number of entries")

			for i, entry := range model.Data.List {
				assert.NotEmpty(t, entry.TripId,
					"list[%d].tripId should always be present regardless of includeTrip", i)
			}
		})
	}
}

func TestTripsForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())

	endpoint := "/api/where/trips-for-route/1110.json?key=TEST"

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripsForRouteHandler_ReferencesInclusion(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name              string
		includeReferences string
		wantRefsPopulated bool
	}{
		{
			name:              "Include References (default)",
			includeReferences: "",
			wantRefsPopulated: true,
		},
		{
			name:              "Include References Explicit",
			includeReferences: "true",
			wantRefsPopulated: true,
		},
		{
			name:              "Exclude References",
			includeReferences: "false",
			wantRefsPopulated: false,
		},
	}

	timeMs := tripsForRouteTestClock.UnixMilli()

	baselineURL := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d", combinedRouteID, timeMs)
	_, baselineModel := callAPIHandler[TripsForRouteResponse](t, api, baselineURL)
	expectedList := baselineModel.Data.List

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d",
				combinedRouteID, timeMs)
			if tt.includeReferences != "" {
				url += "&includeReferences=" + tt.includeReferences
			}

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, expectedList, model.Data.List, "data.list should remain exactly the same regardless of includeReferences flag")
			require.NotEmpty(t, model.Data.List,
				"fixture guarantees a trip at the pinned clock")

			if tt.wantRefsPopulated {
				assert.NotEmpty(t, model.Data.References.Agencies,
					"references.agencies should be populated")
				assert.NotEmpty(t, model.Data.References.Routes,
					"references.routes should be populated")
				assert.NotEmpty(t, model.Data.References.Trips,
					"references.trips should be populated")
				assert.NotEmpty(t, model.Data.References.Stops,
					"references.stops should be populated when includeSchedule=true")
			} else {
				assert.NotNil(t, model.Data.References.Agencies, "references.agencies should be non-nil")
				assert.Empty(t, model.Data.References.Agencies, "references.agencies should be empty")
				assert.NotNil(t, model.Data.References.Routes, "references.routes should be non-nil")
				assert.Empty(t, model.Data.References.Routes, "references.routes should be empty")
				assert.NotNil(t, model.Data.References.Trips, "references.trips should be non-nil")
				assert.Empty(t, model.Data.References.Trips, "references.trips should be empty")
				assert.NotNil(t, model.Data.References.Stops, "references.stops should be non-nil")
				assert.Empty(t, model.Data.References.Stops, "references.stops should be empty")
			}
		})
	}
}

func TestTripsForRouteHandler_ReferencesInclusion_EmptyList(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	outOfServiceTimeMs := tripsForRouteTestClock.Add(12 * time.Hour).UnixMilli()

	tests := []struct {
		name              string
		includeReferences string
	}{
		{
			name:              "Empty List - Include References Explicit",
			includeReferences: "true",
		},
		{
			name:              "Empty List - Exclude References",
			includeReferences: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d",
				combinedRouteID, outOfServiceTimeMs)

			if tt.includeReferences != "" {
				url += "&includeReferences=" + tt.includeReferences
			}

			resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			require.Empty(t, model.Data.List, "fixture should guarantee NO trips at this out-of-service time")

			assert.NotNil(t, model.Data.References.Agencies, "references.agencies should be non-nil")
			assert.Empty(t, model.Data.References.Agencies, "references.agencies should be empty")
			assert.NotNil(t, model.Data.References.Routes, "references.routes should be non-nil")
			assert.Empty(t, model.Data.References.Routes, "references.routes should be empty")
			assert.NotNil(t, model.Data.References.Trips, "references.trips should be non-nil")
			assert.Empty(t, model.Data.References.Trips, "references.trips should be empty")
			assert.NotNil(t, model.Data.References.Stops, "references.stops should be non-nil")
			assert.Empty(t, model.Data.References.Stops, "references.stops should be empty")
		})
	}
}

func TestTripsForRouteHandler_BoolParamParsing(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := tripsForRouteTestClock.UnixMilli()

	values := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "omitted defaults to true", query: "", want: true},
		{name: "explicit true", query: "=true", want: true},
		{name: "explicit false", query: "=false", want: false},
		{name: "empty value", query: "=", want: false},
		{name: "junk value", query: "=abc", want: false},
	}

	assertFlag := func(t *testing.T, model *TripsForRouteResponse, flag string, want bool) {
		t.Helper()
		require.NotEmpty(t, model.Data.List, "fixture guarantees a trip at the pinned clock")
		switch flag {
		case "includeSchedule":
			for i, entry := range model.Data.List {
				if want {
					require.NotNil(t, entry.Schedule, "list[%d].schedule should be present", i)
				} else {
					assert.Nil(t, entry.Schedule, "list[%d].schedule should be omitted", i)
				}
			}
		case "includeStatus":
			for i, entry := range model.Data.List {
				if want {
					require.NotNil(t, entry.Status, "list[%d].status should be present", i)
					assert.NotEmpty(t, entry.Status.ActiveTripID, "list[%d].status.activeTripId should be set", i)
					assert.Contains(t, []string{"scheduled", "in_progress", "completed"}, entry.Status.Phase,
						"list[%d].status.phase should be a known value", i)
				} else {
					assert.Nil(t, entry.Status, "list[%d].status should be omitted", i)
				}
			}
		case "includeTrip":
			if want {
				assert.NotEmpty(t, model.Data.References.Trips, "references.trips should contain the fixture trip")
			} else {
				assert.Empty(t, model.Data.References.Trips, "references.trips should be empty")
			}
		case "includeReferences":
			if want {
				assert.NotEmpty(t, model.Data.References.Agencies, "references.agencies should be populated")
				assert.NotEmpty(t, model.Data.References.Routes, "references.routes should be populated")
				assert.NotEmpty(t, model.Data.References.Trips, "references.trips should be populated")
				assert.NotEmpty(t, model.Data.References.Stops, "references.stops should be populated")
			} else {
				assert.NotNil(t, model.Data.References.Agencies, "references.agencies should be non-nil")
				assert.Empty(t, model.Data.References.Agencies, "references.agencies should be empty")
				assert.NotNil(t, model.Data.References.Routes, "references.routes should be non-nil")
				assert.Empty(t, model.Data.References.Routes, "references.routes should be empty")
				assert.NotNil(t, model.Data.References.Trips, "references.trips should be non-nil")
				assert.Empty(t, model.Data.References.Trips, "references.trips should be empty")
				assert.NotNil(t, model.Data.References.Stops, "references.stops should be non-nil")
				assert.Empty(t, model.Data.References.Stops, "references.stops should be empty")
			}
		}
	}

	for _, flag := range []string{"includeSchedule", "includeStatus", "includeTrip", "includeReferences"} {
		for _, tt := range values {
			want := tt.want
			if flag == "includeReferences" && (tt.name == "empty value" || tt.name == "junk value") {
				// includeReferences is parsed by the shared ShouldIncludeReferences
				// helper (also used by every other endpoint), which treats an
				// unparseable value as true rather than false.
				want = true
			}

			t.Run(flag+"/"+tt.name, func(t *testing.T) {
				url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d", combinedRouteID, timeMs)
				if tt.query != "" {
					url += "&" + flag + tt.query
				}

				resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assertFlag(t, &model, flag, want)
			})
		}
	}

	t.Run("all omitted default to true", func(t *testing.T) {
		url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d", combinedRouteID, timeMs)

		resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		for _, flag := range []string{"includeSchedule", "includeStatus", "includeTrip", "includeReferences"} {
			assertFlag(t, &model, flag, true)
		}
	})
}

func TestStripNumericSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"LLR_TRIP_1083.00060", "LLR_TRIP_1083"},
		{"LLR_TRIP_1083.0", "LLR_TRIP_1083"},
		{"LLR_TRIP_1083", "LLR_TRIP_1083"},         // no dot → unchanged
		{"LLR_TRIP_1083.abc", "LLR_TRIP_1083.abc"}, // non-digit suffix → unchanged
		{"LLR_TRIP_1083.", "LLR_TRIP_1083."},       // trailing dot only → unchanged
		{"12345", "12345"},                         // no dot → unchanged
		{"a.1.2", "a.1"},                           // strips last numeric segment only
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, stripNumericSuffix(tt.input), "input: %q", tt.input)
	}
}

func TestTripsForRouteHandler_OutOfRangeNotEmitted(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock), "trips-for-route.zip", basicTripsForRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "populated success path",
			url:  fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d", combinedRouteID, tripsForRouteTestClock.UnixMilli()),
		},
		{
			name: "empty-list path",
			url:  fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d", combinedRouteID, tripsForRouteTestClock.Add(12*time.Hour).UnixMilli()),
		},
		{
			name: "unknown-agency path",
			url:  fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d", utils.FormCombinedID("UNKNOWN_AGENCY", "NONEXISTENT"), tripsForRouteTestClock.UnixMilli()),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := fetchRawData(t, api, tt.url)

			_, ok := data["outOfRange"]
			assert.False(t, ok, "outOfRange key must NOT be present in trips-for-route response (spec-compliant)")
			_, ok = data["limitExceeded"]
			assert.True(t, ok, "limitExceeded key must still be present")
			_, ok = data["list"]
			assert.True(t, ok, "list key must be present")
			_, ok = data["references"]
			assert.True(t, ok, "references key must be present")
		})
	}
}

func TestCollectStopIDsFromSchedule_NilSchedule(t *testing.T) {
	stopIDsMap := map[string]string{}

	collectStopIDsFromSchedule(nil, stopIDsMap)

	assert.Empty(t, stopIDsMap, "nil schedule must not add any entries")
}

func TestCollectStopIDsFromSchedule_PopulatesMap(t *testing.T) {
	schedule := &models.TripsSchedule{
		StopTimes: []models.StopTime{
			{StopID: "25_1001"},
			{StopID: "25_1002"},
			{StopID: "25_1003"},
		},
	}
	stopIDsMap := map[string]string{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]string{
		"1001": "25_1001",
		"1002": "25_1002",
		"1003": "25_1003",
	}, stopIDsMap)
}

func TestCollectStopIDsFromSchedule_SkipsMalformedIDs(t *testing.T) {
	schedule := &models.TripsSchedule{
		StopTimes: []models.StopTime{
			{StopID: "25_good"},
			{StopID: "no-underscore"},
		},
	}
	stopIDsMap := map[string]string{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]string{"good": "25_good"}, stopIDsMap,
		"malformed stop IDs must be silently skipped")
}

func TestCollectStopIDsFromSchedule_EmptyStopTimes(t *testing.T) {
	schedule := &models.TripsSchedule{StopTimes: []models.StopTime{}}
	stopIDsMap := map[string]string{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Empty(t, stopIDsMap)
}

// TestTripsForRouteHandler_InterlinedBlock verifies that when a block spans
// two routes (the queried route and another route), the entry's outer tripId
// resolves to the queried-route trip in the block while status.activeTripId
// reflects the vehicle's currently-running trip on the other route, and that
// schedule (built from tripId) carries the queried-route trip's own stop
// times rather than the active trip's.
func TestTripsForRouteHandler_InterlinedBlock(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock),
		"trips-for-route-interline.zip", interlineFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := tripsForRouteTestClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-a")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)

	// tfr-trip-a (the entry's own tripId) runs 11:20-11:50; the active trip
	// tfr-trip-b runs 11:55-12:05. Schedule must reflect tfr-trip-a's times.
	require.NotNil(t, entry.Schedule)
	require.Len(t, entry.Schedule.StopTimes, 2)
	assert.Equal(t, 11*time.Hour+20*time.Minute, entry.Schedule.StopTimes[0].ArrivalTime.Duration)
	assert.Equal(t, 11*time.Hour+50*time.Minute, entry.Schedule.StopTimes[1].ArrivalTime.Duration)
}

// TestTripsForRouteHandler_InterlinedBlock_TripReference verifies that with
// includeTrip=true, the references include the queried-route trip selected as
// the entry's tripId (tfr-trip-a), carrying the queried route — not just the
// active trip on the other route.
func TestTripsForRouteHandler_InterlinedBlock_TripReference(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock),
		"trips-for-route-interline.zip", interlineFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := tripsForRouteTestClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeTrip=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-a")
	expectedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	var refTrip *models.Trip
	for i := range model.Data.References.Trips {
		if model.Data.References.Trips[i].ID == expectedTripID {
			refTrip = &model.Data.References.Trips[i]
			break
		}
	}
	require.NotNil(t, refTrip, "references must include the queried-route trip tfr-trip-a")
	assert.Equal(t, expectedRouteID, refTrip.RouteID,
		"the reference must carry the queried route, not the active trip's route")
}

// TestTripsForRouteHandler_OvernightInterlinedBlock verifies that a block
// whose trips straddle midnight is resolved against the previous service day:
// at 00:30 the active trip is yesterday's tfr-yest-b (23:55–24:45), so the
// entry's tripId must resolve to yesterday's queried-route trip tfr-yest-a,
// not today's trip sharing the same block ID.
func TestTripsForRouteHandler_OvernightInterlinedBlock(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(afterMidnightClock),
		"trips-for-route-overnight.zip", overnightInterlineFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := afterMidnightClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-yest-a")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-yest-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)
}

// TestTripsForRouteHandler_LoopingRouteBlock verifies that when a block visits
// the queried route, leaves, and returns (route A → B → A), the entry's
// tripId resolves to the queried-route trip nearest to the active trip
// (tfr-loop-c), not the first match (tfr-loop-a).
func TestTripsForRouteHandler_LoopingRouteBlock(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(loopRouteClock),
		"trips-for-route-loop.zip", loopingRouteFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := loopRouteClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-loop-c")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-loop-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)
}

// TestTripsForRouteHandler_GapCase verifies the nearest-midpoint resolution
// across a layover gap: with the active trip tfr-gap-b running 10:00–10:30,
// the queried-route trips tfr-gap-a (09:30–09:50) and tfr-gap-c (10:35–10:50)
// both fall outside the active window, and the entry must resolve to
// tfr-gap-c, whose midpoint is closest to the active trip's midpoint.
func TestTripsForRouteHandler_GapCase(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(loopRouteClock),
		"trips-for-route-gap.zip", gapFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := loopRouteClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-gap-c")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-gap-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)
}

// TestTripsForRouteHandler_InterlinedBlockAcrossServiceIDs verifies that a
// block still resolves when its active trip and its queried-route trip run
// under two different service_ids both active on the query date. Resolution
// must not require the block's trips to share one literal service_id.
func TestTripsForRouteHandler_InterlinedBlockAcrossServiceIDs(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock),
		"trips-for-route-interline-multisvc.zip", twoServiceIDsInterlineFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := tripsForRouteTestClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-a")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)
}

// TestTripsForRouteHandler_ReusedBlockIDAcrossDays verifies that a block ID
// reused across two unrelated service_ids on consecutive calendar days
// resolves to the queried-route trip under the active trip's own service_id
// (tfr-yest-a), not a same-block candidate from the other day that merely
// happens to have a numerically closer time-of-day midpoint (tfr-today-a).
func TestTripsForRouteHandler_ReusedBlockIDAcrossDays(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(afterMidnightClock),
		"trips-for-route-crossday.zip", crossDayBlockReuseFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := afterMidnightClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeStatus=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-yest-a")
	assert.Equal(t, expectedTripID, entry.TripId)
	require.NotNil(t, entry.Status)
	expectedActiveTripID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-yest-b")
	assert.Equal(t, expectedActiveTripID, entry.Status.ActiveTripID)
}

// TestResolveInterlinedEntryTripID_FetchedTripMissingTimes verifies that a
// fetchedTrip with a NULL cached time window (possible for a trip with no
// stop_times rows) can't be used to compute a nearest-midpoint match, so
// resolution reports ok=false rather than silently treating it as midnight.
func TestResolveInterlinedEntryTripID_FetchedTripMissingTimes(t *testing.T) {
	fetchedTrip := gtfsdb.Trip{
		ID:        "tfr-no-times",
		RouteID:   "tfr-route-otr",
		ServiceID: "tfr-svc",
		BlockID:   nulls.String("tfr-block"),
		// MinArrivalTime/MaxDepartureTime left at their zero value: Valid=false.
	}
	entries := map[string][]blockTripEntry{
		"tfr-block": {{ID: "tfr-candidate", MinArrivalTime: 0, MaxDepartureTime: 100}},
	}

	_, resolved := resolveInterlinedEntryTripID(fetchedTrip, tripsForRouteRouteID, tripsForRouteAgencyID,
		entries, map[string]string{})

	assert.False(t, resolved)
}

// TestResolveInterlinedEntryTripID_NoCandidateInBlock verifies that
// resolveInterlinedEntryTripID reports ok=false when the active trip's block
// has no entries at all (no queried-route trip was found anywhere in it).
// The handler falls back to the active trip's own ID in this case — matching
// legacy OBA and preserving one entry per active block — rather than
// dropping the entry, since the block's own discovery queries scope strictly
// to the queried route, making this branch defensive rather than reachable
// through normal per-route block discovery.
func TestResolveInterlinedEntryTripID_NoCandidateInBlock(t *testing.T) {
	fetchedTrip := gtfsdb.Trip{
		ID:               "tfr-orphan-b",
		RouteID:          "tfr-route-otr",
		ServiceID:        "tfr-svc",
		BlockID:          nulls.String("tfr-orphan-block"),
		MinArrivalTime:   sql.NullInt64{Int64: int64(11*time.Hour + 55*time.Minute), Valid: true},
		MaxDepartureTime: sql.NullInt64{Int64: int64(12*time.Hour + 5*time.Minute), Valid: true},
	}

	_, resolved := resolveInterlinedEntryTripID(fetchedTrip, tripsForRouteRouteID, tripsForRouteAgencyID,
		map[string][]blockTripEntry{}, map[string]string{})

	assert.False(t, resolved)
}

// TestTripsForRouteHandler_SituationReferences verifies that every situationId
// emitted on a list entry resolves to an entry in references.situations.
func TestTripsForRouteHandler_SituationReferences(t *testing.T) {
	// A dedicated manager, so the seeded alert is not clobbered by other tests
	// sharing the package-level fixture.
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// Real-time alerts carry the raw (un-prefixed) route ID from the feed.
	rawRouteID := "151"
	api.GtfsManager.AddAlertForTest(gogtfs.Alert{
		ID:               "test-alert-trips-for-route",
		InformedEntities: []gogtfs.AlertInformedEntity{{RouteID: &rawRouteID}},
		Header:           []gogtfs.AlertText{{Text: "Test Route Alert", Language: "en"}},
	})

	// ParseTimeParameter ignores api.Clock when no time= is given, so pin the
	// handler's window explicitly. Midday Pacific on a weekday inside the RABA
	// fixture's calendar range, when its trips are running.
	queryTime := time.Date(2025, 6, 12, 19, 0, 0, 0, time.UTC)
	url := fmt.Sprintf("/api/where/trips-for-route/25_151.json?key=TEST&includeSchedule=true&includeStatus=true&time=%d",
		queryTime.UnixMilli())

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected trips so situation references can be asserted")

	referenced := make(map[string]bool, len(model.Data.References.Situations))
	for _, situation := range model.Data.References.Situations {
		referenced[situation.ID] = true
	}

	var emitted []string
	for _, entry := range model.Data.List {
		for _, id := range entry.SituationIds {
			emitted = append(emitted, id)
			assert.True(t, referenced[id], "situationId %q must resolve to a situation reference", id)
		}
	}
	// The alert names a route but no agency, so its ID is scoped to the agency
	// the handler resolved the route under.
	require.Contains(t, emitted, "25_test-alert-trips-for-route",
		"expected the seeded alert to surface as a situationId")

	// The resolved date is handed to BuildTripStatus as well as stamped on the
	// entry, so the two must never disagree.
	for _, entry := range model.Data.List {
		if entry.Status == nil {
			continue
		}
		assert.Equal(t, entry.ServiceDate, entry.Status.ServiceDate.UnixMilli(),
			"entry %q reports a different serviceDate than its status", entry.TripId)
	}
}

// TestResolveDuplicatedBaseTrip covers the IDs a DUPLICATED real-time trip can
// arrive under, including the one that used to hand a nonexistent trip ID to
// the schedule and status builders.
func TestResolveDuplicatedBaseTrip(t *testing.T) {
	api := createTestApi(t)
	ctx := context.Background()

	trips, err := api.GtfsManager.GtfsDB.Queries.ListTrips(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, trips, "fixture must contain trips")
	staticTripID := trips[0].ID

	tests := []struct {
		name        string
		dupTripID   string
		wantTripID  string
		wantMatched bool
	}{
		{
			name:        "Feed reuses the static trip ID",
			dupTripID:   staticTripID,
			wantTripID:  staticTripID,
			wantMatched: true,
		},
		{
			name:        "Feed appends a numeric suffix to the static trip ID",
			dupTripID:   staticTripID + ".00060",
			wantTripID:  staticTripID,
			wantMatched: true,
		},
		{
			name:        "Neither the suffixed nor the stripped ID resolves",
			dupTripID:   "no-such-trip.00060",
			wantTripID:  "no-such-trip.00060",
			wantMatched: false,
		},
		{
			name:        "Synthetic ID with nothing to strip",
			dupTripID:   "no-such-trip",
			wantTripID:  "no-such-trip",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseTripID, baseTrip := api.resolveDuplicatedBaseTrip(ctx, tt.dupTripID)

			assert.Equal(t, tt.wantTripID, baseTripID)
			if tt.wantMatched {
				assert.Equal(t, tt.wantTripID, baseTrip.ID, "the returned trip must be the one the ID names")
			} else {
				assert.Empty(t, baseTrip.ID, "an unresolved trip must come back zeroed")
			}
		})
	}
}

// TestTripsForRouteHandler_BlockSequence_AdjacentTripReferences verifies that
// schedule.previousTripId and schedule.nextTripId trips — which are not part
// of the handler's fetched trips — are fully populated in references.trips
// (routeId, headsign, blockId, serviceId), not emitted as empty objects.
func TestTripsForRouteHandler_BlockSequence_AdjacentTripReferences(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(blockSequenceClock),
		"trips-for-route-block-seq.zip", blockSequenceFiles())
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	timeMs := blockSequenceClock.UnixMilli()
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeTrip=true&time=%d",
		combinedRouteID, timeMs)

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	entry := model.Data.List[0]
	assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-2"), entry.TripId)
	require.NotNil(t, entry.Schedule)
	assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-1"), entry.Schedule.PreviousTripId)
	assert.Equal(t, utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-3"), entry.Schedule.NextTripId)

	refTrips := make(map[string]models.Trip)
	for _, ref := range model.Data.References.Trips {
		refTrips[ref.ID] = ref
	}

	// The adjacent block trips are not part of the handler's fetched trips, but
	// their full records must still be present in the references.
	expectedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	expectedBlockID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-seq-block")
	expectedServiceID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-svc")
	for i, tripID := range []string{"tfr-trip-1", "tfr-trip-3"} {
		ref, ok := refTrips[utils.FormCombinedID(tripsForRouteAgencyID, tripID)]
		require.Truef(t, ok, "references must include adjacent trip %s", tripID)
		assert.Equalf(t, expectedRouteID, ref.RouteID, "adjacent trip %s must carry a routeId", tripID)
		assert.NotEmptyf(t, ref.TripHeadsign, "adjacent trip %s must carry a headsign", tripID)
		assert.Equalf(t, expectedBlockID, ref.BlockID, "adjacent trip %s must carry its blockId", tripID)
		assert.Equalf(t, expectedServiceID, ref.ServiceID, "adjacent trip %s must carry its serviceId", tripID)
		assert.Truef(t,
			i == 0 ||
				ref.TripHeadsign != refTrips[utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-1")].TripHeadsign,
			"adjacent trips must not both be the same record")
	}
}

// TestBuildTripReferences_FetchesUnprefetchedTrips verifies that trips
// referenced by entry TripId or Status.ActiveTripID are fetched and fully
// populated in references.trips when not pre-fetched.
func TestBuildTripReferences_FetchesUnprefetchedTrips(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(blockSequenceClock),
		"trips-for-route-block-seq.zip", blockSequenceFiles())
	ctx := context.Background()

	// Only tfr-trip-2 is pre-fetched; tfr-trip-1 (referenced by an entry
	// TripId) and tfr-trip-3 (referenced by Status.ActiveTripID) are not.
	preFetchedTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, "tfr-trip-2")
	require.NoError(t, err)

	entries := []models.TripsForRouteListEntry{
		{TripId: utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-1")},
		{
			TripId: utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-2"),
			Status: &models.TripStatus{ActiveTripID: utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-3")},
		},
	}

	references := api.buildTripReferences(ctx, tripReferenceParams{
		IncludeTrip:     true,
		Trips:           entries,
		PreFetchedTrips: []gtfsdb.Trip{preFetchedTrip},
	})

	refTrips := make(map[string]models.Trip)
	for _, ref := range references.Trips {
		refTrips[ref.ID] = ref
	}

	expectedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	expectedBlockID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-seq-block")
	expectedServiceID := utils.FormCombinedID(tripsForRouteAgencyID, "tfr-svc")

	for _, tc := range []struct {
		name   string
		tripID string
	}{
		{name: "entry TripId", tripID: "tfr-trip-1"},
		{name: "status ActiveTripID", tripID: "tfr-trip-3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := refTrips[utils.FormCombinedID(tripsForRouteAgencyID, tc.tripID)]
			require.Truef(t, ok, "references must include the trip referenced by %s", tc.name)
			assert.Equal(t, expectedRouteID, ref.RouteID)
			assert.NotEmpty(t, ref.TripHeadsign)
			assert.Equal(t, expectedBlockID, ref.BlockID)
			assert.Equal(t, expectedServiceID, ref.ServiceID)
		})
	}
}

// tripFetchFailureDB wraps the GTFS DB and fails GetTripsByIDs lookups that
// query any trip ID in failWhenArgsContain. The handler's active-trip fetch
// never queries those IDs, so only the buildTripReferences lookup fails.
type tripFetchFailureDB struct {
	gtfsdb.DBTX
	failWith            error
	failWhenArgsContain []string
}

func (f *tripFetchFailureDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "-- name: GetTripsByIDs") {
		for _, arg := range args {
			tripID, ok := arg.(string)
			if !ok {
				continue
			}
			for _, failID := range f.failWhenArgsContain {
				if tripID == failID {
					return nil, f.failWith
				}
			}
		}
	}
	return f.DBTX.QueryContext(ctx, query, args...)
}

// TestTripsForRouteHandler_ReferenceLookupFailureDegradesGracefully verifies
// that a failure to fetch a referenced trip degrades gracefully: the handler
// logs and continues, still returning a 200 with the other references intact,
// rather than failing the whole response.
func TestTripsForRouteHandler_ReferenceLookupFailureDegradesGracefully(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(blockSequenceClock),
		"trips-for-route-block-seq.zip", blockSequenceFiles())

	// Block-adjacent trips are referenced by tfr-trip-2's schedule but are
	// not part of the handler's fetched trips.
	api.GtfsManager.GtfsDB.Queries = gtfsdb.New(&tripFetchFailureDB{
		DBTX:                api.GtfsManager.GtfsDB.DB,
		failWith:            errors.New("forced lookup failure"),
		failWhenArgsContain: []string{"tfr-trip-1", "tfr-trip-3"},
	})

	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&includeTrip=true&time=%d",
		combinedRouteID, blockSequenceClock.UnixMilli())

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, http.StatusOK, model.Code)
	require.Len(t, model.Data.List, 1)

	refTrips := make(map[string]models.Trip)
	for _, ref := range model.Data.References.Trips {
		refTrips[ref.ID] = ref
	}
	require.Contains(t, refTrips, utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-2"),
		"the pre-fetched entry trip must still be referenced despite the lookup failure")
	require.NotContains(t, refTrips, utils.FormCombinedID(tripsForRouteAgencyID, "tfr-trip-1"),
		"the unfetchable adjacent trip must not appear in references")
}

// TestTripsForRouteHandler_StopRoutesResolveInReferences verifies that every route referenced by a stop
// (including routes no returned trip runs on) resolve to a route on references.routes and that these
// route agencies resolve in references.agencies.
func TestTripsForRouteHandler_StopRoutesResolveInReferences(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tripsForRouteTestClock),
		"trips-for-route-orphan-stop-route.zip", orphanStopRouteFiles())

	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&includeSchedule=true&time=%d",
		combinedRouteID, tripsForRouteTestClock.UnixMilli())

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.List, 1, "only the queried route's trip should be active at the pinned clock")

	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteTripID)
	assert.Equal(t, expectedTripID, model.Data.List[0].TripId,
		"the single returned trip must be the queried route's, not the orphan route's")

	refs := model.Data.References

	referenceRouteIDs := make(map[string]bool, len(refs.Routes))
	for _, route := range refs.Routes {
		referenceRouteIDs[route.ID] = true
	}
	referenceAgencyIDs := make(map[string]bool, len(refs.Agencies))
	for _, agency := range refs.Agencies {
		referenceAgencyIDs[agency.ID] = true
	}

	combinedOrphanRouteID := utils.FormCombinedID(orphanRouteAgencyID, orphanRouteID)
	require.Len(t, refs.Stops, 2, "references.stops should reference both fixture stops when includeSchedule=true")
	for _, stop := range refs.Stops {
		assert.Contains(t, stop.RouteIDs, combinedOrphanRouteID,
			"stop %s should name the orphan route from the other agency", stop.ID)
	}

	for _, stop := range refs.Stops {
		for _, routeID := range stop.RouteIDs {
			assert.True(t, referenceRouteIDs[routeID],
				"stop %s emits routeId %s, which must resolve in references.routes", stop.ID, routeID)
		}
		for _, routeID := range stop.StaticRouteIDs {
			assert.True(t, referenceRouteIDs[routeID],
				"stop %s emits staticRouteId %s, which must resolve in references.routes", stop.ID, routeID)
		}
	}

	assert.Contains(t, referenceAgencyIDs, orphanRouteAgencyID,
		"references.agencies should contain agency: %s for orphaned route: %s", orphanRouteAgencyID, orphanRouteID)
	for _, route := range refs.Routes {
		assert.True(t, referenceAgencyIDs[route.AgencyID],
			"references.routes entry %s has agencyId %s, which must resolve in references.agencies", route.ID, route.AgencyID)
	}
}
