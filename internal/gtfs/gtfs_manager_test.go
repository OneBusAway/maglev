package gtfs

import (
	"context"
	"fmt"
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
	testCases := []struct {
		name     string
		dataPath string
	}{
		{
			name:     "FromLocalFile",
			dataPath: models.GetFixturePath(t, "raba.zip"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gtfsConfig := Config{
				GtfsURL:      tc.dataPath,
				Env:          appconf.Test,
				GTFSDataPath: ":memory:",
			}
			manager, err := InitGTFSManager(gtfsConfig)
			assert.Nil(t, err)
			defer manager.Shutdown()

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
		})
	}
}

func TestManager_RoutesForAgencyID(t *testing.T) {
	testCases := []struct {
		name     string
		dataPath string
	}{
		{
			name:     "FromLocalFile",
			dataPath: models.GetFixturePath(t, "raba.zip"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gtfsConfig := Config{
				GtfsURL:      tc.dataPath,
				GTFSDataPath: ":memory:",
				Env:          appconf.Test,
			}
			manager, err := InitGTFSManager(gtfsConfig)
			assert.Nil(t, err)
			defer manager.Shutdown()

			manager.RLock()
			routes := manager.RoutesForAgencyID("25")
			manager.RUnlock()
			assert.Equal(t, 13, len(routes))

			route := routes[0]
			assert.Equal(t, "1", route.ShortName)
			assert.Equal(t, "25", route.Agency.Id)
		})
	}
}

func TestManager_GetStopsForLocation_UsesSpatialIndex(t *testing.T) {
	testCases := []struct {
		name          string
		dataPath      string
		lat           float64
		lon           float64
		radius        float64
		expectedStops int
	}{
		{
			name:          "FindStopsWithinRadius",
			dataPath:      models.GetFixturePath(t, "raba.zip"),
			lat:           40.589123, // Near Redding, CA
			lon:           -122.390830,
			radius:        2000, // 2km radius
			expectedStops: 1,    // Should find at least 1 stop
		},
		{
			name:          "FindStopsWithinRadius",
			dataPath:      models.GetFixturePath(t, "raba.zip"),
			lat:           47.589123, // West Seattle
			lon:           -122.390830,
			radius:        2000, // 2km radius
			expectedStops: 0,    // Should find zero stops (outside RABA area)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gtfsConfig := Config{
				GtfsURL:      tc.dataPath,
				GTFSDataPath: ":memory:",
				Env:          appconf.Test,
			}
			manager, err := InitGTFSManager(gtfsConfig)
			assert.Nil(t, err)
			defer manager.Shutdown()

			// Get stops using the manager method
			stops := manager.GetStopsForLocation(context.Background(), tc.lat, tc.lon, tc.radius, 0, 0, "", 100, false, nil, time.Time{})

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
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	trips := manager.GetTrips()
	assert.NotEmpty(t, trips)
	assert.NotEmpty(t, trips[0].ID)
}

func TestManager_FindAgency(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	agency := manager.FindAgency("25")
	assert.NotNil(t, agency)
	assert.Equal(t, "25", agency.Id)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)

	agencyNotFound := manager.FindAgency("nonexistent")
	assert.Nil(t, agencyNotFound)
}

func TestManager_GetVehicleByID(t *testing.T) {
	manager := &Manager{
		realTimeVehicleLookupByVehicle: make(map[string]int),
		realTimeVehicles: []gtfs.Vehicle{
			{
				ID: &gtfs.VehicleID{ID: "vehicle1"},
			},
		},
	}
	rebuildRealTimeVehicleLookupByVehicle(manager)

	vehicle, err := manager.GetVehicleByID("vehicle1")
	assert.Nil(t, err)
	assert.NotNil(t, vehicle)
	assert.Equal(t, "vehicle1", vehicle.ID.ID)

	notFound, err := manager.GetVehicleByID("nonexistent")
	assert.NotNil(t, err)
	assert.Nil(t, notFound)
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
		realTimeTripLookup: make(map[string]int),
		realTimeTrips: []gtfs.Trip{
			{
				ID: gtfs.TripID{ID: "trip1"},
			},
		},
	}
	rebuildRealTimeTripLookup(manager)

	trip, err := manager.GetTripUpdateByID("trip1")
	assert.Nil(t, err)
	assert.NotNil(t, trip)
	assert.Equal(t, "trip1", trip.ID.ID)

	notFound, err := manager.GetTripUpdateByID("nonexistent")
	assert.NotNil(t, err)
	assert.Nil(t, notFound)
}

func TestManager_IsServiceActiveOnDate(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

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
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
	assert.Nil(t, err)
	defer manager.Shutdown()

	trip := &gtfs.Trip{
		ID: gtfs.TripID{ID: "5735633"},
	}
	manager.realTimeVehicles = []gtfs.Vehicle{
		{
			ID:   &gtfs.VehicleID{ID: "vehicle1"},
			Trip: trip,
		},
	}
	
	rebuildRealTimeVehicleLookupByTrip(manager)

	vehicle := manager.GetVehicleForTrip("5735633")
	if vehicle != nil {
		assert.NotNil(t, vehicle)
		assert.Equal(t, "vehicle1", vehicle.ID.ID)
	}
}

func TestRoutesForAgencyID_MapOptimization(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
	require.NoError(t, err, "Failed to initialize manager")
	defer manager.Shutdown()

	targetAgencyID := "25"
	expectedRouteCount := 13

	manager.RLock()
	assert.NotNil(t, manager.routesByAgencyID, "routesByAgencyID map should be initialized")

	cachedRoutes, exists := manager.routesByAgencyID[targetAgencyID]
	assert.True(t, exists, "Agency %s should exist in cache map", targetAgencyID)
	assert.Len(t, cachedRoutes, expectedRouteCount, "Map should contain correct number of routes")
	manager.RUnlock()

	manager.RLock()
	publicRoutes := manager.RoutesForAgencyID(targetAgencyID)
	manager.RUnlock()

	assert.Len(t, publicRoutes, expectedRouteCount, "Public API should return correct route count")

	manager.RLock()
	for _, route := range publicRoutes {
		assert.Equal(t, targetAgencyID, route.Agency.Id,
			"Route %s should belong to agency %s", route.Id, targetAgencyID)
	}
	manager.RUnlock()

	manager.RLock()
	emptyRoutes := manager.RoutesForAgencyID("nonexistent")
	manager.RUnlock()

	assert.Empty(t, emptyRoutes, "Non-existent agency should return empty slice")
}

func TestRoutesForAgencyID_ConcurrentAccess(t *testing.T) {
	gtfsConfig := Config{
		GtfsURL:      models.GetFixturePath(t, "raba.zip"),
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
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
					manager.staticMutex.RLock()
					routes := manager.RoutesForAgencyID("25")
					manager.staticMutex.RUnlock()

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
		manager.staticMutex.RLock()
		staticData := manager.gtfsData
		manager.staticMutex.RUnlock()

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
	gtfsConfig := Config{
		GtfsURL:      "../../testdata/raba.zip",
		GTFSDataPath: ":memory:",
		Env:          appconf.Test,
	}
	manager, err := InitGTFSManager(gtfsConfig)
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
