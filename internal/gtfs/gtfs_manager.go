package gtfs

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"

	"github.com/OneBusAway/go-gtfs"
	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
)

const NoRadiusLimit = -1

// Manager manages the GTFS data and provides methods to access it
type Manager struct {
	gtfsSource                     string
	gtfsData                       *gtfs.Static
	GtfsDB                         *gtfsdb.Client
	lastUpdated                    time.Time
	isLocalFile                    bool
	realTimeTrips                  []gtfs.Trip
	realTimeVehicles               []gtfs.Vehicle
	realTimeMutex                  sync.RWMutex
	realTimeAlerts                 []gtfs.Alert
	realTimeTripLookup             map[string]int
	realTimeVehicleLookupByTrip    map[string]int
	realTimeVehicleLookupByVehicle map[string]int
	staticMutex                    sync.RWMutex // Protects gtfsData and lastUpdated
	config                         Config
	shutdownChan                   chan struct{}
	wg                             sync.WaitGroup
	shutdownOnce                   sync.Once
}

// InitGTFSManager initializes the Manager with the GTFS data from the given source
// The source can be either a URL or a local file path
func InitGTFSManager(config Config) (*Manager, error) {
	isLocalFile := !strings.HasPrefix(config.GtfsURL, "http://") && !strings.HasPrefix(config.GtfsURL, "https://")

	staticData, err := loadGTFSData(config.GtfsURL, isLocalFile, config.StaticAuthHeaderKey, config.StaticAuthHeaderValue)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		gtfsSource:                     config.GtfsURL,
		isLocalFile:                    isLocalFile,
		config:                         config,
		shutdownChan:                   make(chan struct{}),
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
	}
	manager.setStaticGTFS(staticData)

	gtfsDB, err := buildGtfsDB(config, isLocalFile)
	if err != nil {
		return nil, fmt.Errorf("error building GTFS database: %w", err)
	}
	manager.GtfsDB = gtfsDB

	if !isLocalFile {
		manager.wg.Add(1)
		go manager.updateStaticGTFS()
	}

	if config.realTimeDataEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel() // Ensure the context is canceled when done
		manager.updateGTFSRealtime(ctx, config)
		manager.wg.Add(1)
		go manager.updateGTFSRealtimePeriodically(config)
	}

	return manager, nil
}

// Shutdown gracefully shuts down the manager and its background goroutines
func (manager *Manager) Shutdown() {
	manager.shutdownOnce.Do(func() {
		close(manager.shutdownChan)
		manager.wg.Wait()
		if manager.GtfsDB != nil {
			_ = manager.GtfsDB.Close()
		}
	})
}

func (manager *Manager) GetAgencies() []gtfs.Agency {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.gtfsData.Agencies
}

func (manager *Manager) GetTrips() []gtfs.ScheduledTrip {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.gtfsData.Trips
}

func (manager *Manager) GetStaticData() *gtfs.Static {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.gtfsData
}

func (manager *Manager) GetStops() []gtfs.Stop {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.gtfsData.Stops
}

func (manager *Manager) FindAgency(id string) *gtfs.Agency {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	for _, agency := range manager.gtfsData.Agencies {
		if agency.Id == id {
			return &agency
		}
	}
	return nil
}

// RoutesForAgencyID retrieves all routes associated with the specified agency ID from the GTFS data.
func (manager *Manager) RoutesForAgencyID(agencyID string) []*gtfs.Route {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	var agencyRoutes []*gtfs.Route

	for i := range manager.gtfsData.Routes {
		if manager.gtfsData.Routes[i].Agency.Id == agencyID {
			agencyRoutes = append(agencyRoutes, &manager.gtfsData.Routes[i])
		}
	}

	return agencyRoutes
}

type stopWithDistance struct {
	stop     gtfsdb.Stop
	distance float64
}

func (manager *Manager) GetStopsForLocation(
	ctx context.Context,
	lat, lon, radius, latSpan, lonSpan float64,
	query string,
	maxCount int,
	isForRoutes bool,
) []gtfsdb.Stop {
	const epsilon = 1e-6

	if radius == 0 {
		radius = 1000
	}
	if query != "" {
		radius *= 10
	}

	var candidates []stopWithDistance

	// Calculate bounding box for spatial query
	// Convert radius in meters to approximate degrees
	// 1 degree latitude ≈ 111km, 1 degree longitude varies by latitude
	latDegreeInMeters := 111000.0
	lonDegreeInMeters := 111000.0 * math.Cos(lat*math.Pi/180)

	var minLat, maxLat, minLon, maxLon float64

	if latSpan > 0 && lonSpan > 0 {
		// Use provided spans
		minLat = lat - latSpan - epsilon
		maxLat = lat + latSpan + epsilon
		minLon = lon - lonSpan - epsilon
		maxLon = lon + lonSpan + epsilon
	} else {
		// Calculate from radius
		latRadiusDegrees := radius / latDegreeInMeters
		lonRadiusDegrees := radius / lonDegreeInMeters

		minLat = lat - latRadiusDegrees
		maxLat = lat + latRadiusDegrees
		minLon = lon - lonRadiusDegrees
		maxLon = lon + lonRadiusDegrees
	}

	// Check if context is already cancelled
	if ctx.Err() != nil {
		return []gtfsdb.Stop{}
	}

	// Use spatial index query for initial filtering
	dbStops, err := manager.GtfsDB.Queries.GetStopsWithinBounds(ctx, gtfsdb.GetStopsWithinBoundsParams{
		Lat:   minLat,
		Lat_2: maxLat,
		Lon:   minLon,
		Lon_2: maxLon,
	})
	if err != nil {
		// TODO: add logging.
		return []gtfsdb.Stop{}
	}

	// Process results from database query
	for _, dbStop := range dbStops {
		if query != "" && !isForRoutes {
			if dbStop.Code.Valid && dbStop.Code.String == query {
				distance := utils.Haversine(lat, lon, dbStop.Lat, dbStop.Lon)
				if distance <= radius {
					return []gtfsdb.Stop{dbStop}
				}
			}
			continue
		}

		// Calculate precise distance for final filtering
		distance := utils.Haversine(lat, lon, dbStop.Lat, dbStop.Lon)
		if distance <= radius {
			candidates = append(candidates, stopWithDistance{dbStop, distance})
		} else if radius == NoRadiusLimit {
			// No radius specified; include all stops within the given latSpan and lonSpan
			candidates = append(candidates, stopWithDistance{dbStop, distance})
		}
	}

	// Sort by distance
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].distance < candidates[j].distance
	})

	// Limit to maxCount
	var stops []gtfsdb.Stop
	for i := 0; i < len(candidates) && i < maxCount; i++ {
		stops = append(stops, candidates[i].stop)
	}

	return stops
}

func (manager *Manager) VehiclesForAgencyID(agencyID string) []gtfs.Vehicle {
	routes := manager.RoutesForAgencyID(agencyID)
	routeIDs := make(map[string]bool) // all route IDs for the agency.
	for _, route := range routes {
		routeIDs[route.Id] = true
	}

	var vehicles []gtfs.Vehicle
	for _, v := range manager.GetRealTimeVehicles() {
		if v.Trip != nil {
			if routeIDs[v.Trip.ID.RouteID] {
				vehicles = append(vehicles, v)
			}
		}
	}

	return vehicles
}

// GetVehicleForTrip retrieves a vehicle for a specific trip ID or finds the first vehicle that is part of the block
// for that trip. Note we depend on getting the vehicle that may not match the trip ID exactly,
// but is part of the same block.
func (manager *Manager) GetVehicleForTrip(tripID string) *gtfs.Vehicle {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	requestedTrip, err := manager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil || !requestedTrip.BlockID.Valid {
		fmt.Fprintf(os.Stderr, "Could not get block ID for trip %s: %v\n", tripID, err)
		return nil
	}

	requestedBlockID := requestedTrip.BlockID.String

	blockTrips, err := manager.GtfsDB.Queries.GetTripsByBlockID(ctx, requestedTrip.BlockID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not get trips for block %s: %v\n", requestedBlockID, err)
		return nil
	}

	blockTripIDs := make(map[string]bool)
	for _, trip := range blockTrips {
		blockTripIDs[trip.ID] = true
	}

	for _, v := range manager.realTimeVehicles {
		if v.Trip != nil && v.Trip.ID.ID != "" && blockTripIDs[v.Trip.ID.ID] {
			return &v
		}
	}
	return nil
}

func (manager *Manager) GetVehicleByID(vehicleID string) (*gtfs.Vehicle, error) {

	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	if index, exists := manager.realTimeVehicleLookupByVehicle[vehicleID]; exists {
		return &manager.realTimeVehicles[index], nil
	}

	return nil, fmt.Errorf("vehicle with ID %s not found", vehicleID)
}

func (manager *Manager) GetTripUpdatesForTrip(tripID string) []gtfs.Trip {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	var updates []gtfs.Trip
	for _, v := range manager.realTimeTrips {
		if v.ID.ID == tripID {
			updates = append(updates, v)
		}
	}
	return updates
}

func (manager *Manager) GetVehicleLastUpdateTime(vehicle *gtfs.Vehicle) int64 {
	if vehicle == nil || vehicle.Timestamp == nil {
		return 0
	}
	return vehicle.Timestamp.UnixMilli()
}

func (manager *Manager) GetTripUpdateByID(tripID string) (*gtfs.Trip, error) {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	if index, exists := manager.realTimeTripLookup[tripID]; exists {
		return &manager.realTimeTrips[index], nil
	}
	return nil, fmt.Errorf("trip with ID %s not found", tripID)
}

func (manager *Manager) PrintStatistics() {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	fmt.Printf("Source: %s (Local File: %v)\n", manager.gtfsSource, manager.isLocalFile)
	fmt.Printf("Last Updated: %s\n", manager.lastUpdated)
	fmt.Println("Stops Count: ", len(manager.gtfsData.Stops))
	fmt.Println("Routes Count: ", len(manager.gtfsData.Routes))
	fmt.Println("Trips Count: ", len(manager.gtfsData.Trips))
	fmt.Println("Agencies Count: ", len(manager.gtfsData.Agencies))
}

func (manager *Manager) IsServiceActiveOnDate(ctx context.Context, serviceID string, date time.Time) (int64, error) {
	serviceDate := date.Format("20060102")

	exceptions, err := manager.GtfsDB.Queries.GetCalendarDateExceptionsForServiceID(ctx, serviceID)
	if err != nil {
		return 0, fmt.Errorf("error fetching exceptions: %w", err)
	}
	for _, e := range exceptions {
		if e.Date == serviceDate {
			if e.ExceptionType == 1 {
				return 1, nil
			}
			return 0, nil
		}
	}

	calendar, err := manager.GtfsDB.Queries.GetCalendarByServiceID(ctx, serviceID)
	if err != nil {
		return 0, fmt.Errorf("error fetching calendar for service %s: %w", serviceID, err)
	}

	if serviceDate < calendar.StartDate || serviceDate > calendar.EndDate {
		return 0, nil
	}

	switch date.Weekday() {
	case time.Sunday:
		return calendar.Sunday, nil
	case time.Monday:
		return calendar.Monday, nil
	case time.Tuesday:
		return calendar.Tuesday, nil
	case time.Wednesday:
		return calendar.Wednesday, nil
	case time.Thursday:
		return calendar.Thursday, nil
	case time.Friday:
		return calendar.Friday, nil
	case time.Saturday:
		return calendar.Saturday, nil
	default:
		return 0, nil
	}
}
