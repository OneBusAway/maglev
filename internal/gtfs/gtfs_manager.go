package gtfs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/metrics"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"

	"github.com/OneBusAway/go-gtfs"
	"github.com/tidwall/rtree"
	"maglev.onebusaway.org/internal/logging"
)

const NoRadiusLimit = -1

// RegionBounds represents the geographic boundaries of the GTFS region
type RegionBounds struct {
	Lat     float64
	Lon     float64
	LatSpan float64
	LonSpan float64
}

// Manager manages the GTFS data and provides methods to access it.
//
// Lock ordering policy (to prevent deadlocks):
//
//	staticMutex → realTimeMutex
//
// When both locks are needed, staticMutex MUST be acquired first.
// Never acquire staticMutex while holding realTimeMutex.
type Manager struct {
	gtfsData                       *gtfs.Static
	GtfsDB                         *gtfsdb.Client
	routesByAgencyID               map[string][]*gtfs.Route
	lastUpdated                    time.Time
	isLocalFile                    bool
	realTimeTrips                  []gtfs.Trip
	realTimeVehicles               []gtfs.Vehicle
	realTimeMutex                  sync.RWMutex
	realTimeAlerts                 []gtfs.Alert
	realTimeTripLookup             map[string]int
	realTimeVehicleLookupByTrip    map[string]int
	realTimeVehicleLookupByVehicle map[string]int
	agenciesMap                    map[string]*gtfs.Agency
	routesMap                      map[string]*gtfs.Route
	frequencyTripIDs               map[string]struct{}
	staticUpdateMutex              sync.Mutex   // Protects against concurrent ForceUpdate calls
	staticMutex                    sync.RWMutex // Protects gtfsData and lastUpdated
	config                         Config
	shutdownChan                   chan struct{}
	wg                             sync.WaitGroup
	shutdownOnce                   sync.Once
	stopSpatialIndex               *rtree.RTree
	blockLayoverIndices            map[string][]*BlockLayoverIndex
	regionBounds                   *RegionBounds
	isHealthy                      bool
	systemETag                     string      // systemETag stores the SHA-256 hash of the currently loaded GTFS static dataset.
	isReady                        atomic.Bool // Tracks whether initial data loading is complete

	feedExpiresAt time.Time // Holds the max valid service date for the static feed

	feedTrips    map[string][]gtfs.Trip
	feedVehicles map[string][]gtfs.Vehicle
	feedAlerts   map[string][]gtfs.Alert
	// Per-feed agency filter: feedID -> set of allowed agency IDs.
	// Populated once during InitGTFSManager before goroutines start; read-only thereafter.
	// No lock is required for reads.
	feedAgencyFilter map[string]map[string]bool
	// Per-feed, per-vehicle last-seen timestamps for stale vehicle expiry
	feedVehicleLastSeen map[string]map[string]time.Time // feedID -> vehicleID -> lastSeen

	// Per-feed last successfully applied vehicle feed timestamp
	feedVehicleTimestamp map[string]uint64 // feedID -> timestamp

	// Exported metrics client dependency
	Metrics *metrics.Metrics
}

// clearFeedData removes stale data for a specific feed when the staleness threshold is crossed
func (manager *Manager) clearFeedData(feedID string) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	manager.feedTrips[feedID] = nil
	manager.feedVehicles[feedID] = nil
	manager.feedAlerts[feedID] = nil

	delete(manager.feedVehicleTimestamp, feedID)
	delete(manager.feedVehicleLastSeen, feedID)

	manager.rebuildMergedRealtimeLocked()
}

// IsReady returns true if the GTFS data is fully initialized and indexed.
func (manager *Manager) IsReady() bool {
	return manager.isReady.Load()
}

// MarkReady sets the manager status to ready.
func (manager *Manager) MarkReady() {
	manager.isReady.Store(true)
}

// InitGTFSManager initializes the Manager with the GTFS data from the given source
// The source can be either a URL or a local file path
func InitGTFSManager(ctx context.Context, config Config) (*Manager, error) {
	isLocalFile := !strings.HasPrefix(config.GtfsURL, "http://") && !strings.HasPrefix(config.GtfsURL, "https://")

	logger := slog.Default().With(slog.String("component", "gtfs_manager"))

	var staticData *gtfs.Static
	var gtfsDB *gtfsdb.Client
	var err error

	// Use configurable backoffs or default to production values
	backoffs := config.StartupRetries
	if len(backoffs) == 0 {
		backoffs = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	}
	maxAttempts := len(backoffs) + 1

	// Skip retries for local files - they will fail identically every time
	if isLocalFile {
		maxAttempts = 1
	}

	var attemptsMade int

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptsMade = attempt
		// Attempt to load in-memory static data if we haven't already succeeded
		if staticData == nil {
			staticData, err = loadGTFSData(config.GtfsURL, isLocalFile, config)
			if err != nil {
				if attempt < maxAttempts {
					delay := backoffs[attempt-1]
					logging.LogError(logger, "Failed to load GTFS static data, retrying", err,
						slog.Int("attempt", attempt),
						slog.Int("max_attempts", maxAttempts),
						slog.Duration("retry_delay", delay),
					)

					// Cancellable sleep
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(delay):
					}
					continue
				}
				return nil, fmt.Errorf("failed to load GTFS data after %d attempts: %w", maxAttempts, err)
			}

			// Perform structural validation on the in-memory data
			if err = gtfsdb.ValidateGTFSData(staticData); err != nil {
				if attempt < maxAttempts {
					delay := backoffs[attempt-1]
					logging.LogError(logger, "GTFS static data structural validation failed, retrying", err,
						slog.Int("attempt", attempt),
						slog.Int("max_attempts", maxAttempts),
						slog.Duration("retry_delay", delay),
					)

					// Reset staticData to nil so the retry loop fetches it again
					staticData = nil

					// Cancellable sleep
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(delay):
					}
					continue
				}
				return nil, fmt.Errorf("failed GTFS structural validation after %d attempts: %w", maxAttempts, err)
			}
		}

		// Attempt to build the SQLite DB if we haven't already succeeded
		if gtfsDB == nil {
			// Clean up partial SQLite file from previous failed attempts
			if attempt > 1 && config.GTFSDataPath != "" && config.GTFSDataPath != ":memory:" {
				if removeErr := os.Remove(config.GTFSDataPath); removeErr != nil && !os.IsNotExist(removeErr) {
					logging.LogError(logger, "Failed to clean up partial SQLite file before retry", removeErr,
						slog.String("path", config.GTFSDataPath),
						slog.Int("attempt", attempt),
					)
				}
			}

			gtfsDB, err = buildGtfsDB(ctx, config, isLocalFile, "")
			if err != nil {
				if attempt < maxAttempts {
					delay := backoffs[attempt-1]
					logging.LogError(logger, "Failed to build GTFS database, retrying", err,
						slog.Int("attempt", attempt),
						slog.Int("max_attempts", maxAttempts),
						slog.Duration("retry_delay", delay),
					)

					// Cancellable sleep
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(delay):
					}
					continue
				}
				return nil, fmt.Errorf("failed to build GTFS database after %d attempts: %w", maxAttempts, err)
			}
		}

		// Both loads succeeded, break out of the retry loop
		break
	}

	// Log success if we recovered via retries
	if attemptsMade > 1 {
		logger.Info("GTFS data loaded after retry", slog.Int("attempts", attemptsMade))
	}

	manager := &Manager{
		isLocalFile:                    isLocalFile,
		config:                         config,
		shutdownChan:                   make(chan struct{}),
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
		feedTrips:                      make(map[string][]gtfs.Trip),
		feedVehicles:                   make(map[string][]gtfs.Vehicle),
		feedAlerts:                     make(map[string][]gtfs.Alert),
		feedAgencyFilter:               make(map[string]map[string]bool),
		feedVehicleLastSeen:            make(map[string]map[string]time.Time),
		feedVehicleTimestamp:           make(map[string]uint64),
		frequencyTripIDs:               make(map[string]struct{}),
		Metrics:                        config.Metrics,
	}

	// Build per-feed agency filters from config
	for _, feedCfg := range config.RTFeeds {
		if len(feedCfg.AgencyIDs) > 0 {
			filter := make(map[string]bool, len(feedCfg.AgencyIDs))
			for _, id := range feedCfg.AgencyIDs {
				filter[id] = true
			}
			manager.feedAgencyFilter[feedCfg.ID] = filter
		}
	}

	manager.setStaticGTFS(staticData)
	manager.GtfsDB = gtfsDB

	// Startup validation and logging for agency filtering
	enabledFeeds := config.enabledFeeds()
	for _, feedCfg := range enabledFeeds {
		if len(feedCfg.AgencyIDs) > 0 {
			logger.Info("realtime feed agency filtering active",
				slog.String("feed", feedCfg.ID),
				slog.Any("agency_ids", feedCfg.AgencyIDs),
			)

			manager.staticMutex.RLock()
			var validAgencies []string
			for _, configuredAgencyID := range feedCfg.AgencyIDs {
				if _, exists := manager.agenciesMap[configuredAgencyID]; !exists {
					if validAgencies == nil {
						for validID := range manager.agenciesMap {
							validAgencies = append(validAgencies, validID)
						}
						sort.Strings(validAgencies)
					}

					logger.Warn("configured agency-id not found in static GTFS data",
						slog.String("feed", feedCfg.ID),
						slog.String("invalid_agency_id", configuredAgencyID),
						slog.Any("valid_agency_ids", validAgencies),
					)
				}
			}
			manager.staticMutex.RUnlock()
		}
	}
	manager.parseAndLogFeedExpiryLocked(ctx, logger)

	// Populate systemETag from import metadata
	metadata, err := gtfsDB.Queries.GetImportMetadata(ctx)
	if err == nil && metadata.FileHash != "" {
		manager.systemETag = fmt.Sprintf(`"%s"`, metadata.FileHash)
	}

	// Build spatial index for fast stop location queries
	spatialIndex, err := buildStopSpatialIndex(ctx, gtfsDB.Queries)
	if err != nil {
		_ = gtfsDB.Close()
		return nil, fmt.Errorf("error building spatial index: %w", err)
	}
	manager.stopSpatialIndex = spatialIndex

	freqTripIDs := make(map[string]struct{})
	ids, err := gtfsDB.Queries.GetFrequencyTripIDs(ctx)
	if err == nil {
		for _, id := range ids {
			freqTripIDs[id] = struct{}{}
		}
	} else {
		logger.Error("failed to load frequency trip IDs", "error", err)
	}
	manager.frequencyTripIDs = freqTripIDs

	// STARTUP SEQUENCING:
	// If realtime is enabled, perform the first fetch synchronously for each feed
	// to "warm" the cache before marking the manager as ready.
	for _, feedCfg := range enabledFeeds {
		initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		success := manager.updateFeedRealtime(initCtx, feedCfg)
		if !success {
			logger.Warn("initial realtime fetch failed; feed starting in degraded state",
				slog.String("feed", feedCfg.ID))
		}
		cancel()
	}

	// Everything is now warm and ready for traffic
	manager.MarkReady()
	manager.MarkHealthy()

	if !isLocalFile {
		manager.wg.Add(1)
		go manager.updateStaticGTFS()
	}

	// Start one poller goroutine per enabled feed
	for _, feedCfg := range enabledFeeds {
		manager.wg.Add(1)
		go manager.pollFeed(feedCfg)
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

// RLock acquires the static data read lock.
func (manager *Manager) RLock() {
	manager.staticMutex.RLock()
}

// RUnlock releases the static data read lock.
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
	if agency, ok := manager.agenciesMap[id]; ok {
		return agency
	}
	return nil
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) FindRoute(id string) *gtfs.Route {
	if route, ok := manager.routesMap[id]; ok {
		return route
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
	if routes, ok := manager.routesByAgencyID[agencyID]; ok {
		return routes
	}

	return []*gtfs.Route{}
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
				// Use a global radius (20,000 km) to ensure exact stop code
				// searches are never artificially truncated by localized bounding boxes.
				radius = models.GlobalSearchRadiusInMeters
			} else {
				radius = models.DefaultSearchRadiusInMeters // Standard constant for radius
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
		if ctx.Err() != nil {
			return []gtfsdb.Stop{}
		}

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
					if ctx.Err() != nil {
						return []gtfsdb.Stop{}
					}

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
			if err != nil {
				logger := slog.Default().With(slog.String("component", "gtfs_manager"))
				logging.LogError(logger, "could not get active service IDs for date", err, slog.String("date", currentDate))
			}

			if err == nil && len(activeServiceIDs) > 0 {
				stopIDs := make([]string, 0, len(candidates))
				for _, candidate := range candidates {
					stopIDs = append(stopIDs, candidate.stop.ID)
				}

				stopsWithActiveService, err := manager.GtfsDB.Queries.GetStopsWithActiveServiceOnDate(ctx, gtfsdb.GetStopsWithActiveServiceOnDateParams{
					StopIds:    stopIDs,
					ServiceIds: activeServiceIDs,
				})
				if err != nil {
					logger := slog.Default().With(slog.String("component", "gtfs_manager"))
					logging.LogError(logger, "could not get stops with active service on date", err, slog.String("date", currentDate))
				}

				if err == nil {
					stopsWithService := make(map[string]bool)
					for _, stopID := range stopsWithActiveService {
						stopsWithService[stopID] = true
					}

					filteredCandidates := make([]stopWithDistance, 0, len(candidates))
					for _, candidate := range candidates {
						if ctx.Err() != nil {
							return []gtfsdb.Stop{}
						}

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

// VehiclesForAgencyID returns all real-time vehicles serving routes that belong
// to the given agency. It manages its own locking internally; callers must NOT
// hold any Manager locks.
func (manager *Manager) VehiclesForAgencyID(agencyID string) []gtfs.Vehicle {
	// Step 1: Acquire static lock, collect route IDs, then release.
	manager.staticMutex.RLock()
	routes := manager.RoutesForAgencyID(agencyID)
	routeIDs := make(map[string]bool, len(routes))
	for _, route := range routes {
		routeIDs[route.Id] = true
	}
	manager.staticMutex.RUnlock()

	// Step 2: Acquire real-time lock independently to read vehicles.
	rtVehicles := manager.GetRealTimeVehicles()

	var vehicles []gtfs.Vehicle
	for _, v := range rtVehicles {
		if v.Trip != nil && routeIDs[v.Trip.ID.RouteID] {
			vehicles = append(vehicles, v)
		}
	}

	return vehicles
}

// GetVehicleForTrip retrieves a vehicle for a specific trip ID or finds the first vehicle that is part of the block
// for that trip. Note we depend on getting the vehicle that may not match the trip ID exactly,
// but is part of the same block.
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetVehicleForTrip(ctx context.Context, tripID string) *gtfs.Vehicle {

	manager.realTimeMutex.RLock()
	if index, exists := manager.realTimeVehicleLookupByTrip[tripID]; exists {
		vehicle := manager.realTimeVehicles[index]
		manager.realTimeMutex.RUnlock()
		return &vehicle
	}
	manager.realTimeMutex.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
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
	if index, exists := manager.realTimeTripLookup[tripID]; exists {
		updates = append(updates, manager.realTimeTrips[index])
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
	logger := slog.Default().With(slog.String("component", "gtfs_manager"))
	logging.LogOperation(logger, "gtfs_statistics",
		slog.String("source", manager.config.GtfsURL),
		slog.Bool("local_file", manager.isLocalFile),
		slog.Time("last_updated", manager.lastUpdated),
		slog.Int("stops", len(manager.gtfsData.Stops)),
		slog.Int("routes", len(manager.gtfsData.Routes)),
		slog.Int("trips", len(manager.gtfsData.Trips)),
		slog.Int("agencies", len(manager.gtfsData.Agencies)))
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

// GetSystemETag retrieves the SystemETag in a thread-safe manner.
// It acquires the static data read lock to prevent data races during GTFS reloads.
func (manager *Manager) GetSystemETag() string {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.systemETag
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

// FeedExpiresAt returns the parsed feed expiry time.
func (manager *Manager) FeedExpiresAt() time.Time {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.feedExpiresAt
}

// SetFeedExpiresAt implicitly sets the parsed feed expiry time for tests.
func (manager *Manager) SetFeedExpiresAt(t time.Time) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()
	manager.feedExpiresAt = t
}

// SetRealTimeTripsForTest manually sets realtime trips for testing purposes.
// It stores the trips under the synthetic feed ID "_test" so that a subsequent
// call to rebuildMergedRealtimeLocked (e.g. from a real feed update) does not
// silently discard the injected data.
func (manager *Manager) SetRealTimeTripsForTest(trips []gtfs.Trip) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	manager.feedTrips["_test"] = trips
	manager.rebuildMergedRealtimeLocked()
}

// AddTestAlert is a helper method used ONLY for testing to inject mock alerts safely.
func (m *Manager) AddTestAlert(alert gtfs.Alert) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()
	// Initialize the map if it doesn't exist
	if m.feedAlerts == nil {
		m.feedAlerts = make(map[string][]gtfs.Alert)
	}
	m.feedAlerts["_test"] = append(m.feedAlerts["_test"], alert)
	m.rebuildMergedRealtimeLocked()
}
