package restapi

import (
	"net/http"
	"strconv"
	"time"

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

	ctx := r.Context()

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
		currentTime := models.NewModelTime(referenceTime)
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
			// Resolve the block trip sequence; -1 when unavailable.
			if seq, ok := api.blockTripSequence(ctx, vehicle.Trip.ID.ID, referenceTime); ok {
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

			// Set service date (use current date for now)
			tripStatus.ServiceDate = currentTime

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
