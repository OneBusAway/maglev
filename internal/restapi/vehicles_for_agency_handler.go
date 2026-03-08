package restapi

import (
	"net/http"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) vehiclesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := utils.GetIDFromContext(r.Context())

	ctx := r.Context()

	// Acquire static lock only for the agency lookup; release immediately.
	// VehiclesForAgencyID manages its own locking internally.
	api.GtfsManager.RLock()
	agency := api.GtfsManager.FindAgency(id)
	api.GtfsManager.RUnlock()

	if agency == nil {
		// return an empty list response.
		api.sendResponse(w, r, models.NewListResponse([]interface{}{}, models.ReferencesModel{}, false, api.Clock))
		return
	}

	vehiclesForAgency := api.GtfsManager.VehiclesForAgencyID(id)

	// Apply pagination
	offset, limit := utils.ParsePaginationParams(r)
	vehiclesForAgency, limitExceeded := utils.PaginateSlice(vehiclesForAgency, offset, limit)
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
	agencyRefs := make(map[string]models.AgencyReference)
	routeRefs := make(map[string]models.Route)
	tripRefs := make(map[string]models.Trip)

	for _, vehicle := range vehiclesForAgency {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		vehicleStatus := models.VehicleStatus{
			VehicleID: vehicle.ID.ID,
		}

		// Set timestamps
		if vehicle.Timestamp != nil {
			timestampMs := vehicle.Timestamp.UnixNano() / int64(time.Millisecond)
			vehicleStatus.LastLocationUpdateTime = &timestampMs
			vehicleStatus.LastUpdateTime = &timestampMs
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
			tripStatus := &models.TripStatus{
				ActiveTripID:      vehicle.Trip.ID.ID,
				BlockTripSequence: 0,
				Scheduled:         true,
				Phase:             vehicleStatus.Phase,
				Status:            vehicleStatus.Status,
			}

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
				tripStatus.Orientation = utils.Float64Ptr(float64(obaOrientation))
			}

			// Set service date (use current date for now)
			tripStatus.ServiceDate = api.Clock.NowUnixMilli()

			vehicleStatus.TripStatus = tripStatus

			// Add trip to references (basic trip reference)
			tripRefs[vehicle.Trip.ID.ID] = models.Trip{
				ID:      utils.FormCombinedID(id, vehicle.Trip.ID.ID),
				RouteID: utils.FormCombinedID(id, vehicle.Trip.ID.RouteID),
			}

			// Add route to references (from batch-fetched map)
			if route, ok := routeByID[vehicle.Trip.ID.RouteID]; ok {
				shortName := ""
				if route.ShortName.Valid {
					shortName = route.ShortName.String
				}
				longName := ""
				if route.LongName.Valid {
					longName = route.LongName.String
				}
				desc := ""
				if route.Desc.Valid {
					desc = route.Desc.String
				}
				url := ""
				if route.Url.Valid {
					url = route.Url.String
				}
				color := ""
				if route.Color.Valid {
					color = route.Color.String
				}
				textColor := ""
				if route.TextColor.Valid {
					textColor = route.TextColor.String
				}

				routeRefs[route.ID] = models.NewRoute(
					route.ID, route.AgencyID, shortName, longName,
					desc, models.RouteType(route.Type),
					url, color, textColor)

			}
		}

		vehiclesList = append(vehiclesList, vehicleStatus)
	}

	// Add agency to references
	agencyRefs[agency.Id] = models.NewAgencyReference(
		agency.Id, agency.Name, agency.Url, agency.Timezone,
		agency.Language, agency.Phone, agency.Email,
		agency.FareUrl, "", false,
	)

	// Convert maps to slices for references
	agencyRefList := make([]models.AgencyReference, 0, len(agencyRefs))
	for _, agencyRef := range agencyRefs {
		agencyRefList = append(agencyRefList, agencyRef)
	}

	routeRefList := make([]models.Route, 0, len(routeRefs))
	for _, routeRef := range routeRefs {
		routeRefList = append(routeRefList, routeRef)
	}

	tripRefList := make([]models.Trip, 0, len(tripRefs))
	for _, tripRef := range tripRefs {
		tripRefList = append(tripRefList, tripRef)
	}

	references := models.ReferencesModel{
		Agencies:   agencyRefList,
		Routes:     routeRefList,
		Situations: []models.Situation{},
		StopTimes:  []models.RouteStopTime{},
		Stops:      []models.Stop{},
		Trips:      tripRefList,
	}

	response := models.NewListResponse(vehiclesList, references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
