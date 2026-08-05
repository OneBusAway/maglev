package restapi

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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
	// Real-time vehicle GPS position injected by the realtime fixture. Distinct
	// from the fixture stops so status.position reflects the vehicle, not a stop.
	tripsForRouteRealtimeLat = 37.7885
	tripsForRouteRealtimeLon = -122.3962
)

// buildTripsForRouteFixtureZip writes the synthetic GTFS dataset used by the
// trips-for-route fixture tests to a temp directory and returns its path.
func buildTripsForRouteFixtureZip(t *testing.T) string {
	t.Helper()

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
	return zipPath
}

// createTestApiWithTripsForRouteFixture builds a RestAPI backed by a minimal
// in-memory GTFS dataset with a single trip active at tripsForRouteTestClock.
// This guarantees the trips-for-route handler returns at least one entry, so
// the per-entry assertions below validate real data instead of running over
// an empty list (the RABA fixture's block_trip_indexes don't cover this path).
func createTestApiWithTripsForRouteFixture(t *testing.T, c clock.Clock) *RestAPI {
	t.Helper()
	ctx := context.Background()

	zipPath := buildTripsForRouteFixtureZip(t)

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

// createTestApiWithScheduledRealtimePosition builds a RestAPI backed by the same
// synthetic GTFS as createTestApiWithTripsForRouteFixture, plus a real-time feed
// that injects a SCHEDULED vehicle tracking the fixture trip with a GPS position.
// The vehicle goes through the production updateFeedRealtime path, so the
// trips-for-route handler builds entry status from real-time vehicle data.
func createTestApiWithScheduledRealtimePosition(t *testing.T, c clock.Clock) (*RestAPI, func()) {
	t.Helper()

	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String("2.0"),
			Timestamp:           proto.Uint64(uint64(tripsForRouteTestClock.Unix())),
		},
		Entity: []*gtfsrt.FeedEntity{
			{
				Id: proto.String("tracked-vehicle"),
				Vehicle: &gtfsrt.VehiclePosition{
					Vehicle: &gtfsrt.VehicleDescriptor{Id: proto.String("tfr-veh-1")},
					Trip: &gtfsrt.TripDescriptor{
						TripId:  proto.String(tripsForRouteTripID),
						RouteId: proto.String(tripsForRouteRouteID),
					},
					Position: &gtfsrt.Position{
						Latitude:  proto.Float32(tripsForRouteRealtimeLat),
						Longitude: proto.Float32(tripsForRouteRealtimeLon),
					},
					Timestamp: proto.Uint64(uint64(tripsForRouteTestClock.Unix())),
				},
			},
		},
	}
	feedPayload, err := proto.Marshal(feed)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(feedPayload)
	})
	server := httptest.NewServer(mux)

	ctx := context.Background()
	zipPath := buildTripsForRouteFixtureZip(t)

	gtfsConfig := gtfs.Config{
		GtfsURL:      zipPath,
		GTFSDataPath: ":memory:",
		RTFeeds: []gtfs.RTFeedConfig{
			{
				ID:                  "test-feed",
				VehiclePositionsURL: server.URL + "/vehicle-positions",
				RefreshInterval:     30,
				Enabled:             true,
			},
		},
	}

	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)

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

	cleanup := func() {
		api.Shutdown()
		server.Close()
		gtfsManager.Shutdown()
	}
	return api, cleanup
}

// TestTripsForRouteHandler_StatusFields verifies the real-time status sub-object
// fields on trips-for-route entries. A SCHEDULED vehicle with a GPS position
// tracks the fixture trip, so its entry status must carry the vehicle position,
// the -1 occupancy sentinel when the feed omits occupancy, and non-nil empty
// situationIds / vehicleFeatures slices.
func TestTripsForRouteHandler_StatusFields(t *testing.T) {
	api, cleanup := createTestApiWithScheduledRealtimePosition(t, clock.NewMockClock(tripsForRouteTestClock))
	defer cleanup()

	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)
	url := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%d",
		combinedRouteID, tripsForRouteTestClock.UnixMilli())

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)

	expectedTripID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteTripID)
	require.Len(t, model.Data.List, 1, "the single fixture trip should be returned")
	entry := model.Data.List[0]
	assert.Equal(t, expectedTripID, entry.TripId)

	require.NotNil(t, entry.Status, "entry should carry a real-time status")

	assert.GreaterOrEqual(t, entry.Status.BlockTripSequence, 0,
		"blockTripSequence should be a non-negative integer")

	// GTFS-RT stores coordinates as float32, so compare against the round-tripped value.
	assert.Equal(t, float64(float32(tripsForRouteRealtimeLat)), entry.Status.Position.Lat)
	assert.Equal(t, float64(float32(tripsForRouteRealtimeLon)), entry.Status.Position.Lon)

	assert.Equal(t, -1, entry.Status.OccupancyCount,
		"occupancyCount should use the -1 sentinel when the feed omits occupancy")

	require.NotNil(t, entry.Status.SituationIDs, "situationIds must be a non-null slice")
	assert.Empty(t, entry.Status.SituationIDs, "situationIds should be exactly [] with no situations")

	require.NotNil(t, entry.Status.VehicleFeatures, "vehicleFeatures must be a non-null slice")
	assert.Empty(t, entry.Status.VehicleFeatures, "vehicleFeatures should be exactly [] with no data")
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

func TestTripsForRouteHandler_TripInclusion(t *testing.T) {
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))
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
	api := createTestApi(t)
	defer api.Shutdown()

	endpoint := "/api/where/trips-for-route/1110.json?key=TEST"

	resp, model := callAPIHandler[TripsForRouteResponse](t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestTripsForRouteHandler_ReferencesInclusion(t *testing.T) {
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))
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
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))
	combinedRouteID := utils.FormCombinedID(tripsForRouteAgencyID, tripsForRouteRouteID)

	outOfServiceTimeMs := tripsForRouteTestClock.Add(12 * time.Hour).UnixMilli()

	tests := []struct {
		name              string
		includeReferences string
		wantRefsPopulated bool
	}{
		{
			name:              "Empty List - Include References Explicit",
			includeReferences: "true",
			wantRefsPopulated: true,
		},
		{
			name:              "Empty List - Exclude References",
			includeReferences: "false",
			wantRefsPopulated: false,
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
	stopIDsMap := map[string]bool{}

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
	stopIDsMap := map[string]bool{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]bool{
		"1001": true,
		"1002": true,
		"1003": true,
	}, stopIDsMap)
}

func TestCollectStopIDsFromSchedule_SkipsMalformedIDs(t *testing.T) {
	schedule := &models.TripsSchedule{
		StopTimes: []models.StopTime{
			{StopID: "25_good"},
			{StopID: "no-underscore"},
		},
	}
	stopIDsMap := map[string]bool{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Equal(t, map[string]bool{"good": true}, stopIDsMap,
		"malformed stop IDs must be silently skipped")
}

func TestCollectStopIDsFromSchedule_EmptyStopTimes(t *testing.T) {
	schedule := &models.TripsSchedule{StopTimes: []models.StopTime{}}
	stopIDsMap := map[string]bool{}

	collectStopIDsFromSchedule(schedule, stopIDsMap)

	assert.Empty(t, stopIDsMap)
}
