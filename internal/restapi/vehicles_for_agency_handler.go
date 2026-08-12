package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// vehiclesForAgencyHandler returns real-time vehicle positions for all vehicles operated by a given agency.
func (api *RestAPI) vehiclesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	// This endpoint builds a trip status per vehicle, so vehicles sharing a block
	// would otherwise recompute that block's snapshot and reload its trip data once
	// per vehicle. The plural arrivals handler installs the snapshot cache for the
	// same reason; the trip-data memo additionally covers the schedule-deviation
	// path, which resolves the same block without going through a snapshot.
	ctx := withRequestCache(WithSnapshotCache(r.Context(), newSnapshotCache()))

	agency, err := api.GtfsManager.FindAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	if agency == nil {
		// Unknown/untracked agency: empty list, outOfRange=false.
		api.sendResponse(w, r, models.NewListResponseWithRange([]any{}, *models.NewEmptyReferences(), false, api.Clock, false))
		return
	}

	// Parse requested reference time for status entries, falling back to server clock if absent.
	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	referenceTime := api.Clock.Now().In(loc)
	if timeParam := r.URL.Query().Get("time"); timeParam != "" {
		_, parsedTime, fieldErrors, ok := utils.ParseTimeParameter(timeParam, loc)
		if !ok {
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		referenceTime = parsedTime
	}

	// Service date is midnight of the reference date in the agency timezone.
	serviceDate := utils.CalculateServiceDate(referenceTime)

	vehiclesForAgency, err := api.GtfsManager.VehiclesForAgencyID(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// ageInSeconds: absent = no filter; any value >= 0 applies a strict cutoff.
	const maxAgeInSeconds = int64((1<<63 - 1) / int64(time.Second))
	if val := r.URL.Query().Get("ageInSeconds"); val != "" {
		if ageInSeconds, err := strconv.ParseInt(val, 10, 64); err == nil && ageInSeconds >= 0 && ageInSeconds <= maxAgeInSeconds {
			cutoff := referenceTime.Add(-time.Duration(ageInSeconds) * time.Second)
			filtered := vehiclesForAgency[:0]
			for _, vehicle := range vehiclesForAgency {
				if !api.GtfsManager.GetVehicleLastUpdateTime(&vehicle).Before(cutoff) {
					filtered = append(filtered, vehicle)
				}
			}
			vehiclesForAgency = filtered
		}
	}

	vehiclesList := make([]models.VehicleStatus, 0, len(vehiclesForAgency))

	// Collect unique route and trip IDs, then batch-fetch what every vehicle needs.
	routeIDSet := make(map[string]struct{})
	tripIDSet := make(map[string]struct{})
	for _, vehicle := range vehiclesForAgency {
		if vehicle.Trip != nil {
			routeIDSet[vehicle.Trip.ID.RouteID] = struct{}{}
			tripIDSet[vehicle.Trip.ID.ID] = struct{}{}
		}
	}
	routeIDs := make([]string, 0, len(routeIDSet))
	for routeID := range routeIDSet {
		routeIDs = append(routeIDs, routeID)
	}

	// Resolve every vehicle's stop times in one query, so the trip status built
	// for each one reads them from the request memo rather than issuing its own.
	tripIDs := make([]string, 0, len(tripIDSet))
	for tripID := range tripIDSet {
		tripIDs = append(tripIDs, tripID)
	}
	api.prefetchStopTimes(ctx, tripIDs)

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	routeByID := make(map[string]gtfsdb.Route, len(routes))
	for _, route := range routes {
		routeByID[route.ID] = route
	}

	// Maps to build references
	routeRefs := make(map[string]models.Route)
	tripRefs := make(map[string]models.Trip)

	for _, vehicle := range vehiclesForAgency {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		if vehicle.ID == nil {
			api.Logger.Warn("skipping vehicle with nil ID descriptor", "agencyID", id)
			continue
		}
		vid := vehicle.ID.ID
		vehicleStatus := models.VehicleStatus{
			VehicleID: vid,
		}

		// Update times default to 0 when no real update exists.
		if vehicle.Timestamp != nil {
			ts := models.NewModelTime(*vehicle.Timestamp)
			vehicleStatus.LastLocationUpdateTime = ts
			vehicleStatus.LastUpdateTime = ts
		}

		// Set location if available
		if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			vehicleStatus.Location = &models.Location{
				Lat: float64(*vehicle.Position.Latitude),
				Lon: float64(*vehicle.Position.Longitude),
			}
		}

		// Set status and phase based on current status
		vehicleStatus.Status, vehicleStatus.Phase = GetVehicleStatusAndPhase(&vehicle)

		// Build trip status if trip is available
		if vehicle.Trip != nil {
			vehicleStatus.TripID = utils.FormCombinedID(id, vehicle.Trip.ID.ID)

			// Resolve the executing trip; may differ from the nominal trip when interlining.
			activeTripID := api.resolveActiveTripID(ctx, vehicle.Trip.ID.ID, referenceTime)

			tripStatus := api.buildVehicleTripStatus(ctx, &vehicle, vehicleTripStatusParams{
				AgencyID:               id,
				ActiveTripID:           activeTripID,
				ServiceDate:            serviceDate,
				ReferenceTime:          referenceTime,
				Status:                 vehicleStatus.Status,
				Phase:                  vehicleStatus.Phase,
				LastUpdateTime:         vehicleStatus.LastUpdateTime,
				LastLocationUpdateTime: vehicleStatus.LastLocationUpdateTime,
			})

			// Propagate occupancy status from GTFS-RT to the vehicle-level field as well;
			// BuildTripStatus already sets it on the trip status.
			// There is no source for occupancyCapacity or occupancyCount anywhere in maglev — not in the SQLite DB,
			// not in GTFS-RT. Those fields will remain omitted.
			if vehicle.OccupancyStatus != nil {
				vehicleStatus.OccupancyStatus = vehicle.OccupancyStatus.String()
			}

			vehicleStatus.TripStatus = tripStatus

			// Add trip to references (basic trip reference)
			tripRefs[vehicle.Trip.ID.ID] = models.Trip{
				ID:      utils.FormCombinedID(id, vehicle.Trip.ID.ID),
				RouteID: utils.FormCombinedID(id, vehicle.Trip.ID.RouteID),
			}

			// Add the nominal trip's route to references (from batch-fetched map).
			if route, ok := routeByID[vehicle.Trip.ID.RouteID]; ok {
				addRouteReference(routeRefs, route)
			}

			// For interlining, also add the active trip and its route to references.
			if activeTripID != vehicle.Trip.ID.ID {
				if activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID); err == nil {
					tripRefs[activeTripID] = models.Trip{
						ID:      utils.FormCombinedID(id, activeTripID),
						RouteID: utils.FormCombinedID(id, activeTrip.RouteID),
					}
					activeRoute, ok := routeByID[activeTrip.RouteID]
					if !ok {
						if fetched, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID); err == nil {
							activeRoute, ok = fetched, true
						}
					}
					if ok {
						addRouteReference(routeRefs, activeRoute)
					}
				}
			}
		} else {
			defaultTripStatus := models.NewTripStatus()
			defaultTripStatus.Status = "default"
			defaultTripStatus.Phase = "scheduled"
			vehicleStatus.TripStatus = defaultTripStatus
		}

		vehiclesList = append(vehiclesList, vehicleStatus)
	}

	// Convert maps to slices for references
	routeRefList := make([]models.Route, 0, len(routeRefs))
	for _, routeRef := range routeRefs {
		routeRefList = append(routeRefList, routeRef)
	}

	tripRefList := make([]models.Trip, 0, len(tripRefs))
	for _, tripRef := range tripRefs {
		tripRefList = append(tripRefList, tripRef)
	}

	// Omit references entirely when includeReferences=false.
	references := models.NewEmptyReferences()
	if ShouldIncludeReferences(r) {
		references.Agencies = []models.AgencyReference{models.AgencyReferenceFromDatabase(agency)}
		references.Routes = routeRefList
		references.Trips = tripRefList

		alerts := deduplicateAlerts(
			api.collectAlertsForRoutes(routeIDs),
			api.GtfsManager.GetAlertsByIDs("", "", id),
		)
		references.Situations = append(references.Situations, api.BuildSituationReferences(alerts)...)
	}

	// Spec: this endpoint returns all matching vehicles, so limitExceeded is always false.
	response := models.NewListResponse(vehiclesList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}

// vehicleTripStatusParams carries the per-request values buildVehicleTripStatus needs
// alongside the vehicle itself.
type vehicleTripStatusParams struct {
	AgencyID string
	// ActiveTripID is the trip actually executing at ReferenceTime, which differs
	// from the vehicle's nominal trip when interlining is in play.
	ActiveTripID  string
	ServiceDate   time.Time
	ReferenceTime time.Time
	// Status, Phase and the update times are the enclosing vehicleStatus values,
	// mirrored onto the trip status so the two never disagree within a single list
	// entry. BuildTripStatus derives its own equivalents from vehicle freshness and
	// position, which would otherwise make the nested object contradict the outer one.
	Status                 string
	Phase                  string
	LastUpdateTime         models.ModelTime
	LastLocationUpdateTime models.ModelTime
}

// buildVehicleTripStatus builds the tripStatus for one vehicles-for-agency entry.
//
// It delegates to the shared api.BuildTripStatus so this endpoint reports the same
// stop-relative, schedule-deviation, distance-along-trip, predicted and situation
// fields as the other real-time endpoints, then re-applies the two behaviours that
// are specific to this endpoint: the active trip is resolved against the request's
// reference time, and an unresolvable block sequence is reported as -1 rather than 0.
func (api *RestAPI) buildVehicleTripStatus(ctx context.Context, vehicle *gtfs.Vehicle, params vehicleTripStatusParams) *models.TripStatus {
	tripStatus, _, err := api.BuildTripStatus(
		ctx, params.AgencyID, vehicle.Trip.ID.ID, vehicle, params.ServiceDate, params.ReferenceTime,
	)
	if err != nil {
		api.Logger.Warn("vehicles-for-agency: failed to build trip status",
			"tripID", vehicle.Trip.ID.ID, "error", err)
	}
	if tripStatus == nil {
		tripStatus = models.NewTripStatus()
	}

	tripStatus.ActiveTripID = utils.FormCombinedID(params.AgencyID, params.ActiveTripID)

	// Resolve the block trip sequence for the active (not nominal) trip, so it
	// reflects the position of the trip actually being executed.
	if seq, ok := api.blockTripSequence(ctx, params.ActiveTripID, params.ReferenceTime); ok {
		tripStatus.BlockTripSequence = seq
	} else {
		tripStatus.BlockTripSequence = -1
	}

	tripStatus.Status = params.Status
	tripStatus.Phase = params.Phase
	tripStatus.LastUpdateTime = params.LastUpdateTime
	tripStatus.LastLocationUpdateTime = params.LastLocationUpdateTime
	applyRawVehiclePosition(tripStatus, vehicle)

	return tripStatus
}

// applyRawVehiclePosition fills position and orientation from the vehicle's own
// GPS when BuildTripStatus left them unset.
//
// BuildTripStatus reports nothing for a vehicle it considers stale, and a
// requested `time` more than the staleness window away from the feed timestamp
// makes every vehicle stale. Without this the entry would carry a real
// vehicle-level location next to a tripStatus sitting at (0, 0). This endpoint
// has always published the raw position regardless of freshness.
func applyRawVehiclePosition(tripStatus *models.TripStatus, vehicle *gtfs.Vehicle) {
	if vehicle.Position == nil {
		return
	}

	hasCoordinates := vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil
	if hasCoordinates && tripStatus.Position == (models.Location{}) {
		tripStatus.Position = models.Location{
			Lat: float64(*vehicle.Position.Latitude),
			Lon: float64(*vehicle.Position.Longitude),
		}
	}

	if vehicle.Position.Bearing != nil && tripStatus.Orientation == 0 {
		tripStatus.Orientation = OrientationFromGTFSBearing(*vehicle.Position.Bearing)
	}
}

// addRouteReference inserts a route reference keyed by its combined agencyID_routeID.
func addRouteReference(routeRefs map[string]models.Route, route gtfsdb.Route) {
	combinedRouteID := utils.FormCombinedID(route.AgencyID, route.ID)
	routeRefs[combinedRouteID] = models.NewRoute(
		combinedRouteID, route.AgencyID,
		nulls.StringOrEmpty(route.ShortName),
		nulls.StringOrEmpty(route.LongName),
		nulls.StringOrEmpty(route.Desc),
		models.RouteType(route.Type),
		nulls.StringOrEmpty(route.Url),
		nulls.StringOrEmpty(route.Color),
		nulls.StringOrEmpty(route.TextColor),
	)
}
