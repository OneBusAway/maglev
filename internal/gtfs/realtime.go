package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
)

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

// staleVehicleTimeout is the duration after which a vehicle is considered stale
const staleVehicleTimeout = 15 * time.Minute

// isVehicleStale returns true if the incoming vehicle update is older
// than the existing vehicle based on GTFS-RT timestamps.
func isVehicleStale(existing, incoming gtfs.Vehicle) bool {
	if existing.Timestamp == nil || incoming.Timestamp == nil {
		// If either timestamp is missing, we cannot safely compare
		return false
	}
	return incoming.Timestamp.Before(*existing.Timestamp)
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
		if _, ok := lastSeenMap[v.ID.ID]; ok {
			validVehicles = append(validVehicles, v)
		}
	}
	manager.feedVehicles[feedID] = validVehicles
}

func (manager *Manager) GetRealTimeTrips() []gtfs.Trip {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	return manager.realTimeTrips
}

func (manager *Manager) GetRealTimeVehicles() []gtfs.Vehicle {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	return manager.realTimeVehicles
}

func (manager *Manager) GetAlertsForRoute(routeID string) []gtfs.Alert {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	var alerts []gtfs.Alert
	for _, alert := range manager.realTimeAlerts {
		if alert.InformedEntities != nil {
			for _, entity := range alert.InformedEntities {
				if entity.RouteID != nil && *entity.RouteID == routeID {
					alerts = append(alerts, alert)
					break
				}
			}
		}
	}
	return alerts
}

// It acquires the realTimeMutex internally; callers must NOT hold it.
func (manager *Manager) GetAlertsByIDs(tripID, routeID, agencyID string) []gtfs.Alert {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	var alerts []gtfs.Alert
	for _, alert := range manager.realTimeAlerts {
		if alert.InformedEntities == nil {
			continue
		}
		for _, entity := range alert.InformedEntities {
			if entity.TripID != nil && tripID != "" && entity.TripID.ID == tripID {
				alerts = append(alerts, alert)
				break
			}
			if entity.RouteID != nil && routeID != "" && *entity.RouteID == routeID {
				alerts = append(alerts, alert)
				break
			}
			if entity.AgencyID != nil && agencyID != "" && *entity.AgencyID == agencyID {
				alerts = append(alerts, alert)
				break
			}
		}
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

func (manager *Manager) GetAlertsForStop(stopID string) []gtfs.Alert {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	var alerts []gtfs.Alert
	for _, alert := range manager.realTimeAlerts {
		if alert.InformedEntities != nil {
			for _, entity := range alert.InformedEntities {
				if entity.StopID != nil && *entity.StopID == stopID {
					alerts = append(alerts, alert)
					break
				}
			}
		}
	}
	return alerts
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
func (manager *Manager) updateFeedRealtime(ctx context.Context, feedCfg RTFeedConfig) {
	logger := logging.FromContext(ctx).With(slog.String("component", "gtfs_realtime"))
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
		return
	}

	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	if tripData != nil && tripErr == nil {
		manager.feedTrips[feedID] = tripData.Trips
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
			prevByID := make(map[string]gtfs.Vehicle, len(prevVehicles))
			for _, pv := range prevVehicles {
				if pv.ID != nil {
					prevByID[pv.ID.ID] = pv
				}
			}

			validVehicles := make([]gtfs.Vehicle, 0, len(vehicleData.Vehicles))
			for _, v := range vehicleData.Vehicles {
				if v.ID == nil {
					continue
				}

				if prev, exists := prevByID[v.ID.ID]; exists {
					if isVehicleStale(prev, v) {
						// Keep newer existing vehicle, drop stale update
						validVehicles = append(validVehicles, prev)
						continue
					}
				}

				validVehicles = append(validVehicles, v)
			}

			now := time.Now()
			if manager.feedVehicleLastSeen[feedID] == nil {
				manager.feedVehicleLastSeen[feedID] = make(map[string]time.Time)
			}
			lastSeenMap := manager.feedVehicleLastSeen[feedID]

			currentVehicleIDs := make(map[string]struct{}, len(validVehicles))
			for _, v := range validVehicles {
				lastSeenMap[v.ID.ID] = now
				currentVehicleIDs[v.ID.ID] = struct{}{}
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
				if _, current := currentVehicleIDs[pv.ID.ID]; !current {
					if lastSeen, ok := lastSeenMap[pv.ID.ID]; ok && now.Sub(lastSeen) <= staleVehicleTimeout {
						validVehicles = append(validVehicles, pv)
					}
				}
			}

			manager.feedVehicles[feedID] = validVehicles
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

	hadDataBefore := len(manager.feedTrips[feedID]) > 0 || len(manager.feedVehicles[feedID]) > 0 || len(manager.feedAlerts[feedID]) > 0
	hasNewData := tripsUpdated || vehiclesUpdated || alertsUpdated

	if !hasNewData {
		if hadDataBefore {
			logger.Warn("all realtime feed sources failed - retaining stale data",
				slog.String("feed", feedID),
				slog.Bool("trip_updates_error", tripErr != nil),
				slog.Bool("vehicle_positions_error", vehicleErr != nil),
				slog.Bool("service_alerts_error", alertErr != nil),
			)
		} else {
			logger.Error("all realtime feed sources failed - no data available",
				slog.String("feed", feedID),
				slog.Bool("trip_updates_error", tripErr != nil),
				slog.Bool("vehicle_positions_error", vehicleErr != nil),
				slog.Bool("service_alerts_error", alertErr != nil),
			)
		}
	} else {
		logger.Info("updated realtime feed",
			slog.String("feed", feedID),
			slog.Int("trips", len(manager.feedTrips[feedID])),
			slog.Int("vehicles", len(manager.feedVehicles[feedID])),
			slog.Int("alerts", len(manager.feedAlerts[feedID])),
		)
	}

	manager.rebuildMergedRealtimeLocked()
}

func (manager *Manager) rebuildMergedRealtimeLocked() {
	feedIDs := make([]string, 0, len(manager.feedTrips))
	for id := range manager.feedTrips {
		feedIDs = append(feedIDs, id)
	}
	sort.Strings(feedIDs)

	var allTrips []gtfs.Trip
	for _, id := range feedIDs {
		allTrips = append(allTrips, manager.feedTrips[id]...)
	}

	vehicleFeedIDs := make([]string, 0, len(manager.feedVehicles))
	for id := range manager.feedVehicles {
		vehicleFeedIDs = append(vehicleFeedIDs, id)
	}
	sort.Strings(vehicleFeedIDs)

	var allVehicles []gtfs.Vehicle
	for _, id := range vehicleFeedIDs {
		allVehicles = append(allVehicles, manager.feedVehicles[id]...)
	}

	alertFeedIDs := make([]string, 0, len(manager.feedAlerts))
	for id := range manager.feedAlerts {
		alertFeedIDs = append(alertFeedIDs, id)
	}
	sort.Strings(alertFeedIDs)

	var allAlerts []gtfs.Alert
	for _, id := range alertFeedIDs {
		allAlerts = append(allAlerts, manager.feedAlerts[id]...)
	}

	tripLookup := make(map[string]int, len(allTrips))
	for i, trip := range allTrips {
		if trip.ID.ID != "" {
			tripLookup[trip.ID.ID] = i
		}
	}

	vehicleLookupByTrip := make(map[string]int, len(allVehicles))
	vehicleLookupByVehicle := make(map[string]int, len(allVehicles))
	for i, vehicle := range allVehicles {
		if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			vehicleLookupByTrip[vehicle.Trip.ID.ID] = i
		}
		if vehicle.ID != nil && vehicle.ID.ID != "" {
			vehicleLookupByVehicle[vehicle.ID.ID] = i
		}
	}

	manager.realTimeTrips = allTrips
	manager.realTimeVehicles = allVehicles
	manager.realTimeAlerts = allAlerts
	manager.realTimeTripLookup = tripLookup
	manager.realTimeVehicleLookupByTrip = vehicleLookupByTrip
	manager.realTimeVehicleLookupByVehicle = vehicleLookupByVehicle
}

// pollFeed runs the polling loop for a single feed. Each feed gets its own
// goroutine with its own ticker at the feed's configured refresh interval.
func (manager *Manager) pollFeed(feedCfg RTFeedConfig) {
	defer manager.wg.Done()

	if feedCfg.RefreshInterval <= 0 {
		feedCfg.RefreshInterval = 30
	}

	logger := slog.Default().With(slog.String("component", "gtfs_realtime_updater"))
	interval := time.Duration(feedCfg.RefreshInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logging.LogOperation(logger, "started_realtime_feed_poller",
		slog.String("feed", feedCfg.ID),
		slog.Duration("interval", interval),
		slog.String("tripUpdatesURL", feedCfg.TripUpdatesURL),
		slog.String("vehiclePositionsURL", feedCfg.VehiclePositionsURL),
		slog.String("serviceAlertsURL", feedCfg.ServiceAlertsURL),
	)

	for {
		select {
		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_realtime_feed_poller",
				slog.String("feed", feedCfg.ID))
			return
		case <-ticker.C:
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				ctx = logging.WithLogger(ctx, logger)

				logging.LogOperation(logger, "updating_gtfs_realtime_data",
					slog.String("feed", feedCfg.ID))
				manager.updateFeedRealtime(ctx, feedCfg)
			}()
		}
	}
}
