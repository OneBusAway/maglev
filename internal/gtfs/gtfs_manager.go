package gtfs

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"

	"github.com/OneBusAway/go-gtfs"
	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
	"github.com/tidwall/rtree"
	"maglev.onebusaway.org/internal/logging"
)

const NoRadiusLimit = -1

// Manager manages the GTFS data and provides methods to access it
type Manager struct {
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
	staticUpdateMutex              sync.Mutex   // Protects against concurrent ForceUpdate calls
	staticMutex                    sync.RWMutex // Protects gtfsData and lastUpdated
	config                         Config
	shutdownChan                   chan struct{}
	wg                             sync.WaitGroup
	shutdownOnce                   sync.Once
	stopSpatialIndex               *rtree.RTree
	blockLayoverIndices            map[string][]*BlockLayoverIndex
	isHealthy                      bool
}

// InitGTFSManager initializes the Manager with the GTFS data from the given source
// The source can be either a URL or a local file path
func InitGTFSManager(config Config) (*Manager, error) {
	isLocalFile := !strings.HasPrefix(config.GtfsURL, "http://") && !strings.HasPrefix(config.GtfsURL, "https://")

	staticData, err := loadGTFSData(config.GtfsURL, isLocalFile, config)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		isLocalFile:                    isLocalFile,
		config:                         config,
		shutdownChan:                   make(chan struct{}),
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
	}
	manager.setStaticGTFS(staticData)

	gtfsDB, err := buildGtfsDB(config, isLocalFile, "")
	if err != nil {
		return nil, fmt.Errorf("error building GTFS database: %w", err)
	}
	manager.GtfsDB = gtfsDB

	// Build spatial index for fast stop location queries
	ctx := context.Background()
	spatialIndex, err := buildStopSpatialIndex(ctx, gtfsDB.Queries)
	if err != nil {
		return nil, fmt.Errorf("error building spatial index: %w", err)
	}
	manager.stopSpatialIndex = spatialIndex

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

// SetGtfsURL updates the GTFS URL in the configuration.
// It uses a mutex to ensure thread safety.
func (manager *Manager) SetGtfsURL(url string) {
	manager.staticUpdateMutex.Lock()
	defer manager.staticUpdateMutex.Unlock()
	manager.config.GtfsURL = url
	manager.isLocalFile = !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://")
}

// Shutdown gracefully shuts down the manager and its background goroutines
func (manager *Manager) Shutdown() {
	manager.shutdownOnce.Do(func() {
		close(manager.shutdownChan)
		manager.wg.Wait()
		if manager.GtfsDB != nil {
			if err := manager.GtfsDB.Close(); err != nil {
				logger := slog.Default().With(slog.String("component", "gtfs_manager"))
				logging.LogError(logger, "failed to close GTFS database", err)
			}
		}
	})
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) RLock() {
	manager.staticMutex.RLock()
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) RUnlock() {
	manager.staticMutex.RUnlock()
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetAgencies() []gtfs.Agency {
	return manager.gtfsData.Agencies
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetTrips() []gtfs.ScheduledTrip {
	return manager.gtfsData.Trips
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetStaticData() *gtfs.Static {
	return manager.gtfsData
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetStops() []gtfs.Stop {
	return manager.gtfsData.Stops
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetBlockLayoverIndicesForRoute(routeID string) []*BlockLayoverIndex {
	return getBlockLayoverIndicesForRoute(manager.blockLayoverIndices, routeID)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) FindAgency(id string) *gtfs.Agency {
	for _, agency := range manager.gtfsData.Agencies {
		if agency.Id == id {
			agencyCopy := agency
			return &agencyCopy
		}
	}
	return nil
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetRoutes() []gtfs.Route {
	return manager.gtfsData.Routes
}

// RoutesForAgencyID retrieves all routes associated with the specified agency ID from the GTFS data.
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) RoutesForAgencyID(agencyID string) []*gtfs.Route {
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

// GetStopsForLocation retrieves stops near a given location using the spatial index.
// It supports filtering by route types and querying for specific stop codes.
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetStopsForLocation(
	ctx context.Context,
	lat, lon, radius, latSpan, lonSpan float64,
	query string,
	maxCount int,
	isForRoutes bool,
	routeTypes []int,
	queryTime time.Time,
) []gtfsdb.Stop {
	var candidates []stopWithDistance

	var bounds utils.CoordinateBounds

	if latSpan > 0 && lonSpan > 0 {
		bounds = utils.CalculateBoundsFromSpan(lat, lon, latSpan/2, lonSpan/2)
	} else {
		if radius == 0 {
			if query != "" {
				radius = 10000
			} else {
				radius = 500
			}
		}
		bounds = utils.CalculateBounds(lat, lon, radius)
	}

	// Check if context is already cancelled
	if ctx.Err() != nil {
		return []gtfsdb.Stop{}
	}

	dbStops := queryStopsInBounds(manager.stopSpatialIndex, bounds)

	for _, dbStop := range dbStops {
		if query != "" && !isForRoutes {
			if dbStop.Code.Valid && dbStop.Code.String == query {
				return []gtfsdb.Stop{dbStop}
			}
			continue
		}
		distance := utils.Distance(lat, lon, dbStop.Lat, dbStop.Lon)
		candidates = append(candidates, stopWithDistance{dbStop, distance})
	}

	// If the stop does not have any routes actively serving it, don't include it in the results
	// This filtering is only applied if we are not searching for a specific stop code
	if query == "" || isForRoutes {
		if len(routeTypes) > 0 {
			stopIDs := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				stopIDs = append(stopIDs, candidate.stop.ID)
			}

			routesForStops, err := manager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
			if err == nil {
				stopRouteTypes := make(map[string][]int)
				for _, r := range routesForStops {
					stopRouteTypes[r.StopID] = append(stopRouteTypes[r.StopID], int(r.Type))
				}

				filteredCandidates := make([]stopWithDistance, 0, len(candidates))
				for _, candidate := range candidates {
					types := stopRouteTypes[candidate.stop.ID]
					hasMatchingType := false
					for _, rt := range types {
						for _, targetType := range routeTypes {
							if rt == targetType {
								hasMatchingType = true
								break
							}
						}
						if hasMatchingType {
							break
						}
					}
					if hasMatchingType {
						filteredCandidates = append(filteredCandidates, candidate)
					}
				}
				candidates = filteredCandidates
			}
		}

		// Filter by service date - only include stops with active service on current date
		if len(candidates) > 0 && !isForRoutes {
			var currentDate string
			if !queryTime.IsZero() {
				currentDate = queryTime.Format("20060102")
			} else {
				currentDate = time.Now().Format("20060102")
			}

			// Get active service IDs for current date
			activeServiceIDs, err := manager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, currentDate)

			if err == nil && len(activeServiceIDs) > 0 {
				stopIDs := make([]string, 0, len(candidates))
				for _, candidate := range candidates {
					stopIDs = append(stopIDs, candidate.stop.ID)
				}

				stopsWithActiveService, err := manager.GtfsDB.Queries.GetStopsWithActiveServiceOnDate(ctx, gtfsdb.GetStopsWithActiveServiceOnDateParams{
					StopIds:    stopIDs,
					ServiceIds: activeServiceIDs,
				})

				if err == nil {
					stopsWithService := make(map[string]bool)
					for _, stopID := range stopsWithActiveService {
						stopsWithService[stopID] = true
					}

					filteredCandidates := make([]stopWithDistance, 0, len(candidates))
					for _, candidate := range candidates {
						if stopsWithService[candidate.stop.ID] {
							filteredCandidates = append(filteredCandidates, candidate)
						}
					}
					candidates = filteredCandidates
				}
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].distance < candidates[j].distance
	})

	// When isForRoutes is true, return all matching stops without applying maxCount limit.
	// This prevents artificially limiting route results when the stop count would truncate
	// routes that exist at stops beyond the maxCount threshold.
	var stops []gtfsdb.Stop
	for i := 0; i < len(candidates) && (i < maxCount || isForRoutes); i++ {
		stops = append(stops, candidates[i].stop)
	}

	return stops
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetVehicleForTrip(tripID string) *gtfs.Vehicle {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	logger := slog.Default().With(slog.String("component", "gtfs_manager"))

	requestedTrip, err := manager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		logging.LogError(logger, "could not get trip", err,
			slog.String("trip_id", tripID))
		return nil
	}

	if !requestedTrip.BlockID.Valid {
		logger.Debug("trip has no block ID, cannot find vehicle by block",
			slog.String("trip_id", tripID))
		return nil
	}

	requestedBlockID := requestedTrip.BlockID.String

	blockTrips, err := manager.GtfsDB.Queries.GetTripsByBlockID(ctx, requestedTrip.BlockID)
	if err != nil {
		logging.LogError(logger, "could not get trips for block", err,
			slog.String("block_id", requestedBlockID))
		return nil
	}

	blockTripIDs := make(map[string]bool)
	for _, trip := range blockTrips {
		blockTripIDs[trip.ID] = true
	}

	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	// Iterate over all vehicles to find any vehicle serving a trip in this block.
	// We use iteration rather than realTimeVehicleLookupByTrip because we need to
	// match against any trip in the block, not a specific trip ID.
	for _, v := range manager.realTimeVehicles {
		if v.Trip != nil && v.Trip.ID.ID != "" && blockTripIDs[v.Trip.ID.ID] {
			vehicle := v
			return &vehicle
		}
	}
	return nil
}

func (manager *Manager) GetVehicleByID(vehicleID string) (*gtfs.Vehicle, error) {

	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	if index, exists := manager.realTimeVehicleLookupByVehicle[vehicleID]; exists {
		vehicle := manager.realTimeVehicles[index]
		return &vehicle, nil
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
		trip := manager.realTimeTrips[index]
		return &trip, nil
	}
	return nil, fmt.Errorf("trip with ID %s not found", tripID)
}

func (manager *Manager) GetAllTripUpdates() []gtfs.Trip {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	return manager.realTimeTrips
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) PrintStatistics() {
	fmt.Printf("Source: %s (Local File: %v)\n", manager.config.GtfsURL, manager.isLocalFile)
	fmt.Printf("Last Updated: %s\n", manager.lastUpdated)
	fmt.Println("Stops Count: ", len(manager.gtfsData.Stops))
	fmt.Println("Routes Count: ", len(manager.gtfsData.Routes))
	fmt.Println("Trips Count: ", len(manager.gtfsData.Trips))
	fmt.Println("Agencies Count: ", len(manager.gtfsData.Agencies))
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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

// IsHealthy returns true if the GTFS data is loaded and valid.
func (manager *Manager) IsHealthy() bool {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.isHealthy
}

// MarkHealthy sets the manager status to healthy.
func (manager *Manager) MarkHealthy() {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()
	manager.isHealthy = true
}

// MarkUnhealthy sets the manager status to unhealthy.
func (manager *Manager) MarkUnhealthy() {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()
	manager.isHealthy = false
}
