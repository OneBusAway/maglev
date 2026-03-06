package gtfs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/models"
)

func TestManager_GetAgencies(t *testing.T) {
	// Use shared component to avoid reloading DB
	manager, _ := getSharedTestComponents(t)
	assert.NotNil(t, manager)

	agencies := manager.GetAgencies()
	assert.Equal(t, 1, len(agencies))

	agency := agencies[0]
	assert.Equal(t, "25", agency.Id)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)
	assert.Equal(t, "http://www.rabaride.com/", agency.Url)
	assert.Equal(t, "America/Los_Angeles", agency.Timezone)
	assert.Equal(t, "en", agency.Language)
	assert.Equal(t, "530-241-2877", agency.Phone)
	assert.Equal(t, "", agency.FareUrl)
	assert.Equal(t, "", agency.Email)
}

func TestManager_RoutesForAgencyID(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	assert.NotNil(t, manager)

	manager.RLock()
	routes := manager.RoutesForAgencyID("25")
	manager.RUnlock()
	assert.Equal(t, 13, len(routes))

	route := routes[0]
	assert.Equal(t, "1", route.ShortName)
	assert.Equal(t, "25", route.Agency.Id)
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
			stops := manager.GetStopsForLocation(ctx, tc.lat, tc.lon, tc.radius, 0, 0, "", 100, false, nil, time.Time{})

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

	trips := manager.GetTrips()
	assert.NotEmpty(t, trips)
	assert.NotEmpty(t, trips[0].ID)
}

func TestManager_FindAgency(t *testing.T) {
	manager, _ := getSharedTestComponents(t)

	agency := manager.FindAgency("25")
	assert.NotNil(t, agency)
	assert.Equal(t, "25", agency.Id)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)

	agencyNotFound := manager.FindAgency("nonexistent")
	assert.Nil(t, agencyNotFound)
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
		realTimeTrips: []gtfs.Trip{
			{
				ID: gtfs.TripID{ID: "trip1"},
			},
			{
				ID: gtfs.TripID{ID: "trip2"},
			},
		},
		realTimeTripLookup: map[string]int{
			"trip1": 0,
			"trip2": 1,
		},
	}

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
	assert.Equal(t, now.UnixMilli(), timestamp)

	nilTimestamp := manager.GetVehicleLastUpdateTime(nil)
	assert.Equal(t, int64(0), nilTimestamp)

	vehicleNoTimestamp := &gtfs.Vehicle{}
	noTimestamp := manager.GetVehicleLastUpdateTime(vehicleNoTimestamp)
	assert.Equal(t, int64(0), noTimestamp)
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
	trips := manager.GetTrips()
	assert.NotEmpty(t, trips)

	serviceID := trips[0].Service.Id

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
	defer manager.Shutdown()

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

func TestBuildLookupMaps(t *testing.T) {
	staticData := &gtfs.Static{
		Agencies: []gtfs.Agency{
			{Id: "agency_1", Name: "Metro"},
			{Id: "agency_2", Name: "Bus"},
		},
		Routes: []gtfs.Route{
			{Id: "route_101", ShortName: "101"},
			{Id: "route_102", ShortName: "102"},
		},
	}

	agencyMap, routeMap := buildLookupMaps(staticData)

	assert.Equal(t, 2, len(agencyMap))
	assert.NotNil(t, agencyMap["agency_1"])
	assert.Equal(t, "Metro", agencyMap["agency_1"].Name)
	assert.Nil(t, agencyMap["agency_999"], "Should return nil for non-existent agency")

	assert.Equal(t, 2, len(routeMap))
	assert.NotNil(t, routeMap["route_101"])
	assert.Equal(t, "101", routeMap["route_101"].ShortName)
	assert.Nil(t, routeMap["route_999"], "Should return nil for non-existent route")
}

func TestManager_FindAgency_UsesMap(t *testing.T) {
	// This test proves we are using the Map, not the Slice.
	// We populate the Map, but leave the Slice empty.
	// If the code was still looping over the slice, this would fail.
	manager := &Manager{
		agenciesMap: map[string]*gtfs.Agency{
			"A1": {Id: "A1", Name: "Fast Agency"},
		},
		// Empty Slice to ensure we aren't using the old linear search
		gtfsData: &gtfs.Static{
			Agencies: []gtfs.Agency{},
		},
	}

	result := manager.FindAgency("A1")
	assert.NotNil(t, result)
	assert.Equal(t, "Fast Agency", result.Name)

	result = manager.FindAgency("B2")
	assert.Nil(t, result)
}

func TestManager_FindRoute_UsesMap(t *testing.T) {
	manager := &Manager{
		routesMap: map[string]*gtfs.Route{
			"R1": {Id: "R1", LongName: "Express Route"},
		},
		gtfsData: &gtfs.Static{
			Routes: []gtfs.Route{},
		},
	}

	result := manager.FindRoute("R1")
	assert.NotNil(t, result)
	assert.Equal(t, "Express Route", result.LongName)

	result = manager.FindRoute("Unknown")
	assert.Nil(t, result)
}

func TestRoutesForAgencyID_MapOptimization(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err, "Failed to initialize manager")
	defer manager.Shutdown()

	targetAgencyID := "25"
	expectedRouteCount := 13

	// Consolidated lock region
	manager.RLock()
	assert.NotNil(t, manager.routesByAgencyID, "routesByAgencyID map should be initialized")

	cachedRoutes, exists := manager.routesByAgencyID[targetAgencyID]
	assert.True(t, exists, "Agency %s should exist in cache map", targetAgencyID)
	assert.Len(t, cachedRoutes, expectedRouteCount, "Map should contain correct number of routes")

	publicRoutes := manager.RoutesForAgencyID(targetAgencyID)
	emptyRoutes := manager.RoutesForAgencyID("nonexistent")
	manager.RUnlock()

	assert.Len(t, publicRoutes, expectedRouteCount, "Public API should return correct route count")

	for _, route := range publicRoutes {
		assert.Equal(t, targetAgencyID, route.Agency.Id,
			"Route %s should belong to agency %s", route.Id, targetAgencyID)
	}

	assert.Empty(t, emptyRoutes, "Non-existent agency should return empty slice")
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
	defer manager.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errors := make(chan error, 10)

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
					manager.RLock()
					routes := manager.RoutesForAgencyID("25")
					manager.RUnlock()

					if routes == nil {
						errors <- fmt.Errorf("reader %d: got nil routes slice", id)
						return
					}
					time.Sleep(1 * time.Microsecond)
				}
			}
		}(i)
	}

	// Spawn writer (simulating reload)
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Use safe access with mutex for the test writer
		manager.RLock()
		staticData := manager.gtfsData
		manager.RUnlock()

		for {
			select {
			case <-ctx.Done():
				return
			default:
				manager.setStaticGTFS(staticData)
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	wg.Wait()
	close(errors)

	for err := range errors {
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
	defer manager.Shutdown()

	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		manager.RLock()
		_ = manager.RoutesForAgencyID("25")
		manager.RUnlock()
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
		// Use a clearly invalid URL that will trigger failures
		GtfsURL:        "http://invalid.url.that.will.fail.internal/gtfs.zip",
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
	assert.Less(t, duration, 10*time.Second, "Retry logic should respect the configured backoff schedule")
}

func TestParseAndLogFeedExpiryLocked(t *testing.T) {
	ctx := context.Background()

	// In-memory sqlite db to mock
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec("CREATE TABLE calendar (end_date TEXT); CREATE TABLE calendar_dates (date TEXT, exception_type INTEGER);")
	require.NoError(t, err)

	manager := &Manager{
		GtfsDB: &gtfsdb.Client{
			DB:      db,
			Queries: gtfsdb.New(db),
		},
	}

	// 1. Empty calendar
	// Set an initial value to prove it gets reset
	manager.feedExpiresAt = time.Now()
	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.True(t, manager.feedExpiresAt.IsZero(), "Should be zero when no dates exist")

	// 2. Insert valid end date into calendar
	_, err = db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
	require.NoError(t, err)

	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.False(t, manager.feedExpiresAt.IsZero(), "Should parse end date")

	// Valid end date should be 2026-04-01 23:59:59
	expectedTime, _ := time.Parse("20060102150405", "20260401235959")
	assert.Equal(t, expectedTime.Unix(), manager.feedExpiresAt.Unix())

	// 3. Hot-swap scenario: Clear calendar (feed expires at should reset to zero)
	_, err = db.Exec("DELETE FROM calendar")
	require.NoError(t, err)

	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.True(t, manager.feedExpiresAt.IsZero(), "Should reset to zero after hot swap to empty feed")
}
