package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/utils"
)

// alertIndex holds pre-built maps for O(1) alert lookups, keyed by trip, route, agency, and stop IDs.
// It is rebuilt on every call to rebuildMergedRealtimeLocked under the existing write lock.
type alertIndex struct {
	byTrip   map[string][]gtfs.Alert
	byRoute  map[string][]gtfs.Alert
	byAgency map[string][]gtfs.Alert
	byStop   map[string][]gtfs.Alert
}

// mergedRealtime is the merged view of every realtime feed, published as a
// single immutable value.
//
// INVARIANT: nothing reachable from a mergedRealtime is mutated after it is
// stored. rebuildMergedRealtimeLocked always builds a fresh one, and the mock
// helpers copy before they write. That is what lets readers load it without
// holding realTimeMutex.
type mergedRealtime struct {
	trips                    []gtfs.Trip
	vehicles                 []gtfs.Vehicle
	tripLookup               map[string]int
	vehicleLookupByTrip      map[string]int
	vehicleLookupByVehicle   map[string]int
	duplicatedVehicleByRoute map[string][]gtfs.Vehicle
	vehiclesByRoute          map[string][]gtfs.Vehicle
	alerts                   alertIndex
	// exportVehicles feeds the GTFS-RT vehicle export; see ExportVehicles.
	exportVehicles []ExportVehicle
}

// emptyMergedRealtime is returned before the first publish so a zero-value
// Manager, which several tests construct, reads as empty rather than panicking.
var emptyMergedRealtime = &mergedRealtime{
	tripLookup:               map[string]int{},
	vehicleLookupByTrip:      map[string]int{},
	vehicleLookupByVehicle:   map[string]int{},
	duplicatedVehicleByRoute: map[string][]gtfs.Vehicle{},
	vehiclesByRoute:          map[string][]gtfs.Vehicle{},
	alerts: alertIndex{
		byTrip:   map[string][]gtfs.Alert{},
		byRoute:  map[string][]gtfs.Alert{},
		byAgency: map[string][]gtfs.Alert{},
		byStop:   map[string][]gtfs.Alert{},
	},
}

// mergedRealtime loads the current snapshot. Lock-free: callers must not hold
// realTimeMutex for this and must not mutate what they get back.
func (manager *Manager) mergedRealtime() *mergedRealtime {
	if m := manager.merged.Load(); m != nil {
		return m
	}
	return emptyMergedRealtime
}

// clone returns a shallow copy whose maps and slices can be mutated without
// touching the published snapshot. Used only by the Mock* helpers, which build
// the merged view directly instead of going through the feed maps.
func (m *mergedRealtime) clone() *mergedRealtime {
	out := &mergedRealtime{
		trips:                    slices.Clone(m.trips),
		vehicles:                 slices.Clone(m.vehicles),
		tripLookup:               maps.Clone(m.tripLookup),
		vehicleLookupByTrip:      maps.Clone(m.vehicleLookupByTrip),
		vehicleLookupByVehicle:   maps.Clone(m.vehicleLookupByVehicle),
		duplicatedVehicleByRoute: maps.Clone(m.duplicatedVehicleByRoute),
		vehiclesByRoute:          maps.Clone(m.vehiclesByRoute),
		exportVehicles:           slices.Clone(m.exportVehicles),
		alerts: alertIndex{
			byTrip:   maps.Clone(m.alerts.byTrip),
			byRoute:  maps.Clone(m.alerts.byRoute),
			byAgency: maps.Clone(m.alerts.byAgency),
			byStop:   maps.Clone(m.alerts.byStop),
		},
	}
	if out.tripLookup == nil {
		out.tripLookup = map[string]int{}
	}
	if out.vehicleLookupByTrip == nil {
		out.vehicleLookupByTrip = map[string]int{}
	}
	if out.vehicleLookupByVehicle == nil {
		out.vehicleLookupByVehicle = map[string]int{}
	}
	if out.duplicatedVehicleByRoute == nil {
		out.duplicatedVehicleByRoute = map[string][]gtfs.Vehicle{}
	}
	if out.vehiclesByRoute == nil {
		out.vehiclesByRoute = map[string][]gtfs.Vehicle{}
	}
	return out
}

// staleVehicleTimeout is the duration after which a vehicle is considered stale
const staleVehicleTimeout = 15 * time.Minute

// staleFeedThreshold is the duration after which feed data is cleared if fetches keep failing
const staleFeedThreshold = 5 * time.Minute

// realtimeHTTPClient is a dedicated HTTP client for GTFS-RT feed fetching,
// configured with explicit timeouts and transport limits to avoid the pitfalls
// of http.DefaultClient (no timeout, shared global state).
// The transport is cloned from http.DefaultTransport to preserve important
// defaults (ProxyFromEnvironment, DialContext, HTTP/2, keepalives).
var realtimeHTTPClient = newRealtimeHTTPClient()

func newRealtimeHTTPClient() *http.Client {
	var transport *http.Transport
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = t.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.MaxIdleConns = 50
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second

	return &http.Client{
		// Timeout acts as an absolute safety net per request. The caller in
		// pollFeed also sets a 15s context timeout; the stricter of the two
		// wins. Keep this <= the context timeout so the client enforces the
		// bound even if a caller forgets a context.
		Timeout:   10 * time.Second,
		Transport: transport,
	}
}

// isVehicleStale returns true if the incoming vehicle update is older
// than the existing vehicle based on GTFS-RT timestamps.
func isVehicleStale(existing, incoming gtfs.Vehicle) bool {
	if existing.Timestamp == nil || incoming.Timestamp == nil {
		// If either timestamp is missing, we cannot safely compare
		return false
	}
	return incoming.Timestamp.Before(*existing.Timestamp)
}

// vehicleKey identifies a vehicle across feed updates. Exactly one field is
// set, recording which descriptor field the identity came from, so a real ID
// never equals a label or license plate with the same text.
type vehicleKey struct {
	id           string
	label        string
	licensePlate string
}

// newVehicleKey returns the key for a vehicle descriptor. GTFS-RT allows a
// descriptor with only a label or license plate, which go-gtfs parses with an
// empty ID, so those fields are the fallback.
func newVehicleKey(id *gtfs.VehicleID) vehicleKey {
	if id.ID != "" {
		return vehicleKey{id: id.ID}
	}
	if id.Label != "" {
		return vehicleKey{label: id.Label}
	}
	return vehicleKey{licensePlate: id.LicensePlate}
}

// cleanupExpiredVehicles removes vehicles from both the lastSeenMap and feedVehicles
// that have exceeded the staleVehicleTimeout threshold since they were last seen.
// This ensures a consistent retention window across feed updates.
func (manager *Manager) cleanupExpiredVehicles(feedID string) {
	if manager.feedVehicleLastSeen[feedID] == nil {
		return
	}

	now := time.Now()
	lastSeenMap := manager.feedVehicleLastSeen[feedID]

	// First, delete expired entries from lastSeenMap
	for vid, lastSeen := range lastSeenMap {
		if now.Sub(lastSeen) > staleVehicleTimeout {
			delete(lastSeenMap, vid)
		}
	}

	// Then, rebuild feedVehicles to only include vehicles that are still in lastSeenMap
	// (i.e., within the retention window)
	currentVehicles := manager.feedVehicles[feedID]
	validVehicles := make([]gtfs.Vehicle, 0, len(currentVehicles))
	for _, v := range currentVehicles {
		if v.ID == nil {
			continue
		}
		// Keep the vehicle if it's still in the retention window
		if _, ok := lastSeenMap[newVehicleKey(v.ID)]; ok {
			validVehicles = append(validVehicles, v)
		}
	}
	manager.feedVehicles[feedID] = validVehicles
}

// GetRealTimeTrips returns the current snapshot's trips. The result is shared
// with every other reader and must not be modified; copy anything you intend to
// change.
func (manager *Manager) GetRealTimeTrips() []gtfs.Trip {
	return manager.mergedRealtime().trips
}

// GetRealTimeVehicles returns the current snapshot's vehicles under the same
// read-only contract as GetRealTimeTrips.
func (manager *Manager) GetRealTimeVehicles() []gtfs.Vehicle {
	return manager.mergedRealtime().vehicles
}

func (manager *Manager) GetAlertsByIDs(tripID, routeID, agencyID string) []gtfs.Alert {
	idx := manager.mergedRealtime().alerts

	seen := make(map[string]struct{})
	var alerts []gtfs.Alert
	// Invariant: alertIdx only contains alerts with non-empty IDs (rebuildMergedRealtimeLocked
	// skips any alert where alert.ID == ""), so seen[a.ID] is sufficient for deduplication.
	addUnique := func(candidates []gtfs.Alert) {
		for _, a := range candidates {
			if _, ok := seen[a.ID]; !ok {
				seen[a.ID] = struct{}{}
				alerts = append(alerts, a)
			}
		}
	}

	if tripID != "" {
		addUnique(idx.byTrip[tripID])
	}
	if routeID != "" {
		addUnique(idx.byRoute[routeID])
	}
	if agencyID != "" {
		addUnique(idx.byAgency[agencyID])
	}
	return alerts
}

// GetAlertsForTrip returns alerts matching the trip, its route, or agency.
// It acquires the realTimeMutex internally via GetAlertsByIDs.
func (manager *Manager) GetAlertsForTrip(ctx context.Context, tripID string) []gtfs.Alert {
	var routeID string
	var agencyID string

	if manager.GtfsDB != nil {
		trip, err := manager.GtfsDB.Queries.GetTrip(ctx, tripID)
		if err == nil {
			routeID = trip.RouteID
			route, err := manager.GtfsDB.Queries.GetRoute(ctx, routeID)
			if err == nil {
				agencyID = route.AgencyID
			} else if !errors.Is(err, sql.ErrNoRows) {
				slog.WarnContext(ctx, "Failed to fetch route for alerts; degrading to trip+route matching only",
					slog.String("trip_id", tripID),
					slog.String("route_id", routeID),
					slog.Any("error", err),
				)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			slog.WarnContext(ctx, "Failed to fetch trip for alerts",
				slog.String("trip_id", tripID),
				slog.Any("error", err),
			)
		}
	}

	return manager.GetAlertsByIDs(tripID, routeID, agencyID)
}

// GetAlertsForStop returns deduplicated alerts for the given stopID.
// Deduplication by alert ID is done internally so callers never receive
// duplicate entries even when the same alert ID appears in multiple feeds.
func (manager *Manager) GetAlertsForStop(stopID string) []gtfs.Alert {
	src := manager.mergedRealtime().alerts.byStop[stopID]
	if len(src) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(src))
	out := make([]gtfs.Alert, 0, len(src))
	for _, a := range src {
		if _, ok := seen[a.ID]; !ok {
			seen[a.ID] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}

// Fetches GTFS-RT data from a URL with per-feed headers.
func loadRealtimeData(ctx context.Context, source string, headers map[string]string) (*gtfs.Realtime, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Add(key, value)
	}

	resp, err := realtimeHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute GTFS-RT request: %w", err)
	}

	defer logging.SafeCloseWithLogging(resp.Body,
		slog.Default().With(slog.String("component", "gtfs_realtime_downloader")),
		"http_response_body")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gtfs-rt fetch failed: %s returned %s", source, resp.Status)
	}

	const maxBodySize = 25 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if int64(len(body)) > maxBodySize {
		return nil, fmt.Errorf("GTFS-RT response exceeds size limit of %d bytes", maxBodySize)
	}

	return gtfs.ParseRealtime(body, &gtfs.ParseRealtimeOptions{})
}

// updateFeedRealtime fetches and processes realtime data for a single feed.
// It updates the per-feed sub-maps and then calls rebuildMergedRealtimeLocked.
// Returns true if new data was successfully fetched and processed.
func (manager *Manager) updateFeedRealtime(ctx context.Context, feedCfg RTFeedConfig) bool {
	logger := logging.ForComponent(ctx, "gtfs_realtime")
	feedID := feedCfg.ID

	var wg sync.WaitGroup
	var tripData, vehicleData, alertData *gtfs.Realtime
	var tripErr, vehicleErr, alertErr error

	// Fetch trip updates, vehicle positions, and alerts in parallel
	if feedCfg.TripUpdatesURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tripData, tripErr = loadRealtimeData(ctx, feedCfg.TripUpdatesURL, feedCfg.Headers)
			if tripErr != nil {
				logging.LogError(logger, "Error loading GTFS-RT trip updates data", tripErr,
					slog.String("feed", feedID),
					slog.String("url", feedCfg.TripUpdatesURL))
			}
		}()
	}

	if feedCfg.VehiclePositionsURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vehicleData, vehicleErr = loadRealtimeData(ctx, feedCfg.VehiclePositionsURL, feedCfg.Headers)
			if vehicleErr != nil {
				logging.LogError(logger, "Error loading GTFS-RT vehicle positions data", vehicleErr,
					slog.String("feed", feedID),
					slog.String("url", feedCfg.VehiclePositionsURL))
			}
		}()
	}

	if feedCfg.ServiceAlertsURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alertData, alertErr = loadRealtimeData(ctx, feedCfg.ServiceAlertsURL, feedCfg.Headers)
			if alertErr != nil {
				logging.LogError(logger, "Error loading GTFS-RT service alerts data", alertErr,
					slog.String("feed", feedID),
					slog.String("url", feedCfg.ServiceAlertsURL))
			}
		}()
	}

	wg.Wait()

	// Check for context cancellation
	if ctx.Err() != nil {
		return false
	}

	// Block matching queries static data, so it runs before realTimeMutex is
	// taken, and on the unfiltered feed because the export keeps what the
	// agency filter drops.
	var unfilteredVehicles []gtfs.Vehicle
	if vehicleData != nil && vehicleErr == nil {
		unfilteredVehicles = vehicleData.Vehicles
	}
	var tripRefs []tripUpdateRef
	var tripUpdateBlocks map[string]*BlockMatch
	if tripData != nil && tripErr == nil {
		tripRefs = tripUpdateRefsInFeedOrder(tripData.Trips)
		tripRefs = manager.assignTripUpdateVehicles(ctx, feedID, tripRefs, unfilteredVehicles)
		tripUpdateBlocks = manager.tripUpdateBlockMatches(ctx, feedID, tripRefs, tripData.CreatedAt)
	}

	// Apply agency-based filtering if configured for this feed.
	// This runs before acquiring realTimeMutex to keep the critical section short.
	agencyFilter := manager.feedAgencyFilter[feedID]
	var unattributedTrips []gtfs.Trip
	if len(agencyFilter) > 0 {
		routeIDs := collectRealtimeRouteIDs(tripData, tripErr, vehicleData, vehicleErr, alertData, alertErr)
		routeAgencyMap, err := manager.buildRouteAgencyMap(ctx, routeIDs)
		if err != nil {
			logging.LogError(logger, "Error resolving route agencies for realtime filter", err,
				slog.String("feed", feedID))
			return false
		}
		if tripData != nil && tripErr == nil {
			agencyByTripID, err := manager.tripAgencyIDs(ctx, tripData.Trips, routeAgencyMap)
			if err != nil {
				logging.LogError(logger, "Error resolving trip agencies for realtime filter", err,
					slog.String("feed", feedID))
				return false
			}
			tripData.Trips, unattributedTrips = filterTripsByAgency(tripData.Trips, agencyFilter, agencyByTripID)
		}
		if vehicleData != nil && vehicleErr == nil {
			vehicleData.Vehicles = filterVehiclesByAgency(vehicleData.Vehicles, agencyFilter, routeAgencyMap)
		}
		if alertData != nil && alertErr == nil {
			alertData.Alerts = filterAlertsByAgency(alertData.Alerts, agencyFilter, routeAgencyMap)
		}
	}

	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	if tripData != nil && tripErr == nil {
		manager.feedTrips[feedID] = tripData.Trips
		if manager.feedUnattributedTrips == nil {
			manager.feedUnattributedTrips = make(map[string][]gtfs.Trip)
		}
		manager.feedUnattributedTrips[feedID] = unattributedTrips
		manager.storeTripUpdateExportStateLocked(feedID, tripRefs, tripUpdateBlocks)
	}

	if vehicleData != nil && vehicleErr == nil {
		applyVehicleUpdate := true

		// Guard against zero CreatedAt from feeds without FeedHeader timestamp.
		// When CreatedAt is zero time.Time{}, UnixNano() returns a negative value that
		// wraps to ~11.6×10¹⁸ when cast to uint64, which would permanently block updates.
		if vehicleData.CreatedAt.IsZero() {
			// Feed has no FeedHeader timestamp — cannot compare freshness, always apply
			applyVehicleUpdate = true
		} else {
			feedTimestamp := uint64(vehicleData.CreatedAt.UnixNano())
			if feedTimestamp <= manager.feedVehicleTimestamp[feedID] {
				logging.LogOperation(
					logger,
					"skipping_stale_vehicle_realtime_feed",
					slog.String("feed", feedID),
					slog.Uint64("feed_timestamp", feedTimestamp),
					slog.Uint64("last_applied_timestamp", manager.feedVehicleTimestamp[feedID]),
				)
				// Skip applying vehicle updates, but still run cleanup
				applyVehicleUpdate = false
			} else {
				// Record the latest applied vehicle feed timestamp
				manager.feedVehicleTimestamp[feedID] = feedTimestamp
			}
		}

		if applyVehicleUpdate {
			prevVehicles := manager.feedVehicles[feedID]
			prevByID := make(map[vehicleKey]gtfs.Vehicle, len(prevVehicles))
			for _, pv := range prevVehicles {
				if pv.ID != nil {
					prevByID[newVehicleKey(pv.ID)] = pv
				}
			}

			validVehicles := make([]gtfs.Vehicle, 0, len(vehicleData.Vehicles))
			for _, v := range vehicleData.Vehicles {
				if v.ID == nil {
					continue
				}

				if prev, exists := prevByID[newVehicleKey(v.ID)]; exists {
					if isVehicleStale(prev, v) {
						// Log and keep the newer existing vehicle, dropping the stale update
						logging.LogOperation(logger, "skipping_stale_vehicle_entity",
							slog.String("feed", feedID),
							slog.String("vehicle_id", v.ID.ID),
							slog.String("vehicle_label", v.ID.Label),
							slog.Time("existing_timestamp", *prev.Timestamp),
							slog.Time("incoming_timestamp", *v.Timestamp),
						)
						validVehicles = append(validVehicles, prev)
						continue
					}
				}

				validVehicles = append(validVehicles, v)
			}

			now := time.Now()
			if manager.feedVehicleLastSeen[feedID] == nil {
				manager.feedVehicleLastSeen[feedID] = make(map[vehicleKey]time.Time)
			}
			lastSeenMap := manager.feedVehicleLastSeen[feedID]

			currentVehicleIDs := make(map[vehicleKey]struct{}, len(validVehicles))
			for _, v := range validVehicles {
				key := newVehicleKey(v.ID)
				lastSeenMap[key] = now
				currentVehicleIDs[key] = struct{}{}
			}

			// Delete stale vehicles
			for vid, lastSeen := range lastSeenMap {
				if _, current := currentVehicleIDs[vid]; !current {
					if now.Sub(lastSeen) > staleVehicleTimeout {
						delete(lastSeenMap, vid)
					}
				}
			}

			// Retain recently-disappeared vehicles whose last-seen time hasn't expired
			prevVehicles = manager.feedVehicles[feedID]
			for _, pv := range prevVehicles {
				if pv.ID == nil {
					continue
				}
				key := newVehicleKey(pv.ID)
				if _, current := currentVehicleIDs[key]; !current {
					if lastSeen, ok := lastSeenMap[key]; ok && now.Sub(lastSeen) <= staleVehicleTimeout {
						validVehicles = append(validVehicles, pv)
					}
				}
			}

			manager.feedVehicles[feedID] = validVehicles
			manager.storeFilteredOutVehiclesLocked(feedID, vehiclesMissingFrom(unfilteredVehicles, vehicleData.Vehicles))
		} else {
			// Even when skipping the vehicle update due to staleness, still clean up
			// expired vehicles based on the last-seen timeout windows
			manager.cleanupExpiredVehicles(feedID)
		}
	}

	if alertData != nil && alertErr == nil {
		manager.feedAlerts[feedID] = alertData.Alerts
	}

	tripsUpdated := tripData != nil && tripErr == nil
	vehiclesUpdated := vehicleData != nil && vehicleErr == nil
	alertsUpdated := alertData != nil && alertErr == nil

	// OR logic: A feed is partially successful if ANY configured sub-feed succeeds.
	hasNewData := false
	hasURLs := false

	if feedCfg.TripUpdatesURL != "" {
		hasURLs = true
		if tripsUpdated {
			hasNewData = true
		}
	}
	if feedCfg.VehiclePositionsURL != "" {
		hasURLs = true
		if vehiclesUpdated {
			hasNewData = true
		}
	}
	if feedCfg.ServiceAlertsURL != "" {
		hasURLs = true
		if alertsUpdated {
			hasNewData = true
		}
	}

	if !hasURLs {
		hasNewData = false
	}

	// Logging based on partial vs total success
	if hasNewData {
		fullSuccess := true
		if feedCfg.TripUpdatesURL != "" && !tripsUpdated {
			fullSuccess = false
		}
		if feedCfg.VehiclePositionsURL != "" && !vehiclesUpdated {
			fullSuccess = false
		}
		if feedCfg.ServiceAlertsURL != "" && !alertsUpdated {
			fullSuccess = false
		}

		if fullSuccess {
			logger.Debug("updated realtime feed successfully",
				slog.String("feed", feedID),
				slog.Int("trips", len(manager.feedTrips[feedID])),
				slog.Int("vehicles", len(manager.feedVehicles[feedID])),
				slog.Int("alerts", len(manager.feedAlerts[feedID])),
			)
		} else {
			logger.Warn("realtime feed partially updated",
				slog.String("feed", feedID),
				slog.Bool("trip_updates_configured", feedCfg.TripUpdatesURL != ""),
				slog.Bool("trip_updates_success", tripsUpdated),
				slog.Bool("vehicle_positions_configured", feedCfg.VehiclePositionsURL != ""),
				slog.Bool("vehicle_positions_success", vehiclesUpdated),
				slog.Bool("service_alerts_configured", feedCfg.ServiceAlertsURL != ""),
				slog.Bool("service_alerts_success", alertsUpdated),
			)
		}
	} else {
		logger.Error("realtime feed update failed",
			slog.String("feed", feedID),
			slog.Bool("trip_updates_configured", feedCfg.TripUpdatesURL != ""),
			slog.Bool("trip_updates_error", tripErr != nil),
			slog.Bool("vehicle_positions_configured", feedCfg.VehiclePositionsURL != ""),
			slog.Bool("vehicle_positions_error", vehicleErr != nil),
			slog.Bool("service_alerts_configured", feedCfg.ServiceAlertsURL != ""),
			slog.Bool("service_alerts_error", alertErr != nil),
		)
	}

	manager.rebuildMergedRealtimeLocked()

	// Update timestamp within the same lock
	if hasNewData {
		if manager.feedLastUpdate == nil {
			manager.feedLastUpdate = make(map[string]time.Time)
		}
		manager.feedLastUpdate[feedID] = time.Now()
	}

	return hasNewData
}

// collectRealtimeRouteIDs gathers unique route IDs from the sub-feeds that
// decoded successfully so the agency filter can resolve them in one lookup.
func collectRealtimeRouteIDs(
	tripData *gtfs.Realtime, tripErr error,
	vehicleData *gtfs.Realtime, vehicleErr error,
	alertData *gtfs.Realtime, alertErr error,
) map[string]struct{} {
	routeIDSet := make(map[string]struct{})
	if tripData != nil && tripErr == nil {
		collectTripRouteIDs(routeIDSet, tripData.Trips)
	}
	if vehicleData != nil && vehicleErr == nil {
		collectVehicleRouteIDs(routeIDSet, vehicleData.Vehicles)
	}
	if alertData != nil && alertErr == nil {
		collectAlertRouteIDs(routeIDSet, alertData.Alerts)
	}
	return routeIDSet
}

func collectTripRouteIDs(routeIDSet map[string]struct{}, trips []gtfs.Trip) {
	for _, trip := range trips {
		if trip.ID.RouteID != "" {
			routeIDSet[trip.ID.RouteID] = struct{}{}
		}
	}
}

func collectVehicleRouteIDs(routeIDSet map[string]struct{}, vehicles []gtfs.Vehicle) {
	for _, v := range vehicles {
		if v.Trip != nil && v.Trip.ID.RouteID != "" {
			routeIDSet[v.Trip.ID.RouteID] = struct{}{}
		}
	}
}

func collectAlertRouteIDs(routeIDSet map[string]struct{}, alerts []gtfs.Alert) {
	for _, alert := range alerts {
		for _, entity := range alert.InformedEntities {
			if entity.RouteID != nil && *entity.RouteID != "" {
				routeIDSet[*entity.RouteID] = struct{}{}
			}
			if entity.TripID != nil && entity.TripID.RouteID != "" {
				routeIDSet[entity.TripID.RouteID] = struct{}{}
			}
		}
	}
}

// buildRouteAgencyMap resolves route ID to agency ID with batched GetRoutesByIDs
// queries. Routes missing from static data are omitted, matching a per-row miss.
// A statement failure is returned so the caller can leave the previous feed in
// place instead of storing an empty one.
func (manager *Manager) buildRouteAgencyMap(ctx context.Context, routeIDSet map[string]struct{}) (map[string]string, error) {
	if len(routeIDSet) == 0 {
		return map[string]string{}, nil
	}
	if manager.GtfsDB == nil {
		return nil, errors.New("gtfs database is not initialized")
	}

	routeIDs := make([]string, 0, len(routeIDSet))
	for id := range routeIDSet {
		routeIDs = append(routeIDs, id)
	}
	slices.Sort(routeIDs)

	routes, err := utils.QueryInBatches(ctx, routeIDs, manager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		return nil, err
	}

	routeAgencyMap := make(map[string]string, len(routes))
	for _, route := range routes {
		routeAgencyMap[route.ID] = route.AgencyID
	}
	return routeAgencyMap, nil
}

// tripAgencyIDs resolves each trip's agency, keyed by trip ID, through its
// route. GTFS-RT route_id is optional, so a trip whose route ID is missing or
// unknown falls back to the route of the static trip with the same trip ID.
// Trips unknown by both IDs are left out.
func (manager *Manager) tripAgencyIDs(ctx context.Context, trips []gtfs.Trip, routeAgencyMap map[string]string) (map[string]string, error) {
	agencyByTripID := make(map[string]string, len(trips))
	var unresolvedTripIDs []string
	for _, trip := range trips {
		if agencyID, ok := routeAgencyMap[trip.ID.RouteID]; ok {
			agencyByTripID[trip.ID.ID] = agencyID
		} else {
			unresolvedTripIDs = append(unresolvedTripIDs, trip.ID.ID)
		}
	}
	if len(unresolvedTripIDs) == 0 {
		return agencyByTripID, nil
	}
	if manager.GtfsDB == nil {
		return nil, errors.New("gtfs database is not initialized")
	}

	staticTrips, err := utils.QueryInBatches(ctx, unresolvedTripIDs, manager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		return nil, err
	}
	staticRouteIDs := make(map[string]struct{}, len(staticTrips))
	for _, staticTrip := range staticTrips {
		staticRouteIDs[staticTrip.RouteID] = struct{}{}
	}
	staticRouteAgencies, err := manager.buildRouteAgencyMap(ctx, staticRouteIDs)
	if err != nil {
		return nil, err
	}
	for _, staticTrip := range staticTrips {
		if agencyID, ok := staticRouteAgencies[staticTrip.RouteID]; ok {
			agencyByTripID[staticTrip.ID] = agencyID
		}
	}
	return agencyByTripID, nil
}

// filterTripsByAgency returns the trips that belong to one of the allowed
// agencies, plus, separately, the trips that can't be resolved to any agency
// (absent from agencyByTripID; see tripAgencyIDs). Trips of other agencies
// are dropped from both.
func filterTripsByAgency(trips []gtfs.Trip, allowed map[string]bool, agencyByTripID map[string]string) (filtered, unattributed []gtfs.Trip) {
	filtered = make([]gtfs.Trip, 0, len(trips))
	for _, trip := range trips {
		agencyID, resolved := agencyByTripID[trip.ID.ID]
		if !resolved {
			unattributed = append(unattributed, trip)
			continue
		}
		if allowed[agencyID] {
			filtered = append(filtered, trip)
		}
	}
	return filtered, unattributed
}

// filterVehiclesByAgency returns only the vehicles whose trip's route belongs to
// one of the allowed agencies. Vehicles without a trip or unresolvable route are dropped.
func filterVehiclesByAgency(vehicles []gtfs.Vehicle, allowed map[string]bool, routeAgencyMap map[string]string) []gtfs.Vehicle {
	filtered := make([]gtfs.Vehicle, 0, len(vehicles))
	for _, v := range vehicles {
		if v.Trip == nil || v.Trip.ID.RouteID == "" {
			continue
		}
		if agencyID, ok := routeAgencyMap[v.Trip.ID.RouteID]; ok && allowed[agencyID] {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

// filterAlertsByAgency returns only alerts referencing an allowed agency.
func filterAlertsByAgency(alerts []gtfs.Alert, allowed map[string]bool, routeAgencyMap map[string]string) []gtfs.Alert {
	filtered := make([]gtfs.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alertMatchesAgency(alert, allowed, routeAgencyMap) {
			filtered = append(filtered, alert)
		}
	}
	return filtered
}

func alertMatchesAgency(alert gtfs.Alert, allowed map[string]bool, routeAgencyMap map[string]string) bool {
	// Stop-only InformedEntities are not resolved to agencies, so an alert
	// that names only stop IDs is dropped while agency filtering is active.
	for _, entity := range alert.InformedEntities {
		if informedEntityMatchesAgency(entity, allowed, routeAgencyMap) {
			return true
		}
	}
	return false
}

func informedEntityMatchesAgency(entity gtfs.AlertInformedEntity, allowed map[string]bool, routeAgencyMap map[string]string) bool {
	if entity.AgencyID != nil && allowed[*entity.AgencyID] {
		return true
	}
	if entity.RouteID != nil && routeBelongsToAllowedAgency(*entity.RouteID, allowed, routeAgencyMap) {
		return true
	}
	if entity.TripID != nil && routeBelongsToAllowedAgency(entity.TripID.RouteID, allowed, routeAgencyMap) {
		return true
	}
	return false
}

func routeBelongsToAllowedAgency(routeID string, allowed map[string]bool, routeAgencyMap map[string]string) bool {
	if routeID == "" {
		return false
	}
	agencyID, ok := routeAgencyMap[routeID]
	return ok && allowed[agencyID]
}

func (manager *Manager) rebuildMergedRealtimeLocked() {
	feedIDs := make([]string, 0, len(manager.feedTrips))
	totalTrips := 0
	for id, trips := range manager.feedTrips {
		feedIDs = append(feedIDs, id)
		totalTrips += len(trips)
	}
	slices.Sort(feedIDs)

	allTrips := make([]gtfs.Trip, 0, totalTrips)
	for _, id := range feedIDs {
		allTrips = append(allTrips, manager.feedTrips[id]...)
	}

	vehicleFeedIDs := make([]string, 0, len(manager.feedVehicles))
	totalVehicles := 0
	for id, vehicles := range manager.feedVehicles {
		vehicleFeedIDs = append(vehicleFeedIDs, id)
		totalVehicles += len(vehicles)
	}
	slices.Sort(vehicleFeedIDs)

	allVehicles := make([]gtfs.Vehicle, 0, totalVehicles)
	for _, id := range vehicleFeedIDs {
		allVehicles = append(allVehicles, manager.feedVehicles[id]...)
	}

	alertFeedIDs := make([]string, 0, len(manager.feedAlerts))
	for id := range manager.feedAlerts {
		alertFeedIDs = append(alertFeedIDs, id)
	}
	slices.Sort(alertFeedIDs)

	tripLookup := make(map[string]int, len(allTrips))
	for i, trip := range allTrips {
		if trip.ID.ID != "" {
			tripLookup[trip.ID.ID] = i
		}
	}

	vehicleLookupByTrip := make(map[string]int, len(allVehicles))
	vehicleLookupByVehicle := make(map[string]int, len(allVehicles))
	duplicatedVehicleByRoute := make(map[string][]gtfs.Vehicle)
	vehiclesByRoute := make(map[string][]gtfs.Vehicle)
	for i, vehicle := range allVehicles {
		if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			vehicleLookupByTrip[vehicle.Trip.ID.ID] = i
		}
		if vehicle.Trip != nil && vehicle.Trip.ID.RouteID != "" {
			vehiclesByRoute[vehicle.Trip.ID.RouteID] = append(vehiclesByRoute[vehicle.Trip.ID.RouteID], vehicle)
		}
		if vehicle.ID != nil && vehicle.ID.ID != "" {
			vehicleLookupByVehicle[vehicle.ID.ID] = i
		}
		if vehicle.Trip == nil || vehicle.Trip.ID.ScheduleRelationship != gtfsrt.TripDescriptor_DUPLICATED {
			continue
		}
		routeID := vehicle.Trip.ID.RouteID
		// Some feeds omit route_id in VehiclePosition trip descriptors.
		// Fall back to the corresponding TripUpdate to resolve the route.
		if routeID == "" && vehicle.Trip.ID.ID != "" {
			if index, exists := tripLookup[vehicle.Trip.ID.ID]; exists {
				routeID = allTrips[index].ID.RouteID
			}
		}
		if routeID != "" {
			duplicatedVehicleByRoute[routeID] = append(duplicatedVehicleByRoute[routeID], vehicle)
		}
	}
	idx := alertIndex{
		byTrip:   make(map[string][]gtfs.Alert),
		byRoute:  make(map[string][]gtfs.Alert),
		byAgency: make(map[string][]gtfs.Alert),
		byStop:   make(map[string][]gtfs.Alert),
	}
	for _, id := range alertFeedIDs {
		for _, alert := range manager.feedAlerts[id] {
			if alert.InformedEntities == nil || alert.ID == "" {
				continue
			}
			// Per-alert per-bucket dedup: prevents the same alert from being appended
			// to the same bucket more than once when it has multiple InformedEntities
			// referencing the same key (e.g. two entities both pointing to stop "S1").
			// Capacity is set to the entity count so the map never needs to grow.
			n := len(alert.InformedEntities)
			seenTrip := make(map[string]bool, n)
			seenRoute := make(map[string]bool, n)
			seenAgency := make(map[string]bool, n)
			seenStop := make(map[string]bool, n)
			for _, entity := range alert.InformedEntities {
				if entity.TripID != nil && !seenTrip[entity.TripID.ID] {
					seenTrip[entity.TripID.ID] = true
					idx.byTrip[entity.TripID.ID] = append(idx.byTrip[entity.TripID.ID], alert)
				}
				// Only match route-level entities that have no stop or trip restriction.
				// Entities with {routeId + stopId} are stop-specific alerts and are filed
				// in byStop only (matching Java's inverted index bucket behaviour).
				if entity.RouteID != nil && entity.StopID == nil && entity.TripID == nil && !seenRoute[*entity.RouteID] {
					seenRoute[*entity.RouteID] = true
					idx.byRoute[*entity.RouteID] = append(idx.byRoute[*entity.RouteID], alert)
				}
				// Only match agency-wide alerts: entity has agencyId but no route or trip restriction.
				if entity.AgencyID != nil && entity.RouteID == nil && entity.TripID == nil && !seenAgency[*entity.AgencyID] {
					seenAgency[*entity.AgencyID] = true
					idx.byAgency[*entity.AgencyID] = append(idx.byAgency[*entity.AgencyID], alert)
				}
				if entity.StopID != nil && !seenStop[*entity.StopID] {
					seenStop[*entity.StopID] = true
					idx.byStop[*entity.StopID] = append(idx.byStop[*entity.StopID], alert)
				}
			}
		}
	}

	manager.merged.Store(&mergedRealtime{
		trips:                    allTrips,
		vehicles:                 allVehicles,
		tripLookup:               tripLookup,
		vehicleLookupByTrip:      vehicleLookupByTrip,
		vehicleLookupByVehicle:   vehicleLookupByVehicle,
		duplicatedVehicleByRoute: duplicatedVehicleByRoute,
		vehiclesByRoute:          vehiclesByRoute,
		alerts:                   idx,
		exportVehicles:           manager.buildExportVehiclesLocked(vehicleFeedIDs),
	})
}

// calculateBackoff computes the next polling interval using exponential backoff with jitter
func calculateBackoff(baseInterval time.Duration, consecutiveErrors int, maxInterval time.Duration) time.Duration {
	// Cap the consecutive errors at 5 to prevent the multiplier from exceeding 32x
	// We use an if-statement here because a package-level float64 min() shadows the Go built-in min()
	exponent := consecutiveErrors
	if exponent > 5 {
		exponent = 5
	}

	// Exponential scale: 2, 4, 8, 16, 32
	backoffMultiplier := 1 << exponent
	nextInterval := time.Duration(float64(baseInterval) * float64(backoffMultiplier))
	if nextInterval > maxInterval {
		nextInterval = maxInterval
	}

	// +/- 10% Jitter prevents thundering herd behavior across failing feeds
	jitter := time.Duration((rand.Float64() - 0.5) * 0.2 * float64(nextInterval))
	return nextInterval + jitter
}

// pollFeed runs the polling loop for a single feed. Each feed gets its own
// goroutine with exponential backoff on errors, reporting to prometheus metrics.
func (manager *Manager) pollFeed(feedCfg RTFeedConfig) {
	defer manager.wg.Done()

	if feedCfg.RefreshInterval <= 0 {
		feedCfg.RefreshInterval = 30
	}

	logger := slog.Default().With(slog.String("component", "gtfs_realtime_updater"))
	baseInterval := time.Duration(feedCfg.RefreshInterval) * time.Second
	maxInterval := 5 * time.Minute

	consecutiveErrors := 0
	// Initialize to time.Now() to grant a 5-minute startup grace period before triggering staleness clearing
	lastSuccessfulFetch := time.Now()
	feedCleared := false // Track if data has already been cleared for this failure cycle

	logging.LogOperation(logger, "started_realtime_feed_poller",
		slog.String("feed", feedCfg.ID),
		slog.Float64("interval_ms", float64(baseInterval)/float64(time.Millisecond)),
		slog.String("tripUpdatesURL", feedCfg.TripUpdatesURL),
		slog.String("vehiclePositionsURL", feedCfg.VehiclePositionsURL),
		slog.String("serviceAlertsURL", feedCfg.ServiceAlertsURL),
	)

	// Use a Timer instead of Ticker to dynamically control intervals (backoff/jitter)
	timer := time.NewTimer(baseInterval) // Wait one interval before first poll (prevent double fetch)
	defer timer.Stop()

	for {
		select {
		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_realtime_feed_poller",
				slog.String("feed", feedCfg.ID))
			return
		case <-timer.C:
			func() {
				start := time.Now()

				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				ctx = logging.WithLogger(ctx, logger)

				logging.LogOperation(logger, "updating_gtfs_realtime_data",
					slog.String("feed", feedCfg.ID))

				hasNewData := manager.updateFeedRealtime(ctx, feedCfg)
				duration := time.Since(start)

				if manager.Metrics != nil {
					manager.Metrics.FeedFetchDuration.WithLabelValues(feedCfg.ID).Observe(duration.Seconds())
				}

				if hasNewData {
					consecutiveErrors = 0
					lastSuccessfulFetch = time.Now()
					feedCleared = false // Reset clearing flag on success

					if manager.Metrics != nil {
						manager.Metrics.FeedLastSuccessfulFetchTime.WithLabelValues(feedCfg.ID).Set(float64(lastSuccessfulFetch.Unix()))
						manager.Metrics.FeedConsecutiveErrors.WithLabelValues(feedCfg.ID).Set(0)
					}

					timer.Reset(baseInterval) // Reset to standard interval on success
				} else {
					consecutiveErrors++

					if manager.Metrics != nil {
						manager.Metrics.FeedConsecutiveErrors.WithLabelValues(feedCfg.ID).Set(float64(consecutiveErrors))
					}

					// Circuit Breaker / Staleness Protection
					if time.Since(lastSuccessfulFetch) > staleFeedThreshold {
						if !feedCleared { // Only clear once per extended outage
							logger.Warn("feed data is stale due to consecutive failures, clearing",
								slog.String("feed", feedCfg.ID),
								slog.Float64("staleness_ms", float64(time.Since(lastSuccessfulFetch))/float64(time.Millisecond)))
							manager.clearFeedData(feedCfg.ID)
							feedCleared = true
						}
					}

					// Use extracted, testable backoff function
					nextInterval := calculateBackoff(baseInterval, consecutiveErrors, maxInterval)

					logger.Warn("feed update failed, applying backoff",
						slog.String("feed", feedCfg.ID),
						slog.Int("consecutive_errors", consecutiveErrors),
						slog.Float64("next_interval_ms", float64(nextInterval)/float64(time.Millisecond)))

					timer.Reset(nextInterval)
				}
			}()
		}
	}
}

// GetAlertsForRoute returns alerts matching the given route ID.
func (manager *Manager) GetAlertsForRoute(routeID string) []gtfs.Alert {
	return manager.GetAlertsByIDs("", routeID, "")
}
