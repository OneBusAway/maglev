package gtfs

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/metrics"
	"maglev.onebusaway.org/internal/utils"

	"github.com/OneBusAway/go-gtfs"
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
	GtfsDB                         *gtfsdb.Client
	lastUpdatedUnixNanos           atomic.Int64 // Lock-free freshness tracking
	realTimeTrips                  []gtfs.Trip
	realTimeVehicles               []gtfs.Vehicle
	realTimeMutex                  sync.RWMutex
	realTimeTripLookup             map[string]int
	realTimeVehicleLookupByTrip    map[string]int
	realTimeVehicleLookupByVehicle map[string]int
	duplicatedVehicleByRoute       map[string][]gtfs.Vehicle
	alertIdx                       alertIndex
	staticUpdateMutex              sync.Mutex // Protects against concurrent ReloadStatic calls
	config                         Config
	shutdownChan                   chan struct{}
	wg                             sync.WaitGroup
	shutdownOnce                   sync.Once
	isReady                        atomic.Bool // Tracks whether initial data loading is complete

	staticMutex   sync.RWMutex
	feedExpiresAt time.Time // Holds the max valid service date for the static feed
	regionBounds  map[string]*RegionBounds
	systemETag    string // systemETag stores the SHA-256 hash of the currently loaded GTFS static dataset.
	lastUpdated   time.Time

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

	// May be nil when running without direction computation (e.g. in tests).
	DirectionCalculator *AdvancedDirectionCalculator

	// Tracks the last successful update time per feed
	feedLastUpdate map[string]time.Time
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

	delete(manager.feedLastUpdate, feedID)

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
	logger := slog.Default().With(slog.String("component", "gtfs_manager"))

	// Use configurable backoffs or default to production values
	backoffs := config.StartupRetries
	if len(backoffs) == 0 {
		backoffs = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	}
	maxAttempts := len(backoffs) + 1

	// Skip retries for local files - they will fail identically every time
	if config.isLocalFile() {
		maxAttempts = 1
	}

	gtfsDB, err := openGtfsDB(config)
	if err != nil {
		return nil, fmt.Errorf("failed to open GTFS database: %w", err)
	}

	manager := &Manager{
		GtfsDB:                         gtfsDB,
		config:                         config,
		shutdownChan:                   make(chan struct{}),
		realTimeTripLookup:             make(map[string]int),
		realTimeVehicleLookupByTrip:    make(map[string]int),
		realTimeVehicleLookupByVehicle: make(map[string]int),
		duplicatedVehicleByRoute:       make(map[string][]gtfs.Vehicle),
		feedTrips:                      make(map[string][]gtfs.Trip),
		feedVehicles:                   make(map[string][]gtfs.Vehicle),
		feedAlerts:                     make(map[string][]gtfs.Alert),
		feedLastUpdate:                 make(map[string]time.Time),
		feedAgencyFilter:               make(map[string]map[string]bool),
		feedVehicleLastSeen:            make(map[string]map[string]time.Time),
		feedVehicleTimestamp:           make(map[string]uint64),
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

	var attemptsMade int
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptsMade = attempt
		_, reloadErr := manager.ReloadStatic(ctx)
		if reloadErr == nil {
			break
		}
		if attempt < maxAttempts {
			delay := backoffs[attempt-1]
			logging.LogError(logger, "Failed to load GTFS data, retrying", reloadErr,
				slog.Int("attempt", attempt),
				slog.Int("max_attempts", maxAttempts),
				slog.Duration("retry_delay", delay),
			)
			select {
			case <-ctx.Done():
				_ = gtfsDB.Close()
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		err = gtfsDB.Close()
		if err != nil {
			logging.LogError(logger, "closing DB failed", err)
		}
		return nil, fmt.Errorf("failed to load GTFS data after %d attempts: %w", maxAttempts, reloadErr)
	}

	if attemptsMade > 1 {
		logger.Info("GTFS data loaded after retry", slog.Int("attempts", attemptsMade))
	}

	manager.PrintStatistics()

	// Startup validation and logging for agency filtering
	validAgencies, err := manager.GtfsDB.Queries.ListAgencyIds(ctx)
	if err != nil {
		return nil, err
	}

	enabledFeeds := config.enabledFeeds()
	for _, feedCfg := range enabledFeeds {
		if len(feedCfg.AgencyIDs) > 0 {
			logger.Info("realtime feed agency filtering active",
				slog.String("feed", feedCfg.ID),
				slog.Any("agency_ids", feedCfg.AgencyIDs),
			)

			for _, configuredAgencyID := range feedCfg.AgencyIDs {
				found := slices.Index(validAgencies, configuredAgencyID) != -1
				if !found {
					logger.Warn("configured agency-id not found in static GTFS data",
						slog.String("feed", feedCfg.ID),
						slog.String("invalid_agency_id", configuredAgencyID),
						slog.Any("valid_agency_ids", validAgencies),
					)
				}
			}
		}
	}
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

	if !config.isLocalFile() {
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

// GetAgencies returns all agencies from the database.
func (manager *Manager) GetAgencies(ctx context.Context) ([]gtfsdb.Agency, error) {
	return manager.GtfsDB.Queries.ListAgencies(ctx)
}

// GetTrips returns up to limit trips from the database.
func (manager *Manager) GetTrips(ctx context.Context, limit int64) ([]gtfsdb.Trip, error) {
	return manager.GtfsDB.Queries.ListTripsWithLimit(ctx, limit)
}

func (manager *Manager) GetStops(ctx context.Context) ([]gtfsdb.Stop, error) {
	return manager.GtfsDB.Queries.ListStops(ctx)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) FindAgency(ctx context.Context, id string) (*gtfsdb.Agency, error) {
	agency, err := manager.GtfsDB.Queries.GetAgency(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	return &agency, nil
}

func (manager *Manager) GetRoutes(ctx context.Context) ([]gtfsdb.Route, error) {
	return manager.GtfsDB.Queries.ListRoutes(ctx)
}

// RoutesForAgencyID retrieves all routes associated with the specified agency ID from the GTFS data.
func (manager *Manager) RoutesForAgencyID(ctx context.Context, agencyID string) ([]gtfsdb.GetRoutesForAgencyRow, error) {
	return manager.GtfsDB.Queries.GetRoutesForAgency(ctx, agencyID)
}

// GetStopsForLocation retrieves stops near a given location using the spatial index.
// It supports filtering by route types and querying for specific stop codes.
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
//
// GetStopsForLocation is used by the stops-for-location endpoint.
// BOUNDS mode (no routeTypes): shuffles stops then truncates before route-type filtering.
// ORDERED_BY_CLOSEST mode (routeTypes present): sorts by distance, filters by route type, then truncates.
func (manager *Manager) GetStopsForLocation(
	ctx context.Context,
	loc *LocationParams,
	stopCodeQuery string,
	maxCount int,
	routeTypes []int,
) ([]gtfsdb.Stop, bool) {
	bounds := BoundsFromParams(loc)
	if ctx.Err() != nil {
		return []gtfsdb.Stop{}, false
	}

	stops, err := manager.queryStopsInBounds(ctx, bounds)
	if err != nil {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogError(logger, "could not query stops within bounds", err)
		return []gtfsdb.Stop{}, false
	}

	if stopCodeQuery != "" {
		idx := slices.IndexFunc(stops, func(stop gtfsdb.Stop) bool {
			return utils.NullStringOrEmpty(stop.Code) == stopCodeQuery
		})
		if idx >= 0 {
			return []gtfsdb.Stop{stops[idx]}, false
		}
		return nil, false
	}

	var limitExceeded bool
	if len(routeTypes) == 0 {
		// BOUNDS mode: shuffle then truncate before route filtering.
		limitExceeded = len(stops) > maxCount
		if limitExceeded {
			rand.Shuffle(len(stops), func(i, j int) { stops[i], stops[j] = stops[j], stops[i] })
			stops = stops[:maxCount]
		}
	} else {
		// ORDERED_BY_CLOSEST mode: sort by distance, filter by route type, then truncate.
		slices.SortFunc(stops, func(a, b gtfsdb.Stop) int {
			aDist := utils.Distance(loc.Lat, loc.Lon, a.Lat, a.Lon)
			bDist := utils.Distance(loc.Lat, loc.Lon, b.Lat, b.Lon)
			return cmp.Compare(aDist, bDist)
		})

		stopIDs := make([]string, 0, len(stops))
		for _, stop := range stops {
			stopIDs = append(stopIDs, stop.ID)
		}

		routesForStops, err := manager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
		if err == nil {
			stopRouteTypes := make(map[string][]int)
			for _, r := range routesForStops {
				stopRouteTypes[r.StopID] = append(stopRouteTypes[r.StopID], int(r.Type))
			}
			filteredStops := make([]gtfsdb.Stop, 0, len(stops))
			for _, stop := range stops {
				for _, rt := range stopRouteTypes[stop.ID] {
					if slices.Contains(routeTypes, rt) {
						filteredStops = append(filteredStops, stop)
						break
					}
				}
			}
			stops = filteredStops
		}

		if len(stops) > maxCount {
			limitExceeded = true
			stops = stops[:maxCount]
		}
	}

	return stops, limitExceeded
}

// GetStopsInBounds returns stops within the given bounds up to maxCount, without shuffling
// or route-type filtering. Used internally by the arrivals and trips-for-location handlers.
func (manager *Manager) GetStopsInBounds(
	ctx context.Context,
	loc *LocationParams,
	maxCount int,
) []gtfsdb.Stop {
	bounds := BoundsFromParams(loc)
	stops, err := manager.queryStopsInBounds(ctx, bounds)
	if err != nil {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogError(logger, "could not query stops within bounds", err)
		return nil
	}
	if maxCount > 0 && len(stops) > maxCount {
		stops = stops[:maxCount]
	}
	return stops
}

// GetStopIDsWithinBounds returns stop IDs within bounds, optimized for callers that only need IDs.
func (manager *Manager) GetStopIDsWithinBounds(
	ctx context.Context,
	loc *LocationParams,
	maxCount int,
) []string {
	bounds := BoundsFromParams(loc)
	ids, err := manager.GtfsDB.Queries.GetStopIDsWithinBounds(ctx, gtfsdb.GetStopIDsWithinBoundsParams{
		MinLat: bounds.MinLat,
		MaxLat: bounds.MaxLat,
		MinLon: bounds.MinLon,
		MaxLon: bounds.MaxLon,
	})
	if err != nil {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogError(logger, "could not query stop IDs within bounds", err)
		return nil
	}
	if maxCount > 0 && len(ids) > maxCount {
		ids = ids[:maxCount]
	}
	return ids
}

// queryStopsInBounds retrieves all active stops within the given geographic bounds
// from the database's stops_rtree spatial index.
func (manager *Manager) queryStopsInBounds(ctx context.Context, bounds utils.CoordinateBounds) ([]gtfsdb.Stop, error) {
	if bounds.MinLat > bounds.MaxLat {
		return nil, fmt.Errorf("query min lat %f exceeds max lat %f", bounds.MinLat, bounds.MaxLat)
	}
	if bounds.MinLon > bounds.MaxLon {
		return nil, fmt.Errorf("query min lon %f exceeds max lon %f", bounds.MinLon, bounds.MaxLon)
	}
	return manager.GtfsDB.Queries.GetActiveStopsWithinBounds(ctx, gtfsdb.GetActiveStopsWithinBoundsParams{
		MinLat: bounds.MinLat,
		MaxLat: bounds.MaxLat,
		MinLon: bounds.MinLon,
		MaxLon: bounds.MaxLon,
	})
}

// GetRoutesForLocation retrieves routes serving stops near a given location using the spatial index.
// It supports filtering by route types and querying for specific route shortNames.
func (manager *Manager) GetRoutesForLocation(
	ctx context.Context,
	loc *LocationParams,
	routeShortName string,
	maxCount int,
	queryTime time.Time,
) ([]gtfsdb.Route, bool) {
	bounds := BoundsFromParams(loc)
	routes, limitExceeded, err := manager.queryRoutesInBounds(ctx, bounds, loc.Lat, loc.Lon, maxCount, routeShortName)
	if err != nil {
		logger := slog.Default().With(slog.String("component", "gtfs_manager"))
		logging.LogError(logger, "could not query routes within bounds", err)
		return []gtfsdb.Route{}, false
	}

	return routes, limitExceeded
}

// queryRoutesInBounds retrieves all routes serving stops within the given geographic bounds
// from the database's stops_rtree spatial index.
// Despite the query's name, this doesn't actually check "Active" stops beyond
// checking that the stop has at least one stop_time. The corresponding GetStopsForLocation
// checks active service dates as well.
func (manager *Manager) queryRoutesInBounds(ctx context.Context, bounds utils.CoordinateBounds,
	lat, lon float64,
	maxCount int,
	shortNameQuery string,
) ([]gtfsdb.Route, bool, error) {
	if bounds.MinLat > bounds.MaxLat {
		return nil, false, fmt.Errorf("query min lat %f exceeds max lat %f", bounds.MinLat, bounds.MaxLat)
	}
	if bounds.MinLon > bounds.MaxLon {
		return nil, false, fmt.Errorf("query min lon %f exceeds max lon %f", bounds.MinLon, bounds.MaxLon)
	}
	routes, err := manager.GtfsDB.Queries.GetActiveRoutesWithinBounds(ctx, gtfsdb.GetActiveRoutesWithinBoundsParams{
		MinLat: bounds.MinLat,
		MaxLat: bounds.MaxLat,
		MinLon: bounds.MinLon,
		MaxLon: bounds.MaxLon,
		Lat:    lat,
		Lon:    lon,
		// Ask for an extra element so that we can determine if we hit the max count.
		MaxCount:  maxCount + 1,
		ShortName: shortNameQuery,
	})
	if err != nil {
		return nil, false, err
	}

	if len(routes) > maxCount {
		// Drop the extra last element. This is correct because results are in ascending distance order.
		routes = routes[:maxCount]
		return routes, true, nil
	}

	return routes, false, nil
}

// VehiclesForAgencyID returns all real-time vehicles serving routes that belong
// to the given agency. It manages its own locking internally; callers must NOT
// hold any Manager locks.
func (manager *Manager) VehiclesForAgencyID(ctx context.Context, agencyID string) ([]gtfs.Vehicle, error) {
	routes, err := manager.RoutesForAgencyID(ctx, agencyID)
	if err != nil {
		return nil, err
	}

	routeIDs := make(map[string]bool, len(routes))
	for _, route := range routes {
		routeIDs[route.ID] = true
	}

	// Step 2: Acquire real-time lock independently to read vehicles.
	rtVehicles := manager.GetRealTimeVehicles()

	var vehicles []gtfs.Vehicle
	for _, v := range rtVehicles {
		if v.Trip != nil && routeIDs[v.Trip.ID.RouteID] {
			vehicles = append(vehicles, v)
		}
	}

	return vehicles, nil
}

// GetDuplicatedVehiclesForRoute returns real-time vehicles serving DUPLICATED trips
// (GTFS-RT schedule_relationship=DUPLICATED) for the given route ID.
// DUPLICATED trips are extra runs of a scheduled trip, each assigned to a different
// vehicle. They only exist in real-time data and have no static DB entry.
func (manager *Manager) GetDuplicatedVehiclesForRoute(routeID string) []gtfs.Vehicle {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	src := manager.duplicatedVehicleByRoute[routeID]

	out := make([]gtfs.Vehicle, len(src))
	copy(out, src)
	return out
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

func (manager *Manager) GetVehicleLastUpdateTime(vehicle *gtfs.Vehicle) time.Time {
	if vehicle == nil || vehicle.Timestamp == nil {
		return time.Time{}
	}
	return *vehicle.Timestamp
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
	if manager.GtfsDB == nil || manager.GtfsDB.Queries == nil {
		return
	}

	ctx := context.Background()
	logger := slog.Default().With(slog.String("component", "gtfs_manager"))

	countOrZero := func(n int64, err error) int64 {
		if err != nil {
			return 0
		}
		return n
	}

	logging.LogOperation(logger, "gtfs_statistics",
		slog.String("source", manager.config.GtfsURL),
		slog.Time("last_updated", manager.lastUpdated),
		slog.Int64("stops", countOrZero(manager.GtfsDB.Queries.CountStops(ctx))),
		slog.Int64("routes", countOrZero(manager.GtfsDB.Queries.CountRoutes(ctx))),
		slog.Int64("trips", countOrZero(manager.GtfsDB.Queries.CountTrips(ctx))),
		slog.Int64("agencies", countOrZero(manager.GtfsDB.Queries.CountAgencies(ctx))))
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

// FeedExpiresAt returns the parsed feed expiry time.
func (manager *Manager) FeedExpiresAt() time.Time {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	return manager.feedExpiresAt
}

// SetFeedExpiresAtForTest implicitly sets the parsed feed expiry time for tests.
func (manager *Manager) SetFeedExpiresAtForTest(t time.Time) {
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

// GetStaticLastUpdated returns the timestamp when static GTFS data was last loaded lock-free.
func (manager *Manager) GetStaticLastUpdated() time.Time {
	nanos := manager.lastUpdatedUnixNanos.Load()
	if nanos == 0 {
		return time.Time{}
	}
	// Append .UTC() here to ensure the RFC3339 string always ends in 'Z'
	return time.Unix(0, nanos).UTC()
}

// GetFeedUpdateTimes returns a copy of the last update times for all realtime feeds.
func (manager *Manager) GetFeedUpdateTimes() map[string]time.Time {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	// Return a copy to prevent concurrent map read/write outside the lock
	result := make(map[string]time.Time, len(manager.feedLastUpdate))
	for k, v := range manager.feedLastUpdate {
		result[k] = v
	}
	return result
}

// SetFeedUpdateTimeForTest safely records the time a feed was successfully updated.
func (manager *Manager) SetFeedUpdateTimeForTest(feedID string, t time.Time) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	// Ensure map is initialized (helpful for tests)
	if manager.feedLastUpdate == nil {
		manager.feedLastUpdate = make(map[string]time.Time)
	}

	manager.feedLastUpdate[feedID] = t
}

// SetStaticLastUpdatedForTest manually sets the static data timestamp for testing purposes.
func (manager *Manager) SetStaticLastUpdatedForTest(t time.Time) {
	manager.staticMutex.Lock()
	defer manager.staticMutex.Unlock()
	manager.lastUpdated = t
	manager.lastUpdatedUnixNanos.Store(t.UnixNano())
}

// AddAlertForTest is a helper method used ONLY for testing to inject mock alerts safely.
func (m *Manager) AddAlertForTest(alert gtfs.Alert) {
	m.realTimeMutex.Lock()
	defer m.realTimeMutex.Unlock()
	// Initialize the map if it doesn't exist
	if m.feedAlerts == nil {
		m.feedAlerts = make(map[string][]gtfs.Alert)
	}
	m.feedAlerts["_test"] = append(m.feedAlerts["_test"], alert)
	m.rebuildMergedRealtimeLocked()
}
