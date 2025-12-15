package gtfs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
)

// GetRealTimeTrips returns the real-time trip updates
func (manager *Manager) GetRealTimeTrips() []gtfs.Trip {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	return manager.realTimeTrips
}

// GetRealTimeVehicles returns the real-time vehicle positions
func (manager *Manager) GetRealTimeVehicles() []gtfs.Vehicle {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	return manager.realTimeVehicles
}

func loadRealtimeData(ctx context.Context, source string, headers map[string]string) (*gtfs.Realtime, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Add(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer logging.SafeCloseWithLogging(resp.Body,
		slog.Default().With(slog.String("component", "gtfs_realtime_downloader")),
		"http_response_body")

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return gtfs.ParseRealtime(b, &gtfs.ParseRealtimeOptions{})
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

func (manager *Manager) GetAlertsForTrip(tripID string) []gtfs.Alert {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	var routeID string
	var agencyID string
	trip, err := manager.GtfsDB.Queries.GetTrip(context.Background(), tripID)
	if err != nil {
		return nil
	}

	routeID = trip.RouteID
	route, err := manager.GtfsDB.Queries.GetRoute(context.Background(), routeID)
	if err == nil {
		agencyID = route.AgencyID
	}

	alertMap := make(map[string]gtfs.Alert)

	for _, alert := range manager.realTimeAlerts {
		if alert.InformedEntities != nil {
			for _, entity := range alert.InformedEntities {
				if entity.TripID != nil && entity.TripID.ID == tripID {
					alertMap[alert.ID] = alert
					break
				}

				if entity.RouteID != nil && routeID != "" && *entity.RouteID == routeID {
					alertMap[alert.ID] = alert
					break
				}

				if entity.AgencyID != nil && agencyID != "" && *entity.AgencyID == agencyID {
					alertMap[alert.ID] = alert
					break
				}
			}
		}
	}

	alerts := make([]gtfs.Alert, 0, len(alertMap))
	for _, alert := range alertMap {
		alerts = append(alerts, alert)
	}

	return alerts
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

func (manager *Manager) updateGTFSRealtime(ctx context.Context, config Config) {
	logger := logging.FromContext(ctx).With(slog.String("component", "gtfs_realtime"))

	headers := map[string]string{}
	if config.RealTimeAuthHeaderKey != "" && config.RealTimeAuthHeaderValue != "" {
		headers[config.RealTimeAuthHeaderKey] = config.RealTimeAuthHeaderValue
	}

	var wg sync.WaitGroup
	var tripData, vehicleData, alertData *gtfs.Realtime
	var tripErr, vehicleErr, alertErr error

	// Fetch trip updates in parallel
	wg.Add(1)
	go func() {
		defer wg.Done()
		tripData, tripErr = loadRealtimeData(ctx, config.TripUpdatesURL, headers)
		if tripErr != nil {
			logging.LogError(logger, "Error loading GTFS-RT trip updates data", tripErr,
				slog.String("url", config.TripUpdatesURL))
		}
	}()

	// Fetch vehicle positions in parallel
	wg.Add(1)
	go func() {
		defer wg.Done()
		vehicleData, vehicleErr = loadRealtimeData(ctx, config.VehiclePositionsURL, headers)
		if vehicleErr != nil {
			logging.LogError(logger, "Error loading GTFS-RT vehicle positions data", vehicleErr,
				slog.String("url", config.VehiclePositionsURL))
		}
	}()

	if config.ServiceAlertsURL != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			alertData, alertErr = loadRealtimeData(ctx, config.ServiceAlertsURL, headers)
			if alertErr != nil {
				logging.LogError(logger, "Error loading GTFS-RT service alerts data", alertErr,
					slog.String("url", config.ServiceAlertsURL))
			}
		}()
	}

	// Wait for both to complete
	wg.Wait()

	// Check for context cancellation
	if ctx.Err() != nil {
		return
	}

	// Update data if at least one fetch succeeded
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()

	if tripData != nil && tripErr == nil {
		manager.realTimeTrips = tripData.Trips
		rebuildRealTimeTripLookup(manager)

	}
	if vehicleData != nil && vehicleErr == nil {
		manager.realTimeVehicles = vehicleData.Vehicles
		rebuildRealTimeVehicleLookupByTrip(manager)
		rebuildRealTimeVehicleLookupByVehicle(manager)
	}

	if alertData != nil && alertErr == nil {
		manager.realTimeAlerts = alertData.Alerts
	} else if alertErr != nil {
		logging.LogError(logger, "Error loading GTFS-RT service alerts", alertErr)
	}
}

func rebuildRealTimeTripLookup(manager *Manager) {
	if manager.realTimeTripLookup == nil {
		manager.realTimeTripLookup = make(map[string]int)
	} else {
		for k := range manager.realTimeTripLookup {
			delete(manager.realTimeTripLookup, k)
		}
	}
	for i, trip := range manager.realTimeTrips {
		manager.realTimeTripLookup[trip.ID.ID] = i
	}
}

func rebuildRealTimeVehicleLookupByTrip(manager *Manager) {
	if manager.realTimeVehicleLookupByTrip == nil {
		manager.realTimeVehicleLookupByTrip = make(map[string]int)
	} else {
		for k := range manager.realTimeVehicleLookupByTrip {
			delete(manager.realTimeVehicleLookupByTrip, k)
		}
	}
	for i, vehicle := range manager.realTimeVehicles {
		if vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			manager.realTimeVehicleLookupByTrip[vehicle.Trip.ID.ID] = i
		}
	}
}

func rebuildRealTimeVehicleLookupByVehicle(manager *Manager) {
	if manager.realTimeVehicleLookupByVehicle == nil {
		manager.realTimeVehicleLookupByVehicle = make(map[string]int)
	} else {
		for k := range manager.realTimeVehicleLookupByVehicle {
			delete(manager.realTimeVehicleLookupByVehicle, k)
		}
	}
	for i, vehicle := range manager.realTimeVehicles {
		manager.realTimeVehicleLookupByVehicle[vehicle.ID.ID] = i
	}
}

func (manager *Manager) updateGTFSRealtimePeriodically(config Config) {
	defer manager.wg.Done()

	// Create a logger for this goroutine
	logger := slog.Default().With(slog.String("component", "gtfs_realtime_updater"))

	// Update every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for { // nolint
		select {
		case <-ticker.C:
			// Create a context with timeout for the download
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			ctx = logging.WithLogger(ctx, logger)

			// Download realtime data
			logging.LogOperation(logger, "updating_gtfs_realtime_data")
			manager.updateGTFSRealtime(ctx, config)
			cancel() // Ensure the context is canceled when done
		case <-manager.shutdownChan:
			logging.LogOperation(logger, "shutting_down_realtime_updates")
			return
		}
	}
}
