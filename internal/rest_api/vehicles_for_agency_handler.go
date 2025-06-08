package restapi

import (
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"net/http"
	"time"
)

func (api *RestAPI) vehiclesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id := utils.ExtractIDFromParams(r)

	agency := api.GtfsManager.FindAgency(id)
	if agency == nil {
		// return an empty list response.
		api.sendResponse(w, r, models.NewListResponse([]interface{}{}, models.ReferencesModel{}))
		return
	}

	vehiclesForAgency := api.GtfsManager.VehiclesForAgencyID(id)
	vehiclesList := make([]models.VehicleStatus, 0, len(vehiclesForAgency))

	// Maps to build references
	agencyRefs := make(map[string]models.AgencyReference)
	routeRefs := make(map[string]models.Route)
	tripRefs := make(map[string]interface{})

	for _, vehicle := range vehiclesForAgency {
		vehicleStatus := models.VehicleStatus{
			VehicleID: vehicle.ID.ID,
		}

		// Set timestamps
		if vehicle.Timestamp != nil {
			vehicleStatus.LastLocationUpdateTime = vehicle.Timestamp.UnixNano() / int64(time.Millisecond)
			vehicleStatus.LastUpdateTime = vehicle.Timestamp.UnixNano() / int64(time.Millisecond)
		}

		// Set location if available
		if vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			vehicleStatus.Location = &models.Location{
				Lat: *vehicle.Position.Latitude,
				Lon: *vehicle.Position.Longitude,
			}
		}

		// Set status and phase based on current status
		if vehicle.CurrentStatus != nil {
			switch *vehicle.CurrentStatus {
			case 0: // INCOMING_AT
				vehicleStatus.Status = "INCOMING_AT"
				vehicleStatus.Phase = "approaching"
			case 1: // STOPPED_AT
				vehicleStatus.Status = "STOPPED_AT"
				vehicleStatus.Phase = "stopped"
			case 2: // IN_TRANSIT_TO
				vehicleStatus.Status = "IN_TRANSIT_TO"
				vehicleStatus.Phase = "in_progress"
			default:
				vehicleStatus.Status = "SCHEDULED"
				vehicleStatus.Phase = "scheduled"
			}
		} else {
			vehicleStatus.Status = "SCHEDULED"
			vehicleStatus.Phase = "scheduled"
		}

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
					Lat: *vehicle.Position.Latitude,
					Lon: *vehicle.Position.Longitude,
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
				tripStatus.Orientation = obaOrientation
			}

			// Set service date (use current date for now)
			tripStatus.ServiceDate = time.Now().UnixNano() / int64(time.Millisecond)

			vehicleStatus.TripStatus = tripStatus

			// Add trip to references (basic trip reference)
			tripRefs[vehicle.Trip.ID.ID] = map[string]interface{}{
				"id":      vehicle.Trip.ID.ID,
				"routeId": vehicle.Trip.ID.RouteID,
			}

			// Find and add route to references
			if route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(r.Context(), vehicle.Trip.ID.RouteID); err == nil {
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
					url, color, textColor, shortName,
				)
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

	routeRefList := make([]interface{}, 0, len(routeRefs))
	for _, routeRef := range routeRefs {
		routeRefList = append(routeRefList, routeRef)
	}

	tripRefList := make([]interface{}, 0, len(tripRefs))
	for _, tripRef := range tripRefs {
		tripRefList = append(tripRefList, tripRef)
	}

	references := models.ReferencesModel{
		Agencies:   agencyRefList,
		Routes:     routeRefList,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []interface{}{},
		Trips:      tripRefList,
	}

	response := models.NewListResponse(vehiclesList, references)
	api.sendResponse(w, r, response)
}
