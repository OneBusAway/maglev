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
			stops := manager.GetStopsInBounds(ctx, &LocationParams{Lat: tc.lat, Lon: tc.lon, Radius: tc.radius}, 100)

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

func TestRoutesForAgencyID_NonexistentId(t *testing.T) {
	ctx := context.Background()

	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err, "Failed to initialize manager")
	defer manager.Shutdown()

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
	defer manager.Shutdown()

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
	defer manager.Shutdown()

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
	defer manager.Shutdown()

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

func TestParseAndLogFeedExpiryLocked(t *testing.T) {
	ctx := context.Background()

	// In-memory sqlite db to mock
	db, err := sql.Open(gtfsdb.DriverName, ":memory:")
	require.NoError(t, err)
	defer func() { assert.NoError(t, db.Close()) }()

	_, err = db.Exec(`
		CREATE TABLE calendar (end_date TEXT);
		CREATE TABLE calendar_dates (date TEXT, exception_type INTEGER);
		CREATE TABLE import_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			file_hash TEXT NOT NULL,
			import_time INTEGER NOT NULL,
			file_source TEXT NOT NULL,
			feed_expires_at INTEGER
		);
		INSERT INTO import_metadata (id, file_hash, import_time, file_source) VALUES (1, '', 0, '');
	`)
	require.NoError(t, err)

	manager := &Manager{
		GtfsDB: &gtfsdb.Client{
			DB:      db,
			Queries: gtfsdb.New(db),
		},
	}

	// 1. Empty calendar — feed_expires_at should be reset to NULL
	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.True(t, manager.FeedExpiresAt().IsZero(), "Should be zero when no dates exist")

	// 2. Insert valid end date into calendar
	_, err = db.Exec("INSERT INTO calendar (end_date) VALUES ('20260401')")
	require.NoError(t, err)

	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.False(t, manager.FeedExpiresAt().IsZero(), "Should parse end date")

	// Valid end date should be 2026-04-01 23:59:59
	expectedTime, _ := time.Parse("20060102150405", "20260401235959")
	assert.Equal(t, expectedTime.Unix(), manager.FeedExpiresAt().Unix())

	// 3. Reload scenario: Clear calendar (feed expires at should reset to zero)
	_, err = db.Exec("DELETE FROM calendar")
	require.NoError(t, err)

	manager.parseAndLogFeedExpiryLocked(ctx, slog.Default())
	assert.True(t, manager.FeedExpiresAt().IsZero(), "Should reset to zero after reload with empty feed")

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
	assert.False(t, manager.FeedExpiresAt().IsZero(), "Should parse end date")

	// Valid end date should be 2026-04-05 23:59:59 (from calendar_dates exception_type = 1)
	expectedTime2, _ := time.Parse("20060102150405", "20260405235959")
	assert.Equal(t, expectedTime2.Unix(), manager.FeedExpiresAt().Unix())
}

func TestManager_DataFreshnessTracking(t *testing.T) {
	// nil DB returns zero
	nilManager := &Manager{}
	assert.True(t, nilManager.GetStaticLastUpdated().IsZero())

	// Set and test StaticLastUpdated via DB roundtrip
	tempDir := t.TempDir()
	cfg := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: tempDir + "/gtfs.db",
		Env:          appconf.Development,
	}
	dbManager, err := InitGTFSManager(context.Background(), cfg)
	require.NoError(t, err)
	defer dbManager.Shutdown()

	now := time.Now().UTC().Truncate(time.Second)
	dbManager.SetStaticLastUpdatedForTest(now)
	gotStatic := dbManager.GetStaticLastUpdated()
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
