package restapi

import (
	"context"
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) stopsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return
	}

	id := utils.ExtractIDFromParams(r)

	// Validate agency exists
	agency := api.GtfsManager.FindAgency(id)
	if agency == nil {
		api.sendNull(w, r)
		return
	}

	// Get all stop IDs for the agency
	stopIDs, err := api.GtfsManager.GtfsDB.Queries.GetStopIDsForAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build stops list with full details
	stopsList, err := api.buildStopsListForAgency(ctx, id, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build agency reference
	agencyRef := models.NewAgencyReference(
		agency.Id,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Language,
		agency.Phone,
		agency.Email,
		agency.FareUrl,
		"",
		false,
	)

	// Build route references from stops
	routeRefs, err := api.BuildRouteReferencesAsInterface(ctx, id, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build references
	references := models.ReferencesModel{
		Agencies:   []models.AgencyReference{agencyRef},
		Routes:     routeRefs,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponse(stopsList, references, api.Clock)
	api.sendResponse(w, r, response)
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) buildStopsListForAgency(ctx context.Context, agencyID string, stopIDs []string) ([]models.Stop, error) {
	if len(stopIDs) == 0 {
		return []models.Stop{}, nil
	}

	// Batch query 1: Get all stops in one query
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Batch query 2: Get all route IDs for all stops in one query
	routeRows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Organize routes in memory by stop ID
	routesMap := make(map[string][]string)
	for _, row := range routeRows {
		routeID, ok := row.RouteID.(string)
		if ok {
			routesMap[row.StopID] = append(routesMap[row.StopID], utils.FormCombinedID(agencyID, routeID))
		}
	}

	// Build stops list from batched results
	stopsList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIDs := routesMap[stop.ID]
		if routeIDs == nil {
			routeIDs = []string{}
		}

		direction := models.UnknownValue
		if stop.Direction.Valid && stop.Direction.String != "" {
			direction = stop.Direction.String
		}

		stopsList = append(stopsList, models.Stop{
			Code:               stop.Code.String,
			Direction:          direction,
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			RouteIDs:           routeIDs,
			StaticRouteIDs:     routeIDs,
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	return stopsList, nil
}
