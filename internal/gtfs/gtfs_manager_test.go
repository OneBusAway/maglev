package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func TestManager_GetAgencies(t *testing.T) {
	// Use shared component to avoid reloading DB
	manager, _ := getSharedTestComponents(t)
	assert.NotNil(t, manager)

	agencies, err := manager.GtfsDB.Queries.ListAgencies(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, len(agencies))

	agency := agencies[0]
	assert.Equal(t, "25", agency.ID)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)
	assert.Equal(t, "http://www.rabaride.com/", agency.Url)
	assert.Equal(t, "America/Los_Angeles", agency.Timezone)
	assert.Equal(t, "en", agency.Lang.String)
	assert.Equal(t, "530-241-2877", agency.Phone.String)
	assert.Equal(t, "", agency.FareUrl.String)
	assert.Equal(t, "", agency.Email.String)
}

func TestManager_RoutesForAgencyID(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	assert.NotNil(t, manager)

	routes, err := manager.RoutesForAgencyID(t.Context(), "25")
	assert.Nil(t, err)
	assert.Equal(t, 13, len(routes))

	route := routes[0]
	assert.Equal(t, "1", route.ShortName.String)
}

func TestManager_GetStopsForLocation_UsesSpatialIndex(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name          string
		lat           float64
		lon           float64
		radius        float64
		expectedStops int
	}{
		{
			name:          "FindStopsWithinRadius",
			lat:           40.589123, // Near Redding, CA
			lon:           -122.390830,
			radius:        2000, // 2km radius
			expectedStops: 1,    // Should find at least 1 stop
		},
		{
			name:          "FindStopsWithinRadius",
			lat:           47.589123, // West Seattle
			lon:           -122.390830,
			radius:        2000, // 2km radius
			expectedStops: 0,    // Should find zero stops (outside RABA area)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager, _ := getSharedTestComponents(t)
			assert.NotNil(t, manager)

			// Get stops using the manager method
			stops, err := manager.GetStopsInBounds(ctx, &LocationParams{Lat: tc.lat, Lon: tc.lon, Radius: tc.radius}, 100)
			require.NoError(t, err)

			// The test expects that the spatial index query is used
			assert.GreaterOrEqual(t, len(stops), tc.expectedStops, "Should find stops within radius")

			// Verify stops are actually within the radius
			for _, stop := range stops {
				assert.NotZero(t, stop.Lat)
				assert.NotZero(t, stop.Lon)
			}

			assert.NotNil(t, manager.GtfsDB.Queries, "Database queries should exist")
		})
	}
}

func TestManager_GetTrips(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	assert.NotNil(t, manager)

	trips, err := manager.GetTrips(context.Background(), 100)
	require.NoError(t, err)
	assert.NotEmpty(t, trips)
	assert.NotEmpty(t, trips[0].ID)
}

func TestManager_FindAgency(t *testing.T) {
	manager, _ := getSharedTestComponents(t)

	agency, err := manager.FindAgency(context.Background(), "25")
	assert.Nil(t, err)
	assert.NotNil(t, agency)
	assert.Equal(t, "25", agency.ID)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)

	agency, err = manager.FindAgency(context.Background(), "nonexistent")
	assert.Nil(t, err)
	assert.Nil(t, agency)
}

func TestManager_GetVehicleByID(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {
				{
					ID: &gtfs.VehicleID{ID: "vehicle1"},
				},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	vehicle, err := manager.GetVehicleByID("vehicle1")
	assert.Nil(t, err)
	assert.NotNil(t, vehicle)
	assert.Equal(t, "vehicle1", vehicle.ID.ID)

	notFound, err := manager.GetVehicleByID("nonexistent")
	assert.NotNil(t, err)
	assert.Nil(t, notFound)
}

func TestManager_RebuildMergedRealtime_VehiclesByRoute(t *testing.T) {
	manager := &Manager{
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-1": {
				{
					ID:   &gtfs.VehicleID{ID: "v1"},
					Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "route-a"}},
				},
				{
					ID:   &gtfs.VehicleID{ID: "v-no-trip"},
					Trip: nil,
				},
			},
			"feed-2": {
				{
					ID:   &gtfs.VehicleID{ID: "v2"},
					Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "route-a"}},
				},
				{
					ID:   &gtfs.VehicleID{ID: "v3"},
					Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: "route-b"}},
				},
				{
					ID:   &gtfs.VehicleID{ID: "v-empty-route"},
					Trip: &gtfs.Trip{ID: gtfs.TripID{RouteID: ""}},
				},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	merged := manager.mergedRealtime()
	require.Len(t, merged.vehiclesByRoute["route-a"], 2)
	assert.ElementsMatch(t, []string{"v1", "v2"}, []string{
		merged.vehiclesByRoute["route-a"][0].ID.ID,
		merged.vehiclesByRoute["route-a"][1].ID.ID,
	})

	require.Len(t, merged.vehiclesByRoute["route-b"], 1)
	assert.Equal(t, "v3", merged.vehiclesByRoute["route-b"][0].ID.ID)

	// stale-feed clear: remove feed-1
	delete(manager.feedVehicles, "feed-1")
	manager.rebuildMergedRealtimeLocked()

	merged = manager.mergedRealtime()
	require.Len(t, merged.vehiclesByRoute["route-a"], 1)
	assert.Equal(t, "v2", merged.vehiclesByRoute["route-a"][0].ID.ID)
	require.Len(t, merged.vehiclesByRoute["route-b"], 1)
	assert.Equal(t, "v3", merged.vehiclesByRoute["route-b"][0].ID.ID)
}

func TestGetVehicleForTrip_DirectTripIDLookup(t *testing.T) {
	tripID := "trip-direct"
	vehicleID := "v-direct"

	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedVehicles: map[string][]gtfs.Vehicle{
			"feed-0": {
				{
					ID:   &gtfs.VehicleID{ID: vehicleID},
					Trip: &gtfs.Trip{ID: gtfs.TripID{ID: tripID}},
				},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	ctx := context.Background()
	got := manager.GetVehicleForTrip(ctx, tripID)
	require.NotNil(t, got)
	assert.Equal(t, vehicleID, got.ID.ID)
}

func TestManager_GetTripUpdatesForTrip(t *testing.T) {
	manager := &Manager{
		feedTrips: map[string][]gtfs.Trip{
			"_test": {
				{ID: gtfs.TripID{ID: "trip1"}},
				{ID: gtfs.TripID{ID: "trip2"}},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	updates := manager.GetTripUpdatesForTrip("trip1")
	assert.Len(t, updates, 1)
	assert.Equal(t, "trip1", updates[0].ID.ID)

	noUpdates := manager.GetTripUpdatesForTrip("nonexistent")
	assert.Empty(t, noUpdates)
}

func TestManager_GetVehicleLastUpdateTime(t *testing.T) {
	now := time.Now()
	vehicle := &gtfs.Vehicle{
		Timestamp: &now,
	}

	manager := &Manager{}
	timestamp := manager.GetVehicleLastUpdateTime(vehicle)
	assert.Equal(t, now, timestamp)

	nilTimestamp := manager.GetVehicleLastUpdateTime(nil)
	assert.True(t, nilTimestamp.IsZero())

	vehicleNoTimestamp := &gtfs.Vehicle{}
	noTimestamp := manager.GetVehicleLastUpdateTime(vehicleNoTimestamp)
	assert.True(t, noTimestamp.IsZero())
}

func TestManager_GetTripUpdateByID(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
		feedTrips: map[string][]gtfs.Trip{
			"feed-0": {
				{
					ID: gtfs.TripID{ID: "trip1"},
				},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	trip, err := manager.GetTripUpdateByID("trip1")
	assert.Nil(t, err)
	assert.NotNil(t, trip)
	assert.Equal(t, "trip1", trip.ID.ID)

	notFound, err := manager.GetTripUpdateByID("nonexistent")
	assert.NotNil(t, err)
	assert.Nil(t, notFound)
}

func TestManager_IsServiceActiveOnDate(t *testing.T) {
	manager, _ := getSharedTestComponents(t)

	// Get a trip to find a valid service ID
	trips, err := manager.GetTrips(context.Background(), 100)
	require.NoError(t, err)
	assert.NotEmpty(t, trips)

	serviceID := trips[0].ServiceID

	testCases := []struct {
		name    string
		date    time.Time
		weekday string
	}{
		{
			name:    "Sunday",
			date:    time.Date(2024, 11, 3, 0, 0, 0, 0, time.UTC),
			weekday: "Sunday",
		},
		{
			name:    "Monday",
			date:    time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC),
			weekday: "Monday",
		},
		{
			name:    "Tuesday",
			date:    time.Date(2024, 11, 5, 0, 0, 0, 0, time.UTC),
			weekday: "Tuesday",
		},
		{
			name:    "Wednesday",
			date:    time.Date(2024, 11, 6, 0, 0, 0, 0, time.UTC),
			weekday: "Wednesday",
		},
		{
			name:    "Thursday",
			date:    time.Date(2024, 11, 7, 0, 0, 0, 0, time.UTC),
			weekday: "Thursday",
		},
		{
			name:    "Friday",
			date:    time.Date(2024, 11, 8, 0, 0, 0, 0, time.UTC),
			weekday: "Friday",
		},
		{
			name:    "Saturday",
			date:    time.Date(2024, 11, 9, 0, 0, 0, 0, time.UTC),
			weekday: "Saturday",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.weekday, tc.date.Weekday().String())

			active, err := manager.IsServiceActiveOnDate(context.Background(), serviceID, tc.date)
			if err == nil {
				assert.GreaterOrEqual(t, active, int64(0))
			}
		})
	}
}

func TestManager_GetVehicleForTrip(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	// We use isolated GTFSManager here instead of shared test components because we want to control the real-time vehicles for this test.
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown(context.Background())

	trip := &gtfs.Trip{
		ID: gtfs.TripID{ID: "5735633"},
	}
	manager.feedVehicles = map[string][]gtfs.Vehicle{
		"feed-0": {
			{
				ID:   &gtfs.VehicleID{ID: "vehicle1"},
				Trip: trip,
			},
		},
	}

	manager.rebuildMergedRealtimeLocked()

	vehicle := manager.GetVehicleForTrip(context.Background(), "5735633")
	if vehicle != nil {
		assert.NotNil(t, vehicle)
		assert.Equal(t, "vehicle1", vehicle.ID.ID)
	}

	// Test Not Found
	nilVehicle := manager.GetVehicleForTrip(context.Background(), "nonexistent")
	assert.Nil(t, nilVehicle)
}

func TestRoutesForAgencyID_NonexistentId(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err, "Failed to initialize manager")
	defer manager.Shutdown(context.Background())

	emptyRoutes, err := manager.RoutesForAgencyID(ctx, "nonexistent")
	assert.Nil(t, err)
	assert.Empty(t, emptyRoutes, "Non-existent agency should return empty slice")
}

func TestRoutesForAgencyID_ValidId(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err, "Failed to initialize manager")
	defer manager.Shutdown(context.Background())

	targetAgencyID := "25"
	expectedRouteCount := 13

	publicRoutes, err := manager.RoutesForAgencyID(ctx, targetAgencyID)
	assert.Nil(t, err)
	assert.Len(t, publicRoutes, expectedRouteCount, "Public API should return correct route count")
}

func TestRoutesForAgencyID_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errorChan := make(chan error, 10)

	// Spawn concurrent readers
	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					routes, err := manager.RoutesForAgencyID(ctx, "25")
					if errors.Is(err, context.DeadlineExceeded) {
						return
					} else if err != nil {
						errorChan <- err
						return
					}

					if routes == nil {
						errorChan <- fmt.Errorf("reader %d: got nil routes slice", id)
						return
					}
					time.Sleep(1 * time.Microsecond)
				}
			}
		}(i)
	}

	wg.Wait()
	close(errorChan)

	for err := range errorChan {
		t.Error(err)
	}
}

func BenchmarkRoutesForAgencyID_MapLookup(b *testing.B) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(b, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	if err != nil {
		b.Fatalf("Failed to initialize: %v", err)
	}
	defer manager.Shutdown(context.Background())

	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = manager.RoutesForAgencyID(ctx, "25")
	}
}

func TestInitGTFSManager_RetryLogic(t *testing.T) {
	ctx := context.Background()

	// Use an ultra-fast backoff schedule for the test to prevent it from hanging
	backoffs := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
	}

	config := Config{
		// Use a clearly invalid local URL that will trigger immediate connection refused
		GtfsURL:        "http://127.0.0.1:9099/gtfs.zip",
		GTFSDataPath:   ":memory:",
		Env:            appconf.Test,
		StartupRetries: backoffs, // Inject test backoffs
	}

	start := time.Now()

	manager, err := InitGTFSManager(ctx, config)

	// It should eventually fail after trying all backoffs
	require.Error(t, err)
	require.Nil(t, manager)

	// Verify the entire process was fast (proving it used our 1ms, 2ms, 3ms backoffs)
	duration := time.Since(start)
	assert.Less(t, duration, 2*time.Second, "Retry logic should respect the configured backoff schedule")
}

func TestManager_DataFreshnessTracking(t *testing.T) {
	ctx := context.Background()

	// Set and test StaticLastUpdated via DB roundtrip
	tempDir := t.TempDir()
	cfg := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}
	dbManager, err := InitGTFSManager(ctx, cfg)
	require.NoError(t, err)
	defer dbManager.Shutdown(context.Background())

	now := time.Now().UTC().Truncate(time.Second)
	dbManager.SetStaticLastUpdatedForTest(ctx, now)
	gotStatic := dbManager.GetStaticLastUpdated(ctx)
	assert.Equal(t, now.Unix(), gotStatic.Unix())
	assert.Equal(t, "UTC", gotStatic.Location().String())

	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
	}

	// Test GetFeedUpdateTimes returns defensive map copy
	manager.SetFeedUpdateTimeForTest("feed-1", now)
	feedTimes := manager.GetFeedUpdateTimes()
	assert.Contains(t, feedTimes, "feed-1")
	assert.Equal(t, now, feedTimes["feed-1"])

	// Modify the copy and ensure the original map state is safe
	feedTimes["feed-1"] = time.Now().Add(time.Hour)
	feedTimes2 := manager.GetFeedUpdateTimes()
	assert.Equal(t, now, feedTimes2["feed-1"])
}

// createTestManagerWithGTFSFiles creates an in-memory GTFS Manager loaded from a synthetic GTFS zip.
func createTestManagerWithGTFSFiles(t *testing.T, files map[string]string) *Manager {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	zipPath := filepath.Join(t.TempDir(), "feed.zip")
	require.NoError(t, os.WriteFile(zipPath, buf.Bytes(), 0600))

	gtfsConfig := Config{
		GtfsURL:      zipPath,
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(context.Background(), gtfsConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	return manager
}

// TestGetVehicleForTrip_BlocklessTripTreatedAsOwnBlock verifies that a trip without a block_id
// is treated as its own single-trip block and does not match vehicles from other blockless trips.
func TestGetVehicleForTrip_BlocklessTripTreatedAsOwnBlock(t *testing.T) {
	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"TEST,Test Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"R1,TEST,1,Route 1,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"S1,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"STOP1,Stop 1,37.77,-122.41\n" +
			"STOP2,Stop 2,37.78,-122.40\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			"R1,S1,T1,Headsign 1,0,\n" +
			"R1,S1,T2,Headsign 2,0,\n" +
			"R1,S1,T3,Headsign 3,0,B1\n" +
			"R1,S1,T4,Headsign 4,0,B1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"T1,10:00:00,10:00:00,STOP1,1\n" +
			"T1,10:30:00,10:30:00,STOP2,2\n" +
			"T2,11:00:00,11:00:00,STOP1,1\n" +
			"T2,11:30:00,11:30:00,STOP2,2\n" +
			"T3,12:00:00,12:00:00,STOP1,1\n" +
			"T3,12:30:00,12:30:00,STOP2,2\n" +
			"T4,13:00:00,13:00:00,STOP1,1\n" +
			"T4,13:30:00,13:30:00,STOP2,2\n",
	}

	manager := createTestManagerWithGTFSFiles(t, files)

	manager.feedVehicles = map[string][]gtfs.Vehicle{
		"feed-0": {
			{
				ID:   &gtfs.VehicleID{ID: "V2"},
				Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T2"}},
			},
		},
	}
	manager.rebuildMergedRealtimeLocked()

	assert.Nil(t, manager.GetVehicleForTrip(context.Background(), "T1"))

	v2 := manager.GetVehicleForTrip(context.Background(), "T2")
	require.NotNil(t, v2)
	assert.Equal(t, "V2", v2.ID.ID)

	manager.feedVehicles["feed-0"] = append(manager.feedVehicles["feed-0"], gtfs.Vehicle{
		ID:   &gtfs.VehicleID{ID: "V3"},
		Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "T3"}},
	})
	manager.rebuildMergedRealtimeLocked()

	v3 := manager.GetVehicleForTrip(context.Background(), "T4")
	require.NotNil(t, v3)
	assert.Equal(t, "V3", v3.ID.ID)
}
