package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// vehiclesForAgencyHandler returns real-time vehicle positions for all vehicles operated by a given agency.
func (api *RestAPI) vehiclesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	reqLogger := logging.ForComponent(r.Context(), "http_server")
	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	// Vehicles frequently share a block, and BuildTripStatus resolves the whole
	// block each time it runs. The snapshot cache lets every vehicle on a block pay
	// for that once per request instead of once per vehicle.
	ctx := WithSnapshotCache(r.Context(), newSnapshotCache())

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
		_, parsedTime, fieldErrors, ok := utils.ParseTimeParameter(timeParam, loc, api.Clock)
		if !ok {
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		referenceTime = parsedTime
	}

	// Service date is midnight of the reference date in the agency timezone.
	serviceDate := models.NewModelTime(utils.CalculateServiceDate(referenceTime))

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

	// Collect unique route IDs and batch-fetch routes
	routeIDSet := make(map[string]struct{})
	for _, vehicle := range vehiclesForAgency {
		if vehicle.Trip != nil {
			routeIDSet[vehicle.Trip.ID.RouteID] = struct{}{}
		}
	}
	routeIDs := make([]string, 0, len(routeIDSet))
	for routeID := range routeIDSet {
		routeIDs = append(routeIDs, routeID)
	}
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
	situations := newSituationCollector()

	// Stops named by each entry's closestStop and nextStop. #1195 requires them in
	// references.stops, otherwise those IDs point at nothing.
	var stopIDs []string

	// serviceDate above is the response-shaped value; BuildTripStatus wants the
	// underlying time.
	serviceDateTime := utils.CalculateServiceDate(referenceTime)

	// Populated lazily by frequencyForEntry, so trips shared by several vehicles
	// are queried once per request rather than once per vehicle.
	freqMap := make(map[string][]gtfsdb.Frequency)

	for _, vehicle := range vehiclesForAgency {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		if vehicle.ID == nil {
			reqLogger.Warn("skipping vehicle with nil ID descriptor", "agencyID", id)
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

			tripStatus := models.NewTripStatus()
			// Resolve the executing trip; may differ from the nominal trip when interlining.
			activeTripID := api.resolveActiveTripID(ctx, vehicle.Trip.ID.ID, referenceTime)
			tripStatus.ActiveTripID = utils.FormCombinedID(id, activeTripID)
			// Resolve the block trip sequence for the active (not nominal) trip,
			// so it reflects the position of the trip actually being executed.
			if seq, ok := api.blockTripSequence(ctx, activeTripID, referenceTime); ok {
				tripStatus.BlockTripSequence = seq
			} else {
				tripStatus.BlockTripSequence = -1
			}
			tripStatus.Phase = vehicleStatus.Phase
			tripStatus.Status = vehicleStatus.Status

			// Add position information to trip status
			if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
				tripStatus.Position = models.Location{
					Lat: float64(*vehicle.Position.Latitude),
					Lon: float64(*vehicle.Position.Longitude),
				}
			}

			// Add orientation if available (convert from GTFS bearing to OBA orientation)
			if vehicle.Position != nil && vehicle.Position.Bearing != nil {
				// Convert from GTFS bearing (0° = North, 90° = East) to OBA orientation (0° = East, 90° = North)
				// OBA orientation = (90 - GTFS bearing) mod 360
				obaOrientation := (90 - *vehicle.Position.Bearing)
				if obaOrientation < 0 {
					obaOrientation += 360
				}
				tripStatus.Orientation = float64(obaOrientation)
			}

			// Trip status update times default to 0 when no real update exists.
			if vehicle.Timestamp != nil {
				ts := models.NewModelTime(*vehicle.Timestamp)
				tripStatus.LastUpdateTime = ts
				tripStatus.LastLocationUpdateTime = ts
			}

			tripStatus.ServiceDate = serviceDate

			// Propagate occupancy status from GTFS-RT to both TripStatus and VehicleStatus.
			// There is no source for occupancyCapacity or occupancyCount anywhere in maglev — not in the SQLite DB,
			// not in GTFS-RT. Those fields will remain omitted.
			if vehicle.OccupancyStatus != nil {
				occupancy := vehicle.OccupancyStatus.String()
				tripStatus.OccupancyStatus = occupancy
				vehicleStatus.OccupancyStatus = occupancy
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

			// An interlined vehicle executes a trip that can belong to another
			// route, and that route is only known once the trip below resolves.
			activeRouteID := vehicle.Trip.ID.RouteID

			// For interlining, also add the active trip and its route to references.
			if activeTripID != vehicle.Trip.ID.ID {
				if activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripID); err == nil {
					activeRouteID = activeTrip.RouteID
					tripRefs[activeTripID] = models.Trip{
						ID:      utils.FormCombinedID(id, activeTripID),
						RouteID: utils.FormCombinedID(id, activeTrip.RouteID),
					}
					activeRoute, ok := routeByID[activeTrip.RouteID]
					if !ok {
						if fetched, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, activeTrip.RouteID); err == nil {
							activeRoute, ok = fetched, true
							// Cache it so the situation lookup below stays free of queries.
							routeByID[activeTrip.RouteID] = fetched
						}
					}
					if ok {
						addRouteReference(routeRefs, activeRoute)
					}
				}
			}

			tripStatus.SituationIDs = situations.addRefs(api.vehicleSituationRefs(ctx,
				tripRouteRef{tripID: activeTripID, routeID: activeRouteID},
				tripRouteRef{tripID: vehicle.Trip.ID.ID, routeID: vehicle.Trip.ID.RouteID},
				routeByID,
			))

			// activeTripID, not the nominal trip: the distance and stop fields have
			// to describe the trip whose ID this entry reports as activeTripId, or an
			// interlined vehicle would carry one trip's ID beside another's distances.
			api.fillTripStatusFromSharedBuilder(ctx, tripStatus, id, activeTripID, &vehicle,
				serviceDateTime, referenceTime, freqMap)

			statusStopIDs, err := referencedStopIDs(tripStatus, nil)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, statusStopIDs...)
		} else {
			defaultTripStatus := models.NewTripStatus()
			defaultTripStatus.Status = "default"
			defaultTripStatus.Phase = "scheduled"
			vehicleStatus.TripStatus = defaultTripStatus
		}

		vehiclesList = append(vehiclesList, vehicleStatus)
	}

	// Convert maps to slices for references
	tripRefList := make([]models.Trip, 0, len(tripRefs))
	for _, tripRef := range tripRefs {
		tripRefList = append(tripRefList, tripRef)
	}

	// Omit references entirely when includeReferences=false.
	references := models.NewEmptyReferences()
	if ShouldIncludeReferences(r) {
		if len(vehiclesList) > 0 {
			references.Agencies = []models.AgencyReference{models.AgencyReferenceFromDatabase(agency)}
		}

		stops, stopRoutes, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, id, stopIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Stops = stops
		// A stop reference lists the routes serving it, so those routes belong here
		// too or the stop entries point at routes that are themselves absent.
		for combinedRouteID, stopRoute := range stopRoutes {
			if _, exists := routeRefs[combinedRouteID]; !exists {
				routeRefs[combinedRouteID] = routeReferenceFromStopRow(stopRoute)
			}
		}

		// Built after the merge above so the stops' routes are included.
		routeRefList := make([]models.Route, 0, len(routeRefs))
		for _, routeRef := range routeRefs {
			routeRefList = append(routeRefList, routeRef)
		}

		references.Routes = routeRefList
		references.Trips = tripRefList
		references.Situations = api.situationReferences(ctx, situations.refs)
	}

	// Spec: this endpoint returns all matching vehicles, so limitExceeded is always false.
	const outOfRange, limitExceeded = false, false
	response := models.NewListResponseWithRange(vehiclesList, *references, outOfRange, api.Clock, limitExceeded)
	api.sendResponse(w, r, response)
}

// fillTripStatusFromSharedBuilder populates the tripStatus fields this handler
// does not compute itself: the stop-relative fields, the distance fields,
// scheduleDeviation and predicted. Every other real-time endpoint gets them from
// BuildTripStatus; this one carried a reduced copy of the logic and left them at
// their zero values (#1195).
//
// Only those fields are copied over. This endpoint resolves activeTripId,
// blockTripSequence, status, phase, position, orientation and the update times
// itself, and several of them deliberately differ from what the builder produces:
// blockTripSequence reports -1 rather than 0 when it cannot be resolved, and
// BuildVehicleStatus returns early for a vehicle it judges stale, which would
// leave position at (0, 0) beside a real location at the top level. Copying the
// whole status and then undoing the differences would leave this endpoint's
// behaviour at the mercy of later changes to the builder, so the narrow direction
// is deliberate.
//
// A failure leaves the fields at the zero values the endpoint emitted before, so
// the entry degrades to its old shape instead of the request failing.
func (api *RestAPI) fillTripStatusFromSharedBuilder(
	ctx context.Context,
	status *models.TripStatus,
	agencyID, tripID string,
	vehicle *gtfs.Vehicle,
	serviceDate, currentTime time.Time,
	freqMap map[string][]gtfsdb.Frequency,
) {
	built, _, err := api.BuildTripStatus(ctx, agencyID, tripID, vehicle, serviceDate, currentTime, freqMap)
	if err != nil || built == nil {
		logging.ForComponent(ctx, "http_server").Warn("BuildTripStatus failed, leaving tripStatus spec fields unset",
			"tripID", tripID, "error", err)
		return
	}

	status.ClosestStop = built.ClosestStop
	status.ClosestStopTimeOffset = built.ClosestStopTimeOffset
	status.NextStop = built.NextStop
	status.NextStopTimeOffset = built.NextStopTimeOffset
	status.ScheduleDeviation = built.ScheduleDeviation
	status.ScheduledDistanceAlongTrip = built.ScheduledDistanceAlongTrip
	status.TotalDistanceAlongTrip = built.TotalDistanceAlongTrip
	status.DistanceAlongTrip = built.DistanceAlongTrip
	// SetPredicted keeps Scheduled as its inverse.
	status.SetPredicted(built.Predicted)
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
