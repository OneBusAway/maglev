package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
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
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForRouteTestClock is the clock used by the synthetic-fixture tests below.
// The fixture inserts a trip with stop_times at 11:55 and 12:05, so a clock at
// 12:00 falls inside the handler's (-30min/+10min) active window.
var tripsForRouteTestClock = time.Date(2025, 6, 12, 12, 0, 0, 0, time.UTC)

const (
	tripsForRouteAgencyID = "tfr-agency"
	tripsForRouteRouteID  = "tfr-route"
	tripsForRouteTripID   = "tfr-trip"
	tripsForRouteStop1ID  = "tfr-stop1"
	tripsForRouteStop2ID  = "tfr-stop2"
	tripsForRouteHeadsign = "Test Headsign"
)

// createTestApiWithTripsForRouteFixture builds a RestAPI backed by a minimal
// in-memory GTFS dataset with a single trip active at tripsForRouteTestClock.
// This guarantees the trips-for-route handler returns at least one entry, so
// the per-entry assertions below validate real data instead of running over
// an empty list (the RABA fixture's block_trip_indexes don't cover this path).
func createTestApiWithTripsForRouteFixture(t *testing.T, c clock.Clock) *RestAPI {
	t.Helper()
	ctx := context.Background()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	files := map[string]string{
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
		// First stop at 11:55, last at 12:05 — pinned clock at 12:00 falls inside the
		// handler's (-30min/+10min) active window.
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			tripsForRouteTripID + ",11:55:00,11:55:00," + tripsForRouteStop1ID + ",1\n" +
			tripsForRouteTripID + ",12:05:00,12:05:00," + tripsForRouteStop2ID + ",2\n",
	}
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	zipPath := filepath.Join(t.TempDir(), "trips-for-route.zip")
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

func TestTripsForRouteHandler_DifferentRoutes(t *testing.T) {
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))
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
			name:         "Non-existent Route",
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

	// ParseTimeParameter ignores api.Clock when no time= is given, so pass it explicitly
	// to pin the handler's time window to our fixture.
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
			assert.False(t, model.Data.OutOfRange)

			assert.GreaterOrEqual(t, len(model.Data.List), tt.minExpected)
			assert.LessOrEqual(t, len(model.Data.List), tt.maxExpected)

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
			for _, s := range refs.Stops {
				assert.Contains(t, s.ID, "_",
					"reference stop IDs must be combined IDs to match schedule stop times")
			}
		})
	}
}

func TestTripsForRouteHandler_ScheduleInclusion(t *testing.T) {
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))
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

func TestTripsForRouteHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/trips-for-route/1110.json?key=TEST"

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
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
