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
	// If no stops, return empty list
	if len(stopIDs) == 0 {
		return []models.Stop{}, nil
	}

	// Batch fetch all stops in one query
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Batch fetch all route IDs for these stops in one query
	routeIDsRows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Build a map of stop ID to route IDs in memory
	routesByStop := make(map[string][]string)
	for _, row := range routeIDsRows {
		if rid, ok := row.RouteID.(string); ok {
			routesByStop[row.StopID] = append(routesByStop[row.StopID], rid)
		}
	}

	// Construct the stops list
	stopsList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIdsString := routesByStop[stop.ID]
		if routeIdsString == nil {
			routeIdsString = []string{}
		}

		stopsList = append(stopsList, models.Stop{
			Code:               stop.Code.String,
			Direction:          utils.NullStringOrEmpty(stop.Direction),
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	return stopsList, nil
}
