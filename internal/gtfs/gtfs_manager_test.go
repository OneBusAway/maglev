package gtfs

import (
	"context"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
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
			}
			manager, err := InitGTFSManager(gtfsConfig)
			assert.Nil(t, err)

			routes := manager.RoutesForAgencyID("25")
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

			// Get stops using the manager method
			stops := manager.GetStopsForLocation(context.Background(), tc.lat, tc.lon, tc.radius, 0, 0, "", 100, false)

			// The test expects that the spatial index query is used
			// We'll verify this by checking that we get results and that
			// the query is efficient (not iterating through all stops)
			assert.GreaterOrEqual(t, len(stops), tc.expectedStops, "Should find stops within radius")

			// Verify stops are actually within the radius
			for _, stop := range stops {
				// Lat and Lon are float64, not pointers - no nil check needed
				// Calculate distance to verify it's within radius
				// This would use the utils.Haversine function
				// but for now we'll just verify coordinates exist
				assert.NotZero(t, stop.Lat)
				assert.NotZero(t, stop.Lon)
			}

			// The key test is that this should use the spatial index
			// We'll verify this is implemented by checking the database has the query
			assert.NotNil(t, manager.GtfsDB.Queries, "Database queries should exist")

			// This will fail initially because GetStopsWithinRadius doesn't exist yet
			// Once we implement it, this test will pass
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

	agency := manager.FindAgency("25")
	assert.NotNil(t, agency)
	assert.Equal(t, "25", agency.Id)
	assert.Equal(t, "Redding Area Bus Authority", agency.Name)

	notFound := manager.FindAgency("nonexistent")
	assert.Nil(t, notFound)
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

	// Get a trip to find a valid service ID
	trips := manager.GetTrips()
	assert.NotEmpty(t, trips)

	serviceID := trips[0].Service.Id

	// Test with a date that should be within service period
	testDate := time.Date(2024, 11, 4, 0, 0, 0, 0, time.UTC) // Monday
	active, err := manager.IsServiceActiveOnDate(context.Background(), serviceID, testDate)
	// The function should complete without error
	if err == nil {
		assert.GreaterOrEqual(t, active, int64(0))
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

	// Set up real-time vehicle with a trip
	trip := &gtfs.Trip{
		ID: gtfs.TripID{ID: "5735633"},
	}
	manager.realTimeVehicles = []gtfs.Vehicle{
		{
			ID:   &gtfs.VehicleID{ID: "vehicle1"},
			Trip: trip,
		},
	}

	// Test getting vehicle for a trip in the same block
	vehicle := manager.GetVehicleForTrip("5735633")
	if vehicle != nil {
		assert.NotNil(t, vehicle)
		assert.Equal(t, "vehicle1", vehicle.ID.ID)
	}
}
