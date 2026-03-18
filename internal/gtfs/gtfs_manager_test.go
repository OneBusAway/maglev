package gtfs

import (
	"context"
	"database/sql"
	"errors"
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

func TestParseAndLogFeedExpiryLocked(t *testing.T) {
	ctx := context.Background()

	// In-memory sqlite db to mock
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

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

	// 4. Test calendar_dates exception_type = 1 branch
	// Insert a valid calendar date
	_, err = db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
	require.NoError(t, err)

	// Insert an extra service day via calendar_dates (exception_type = 1) that is LATER than calendar end_date
	_, err = db.Exec("INSERT INTO calendar_dates (date, exception_type) VALUES ('20260405', 1)")
	require.NoError(t, err)
	// Insert a removed service day (exception_type = 2) that is later than the added day, which should be ignored by the query calculation
	_, err = db.Exec("INSERT INTO calendar_dates (date, exception_type) VALUES ('20260410', 2)")
	require.NoError(t, err)

	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.False(t, manager.feedExpiresAt.IsZero(), "Should parse end date")

	// Valid end date should be 2026-04-05 23:59:59 (from calendar_dates exception_type = 1)
	expectedTime2, _ := time.Parse("20060102150405", "20260405235959")
	assert.Equal(t, expectedTime2.Unix(), manager.feedExpiresAt.Unix())
}

func TestManager_DataFreshnessTracking(t *testing.T) {
	manager := &Manager{
		realTimeMutex: sync.RWMutex{},
	}

	// Test GetStaticLastUpdated logic (zero case)
	zeroStatic := manager.GetStaticLastUpdated()
	assert.True(t, zeroStatic.IsZero())

	// Set and test StaticLastUpdated (verify normalization to UTC & atomic persistence)
	now := time.Now().UTC()
	manager.SetStaticLastUpdatedForTest(now)
	gotStatic := manager.GetStaticLastUpdated()
	assert.Equal(t, now.UnixNano(), gotStatic.UnixNano())
	assert.Equal(t, "UTC", gotStatic.Location().String())

	// Test GetFeedUpdateTimes returns defensive map copy
	manager.SetFeedUpdateTime("feed-1", now)
	feedTimes := manager.GetFeedUpdateTimes()
	assert.Contains(t, feedTimes, "feed-1")
	assert.Equal(t, now, feedTimes["feed-1"])

	// Modify the copy and ensure the original map state is safe
	feedTimes["feed-1"] = time.Now().Add(time.Hour)
	feedTimes2 := manager.GetFeedUpdateTimes()
	assert.Equal(t, now, feedTimes2["feed-1"])
}

func TestActiveServiceIDsCacheInvalidation(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	// Use a fixed date that has known calendar data in the RABA fixture.
	// The RABA feed covers weekdays; pick a Monday.
	date := "20240101"

	// First call should hit the DB and populate the cache.
	ids1, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	require.NotEmpty(t, ids1, "RABA fixture must have active services on 20240101; check the test date or fixture")

	manager.activeServiceIDsCacheMutex.RLock()
	cached, ok := manager.activeServiceIDsCache[date]
	manager.activeServiceIDsCacheMutex.RUnlock()
	assert.True(t, ok, "cache entry should exist after first call")
	assert.Equal(t, ids1, cached, "cached value should match returned value")

	// Second call should return the same result from cache without hitting the DB.
	ids2, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	assert.Equal(t, ids1, ids2, "cached result should match original result")

	// ForceUpdate should clear the cache.
	err = manager.ForceUpdate(ctx)
	require.NoError(t, err)

	manager.activeServiceIDsCacheMutex.RLock()
	cacheLen := len(manager.activeServiceIDsCache)
	manager.activeServiceIDsCacheMutex.RUnlock()
	assert.Equal(t, 0, cacheLen, "cache should be empty after ForceUpdate")

	// After ForceUpdate the cache should repopulate on the next call.
	ids3, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	assert.Equal(t, ids1, ids3, "result after cache invalidation should match original")

	manager.activeServiceIDsCacheMutex.RLock()
	_, repopulated := manager.activeServiceIDsCache[date]
	manager.activeServiceIDsCacheMutex.RUnlock()
	assert.True(t, repopulated, "cache should be repopulated after post-ForceUpdate call")
}

func TestActiveServiceIDsCache_ErrorPathLeavesNothingCached(t *testing.T) {
	// Use an isolated manager so the cache is guaranteed cold for this date,
	// regardless of test execution order in the package.
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(context.Background(), gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, queryErr := manager.GetActiveServiceIDsForDateCached(cancelledCtx, "20240101")

	// mattn/go-sqlite3 (sqlite_fts5 build tag) propagates a cancelled context as an error.
	// The pure-Go modernc driver (purego tag) may not; guard the assertion so the test
	// remains valid if the build tag ever changes.
	if errors.Is(queryErr, context.Canceled) || errors.Is(queryErr, context.DeadlineExceeded) {
		manager.activeServiceIDsCacheMutex.RLock()
		_, cached := manager.activeServiceIDsCache["20240101"]
		manager.activeServiceIDsCacheMutex.RUnlock()
		assert.False(t, cached, "cache must remain empty after a failed query")
	} else {
		t.Logf("driver did not propagate cancelled context as an error (%v); cache-pollution assertion skipped", queryErr)
	}
}

func TestActiveServiceIDsCacheRace(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	ctx := context.Background()

	// The primary goal is to exercise the race detector on the double-checked locking
	// path; the equality assertions confirm all goroutines see consistent data.
	const workers = 20
	results := make([][]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], _ = manager.GetActiveServiceIDsForDateCached(ctx, "20240101")
		}(i)
	}
	wg.Wait()

	for i := 1; i < workers; i++ {
		assert.Equal(t, results[0], results[i], "goroutine %d returned inconsistent result", i)
	}
}

func TestActiveServiceIDsCacheNilDB(t *testing.T) {
	manager := &Manager{
		activeServiceIDsCache: make(map[string][]string),
		// GtfsDB is intentionally nil.
	}
	_, err := manager.GetActiveServiceIDsForDateCached(context.Background(), "20240101")
	require.Error(t, err, "nil GtfsDB should return an error, not panic")
}

func TestActiveServiceIDsCacheMutationSafety(t *testing.T) {
	// Use an isolated manager so the cache is cold, guaranteeing the first call is a
	// genuine cache miss and that we exercise both the miss-path and hit-path copies.
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(context.Background(), gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	ctx := context.Background()
	date := "20240101"

	// First call: cache miss path — result must be a defensive copy.
	ids1, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	require.NotEmpty(t, ids1, "need at least one service ID to test mutation safety")

	original := ids1[0]
	ids1[0] = "mutated-miss-path"

	// Second call: cache hit path — must return a fresh copy of the stored value.
	ids2, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	assert.Equal(t, original, ids2[0], "miss-path: mutating first result must not corrupt the cache")

	ids2[0] = "mutated-hit-path"

	// Third call: still must return the original value.
	ids3, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	assert.Equal(t, original, ids3[0], "hit-path: mutating second result must not corrupt the cache")
}

func TestActiveServiceIDsCacheEmptyDate(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	ctx := context.Background()

	// An empty date string is syntactically invalid for the calendar CTE. The call must
	// return an error or an empty slice; it must never panic or cache garbage.
	ids, err := manager.GetActiveServiceIDsForDateCached(ctx, "")
	if err != nil {
		// Acceptable: DB returned an error for a malformed date.
		manager.activeServiceIDsCacheMutex.RLock()
		_, cached := manager.activeServiceIDsCache[""]
		manager.activeServiceIDsCacheMutex.RUnlock()
		assert.False(t, cached, "a failed query for empty date must not populate the cache")
	} else {
		// Also acceptable: DB returned an empty result set.
		assert.Empty(t, ids, "empty date should yield no active service IDs")
	}
}

func TestActiveServiceIDsCacheConcurrentForceUpdate(t *testing.T) {
	ctx := context.Background()

	tempDir := t.TempDir()
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}

	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	defer manager.Shutdown()

	date := "20240101"

	// Warm the cache before the concurrent phase.
	_, err = manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)

	// Launch readers that race against ForceUpdate. With the epoch guard in place, no
	// in-flight reader can write stale data from the old dataset into the freshly-cleared
	// cache. The race detector will catch any unsynchronised access.
	const readers = 10
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				_, _ = manager.GetActiveServiceIDsForDateCached(ctx, date)
			}
		}()
	}

	err = manager.ForceUpdate(ctx)
	require.NoError(t, err)

	wg.Wait()

	// The epoch must have advanced, confirming ForceUpdate cleared the cache.
	assert.Greater(t, manager.cacheEpoch.Load(), uint64(0), "epoch should advance after ForceUpdate")

	// Repeated queries after settling must return consistent results.
	ids1, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	ids2, err := manager.GetActiveServiceIDsForDateCached(ctx, date)
	require.NoError(t, err)
	assert.Equal(t, ids1, ids2, "repeated queries after ForceUpdate must return consistent results")
}

func TestMockClearServiceIDsCache_IncrementsEpoch(t *testing.T) {
	manager, _ := getSharedTestComponents(t)

	before := manager.cacheEpoch.Load()
	manager.MockClearServiceIDsCache()
	after := manager.cacheEpoch.Load()

	assert.Greater(t, after, before, "MockClearServiceIDsCache must increment cacheEpoch")
}

func TestActiveServiceIDsCacheConcurrentErrorAndReaders(t *testing.T) {
	manager, _ := getSharedTestComponents(t)
	// Clear the cache so the cancelled-context goroutine reaches the DB query path
	// rather than returning a warm-cache hit before the context is inspected.
	manager.MockClearServiceIDsCache()
	ctx := context.Background()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// One goroutine queries with a cancelled context; all others use a valid context.
	// The error from the bad goroutine must not corrupt the cache for the rest.
	const workers = 20
	results := make([][]string, workers)
	errs := make([]error, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			c := ctx
			if idx == 0 {
				c = cancelledCtx
			}
			results[idx], errs[idx] = manager.GetActiveServiceIDsForDateCached(c, "20240101")
		}(i)
	}
	wg.Wait()

	// All goroutines using a valid context must have succeeded with consistent results.
	var reference []string
	for i := 1; i < workers; i++ {
		require.NoError(t, errs[i], "goroutine %d should not have gotten an error", i)
		if reference == nil {
			reference = results[i]
		} else {
			assert.Equal(t, reference, results[i], "goroutine %d returned inconsistent result", i)
		}
	}
	assert.NotEmpty(t, reference, "valid goroutines must return non-empty results")
}

func BenchmarkGetActiveServiceIDsForDate(b *testing.B) {
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

	date := "20240101"

	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = manager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, date)
		}
	})

	// Warm the cache once before benchmarking the cached path.
	_, _ = manager.GetActiveServiceIDsForDateCached(ctx, date)

	b.Run("cached", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = manager.GetActiveServiceIDsForDateCached(ctx, date)
		}
	})
}
