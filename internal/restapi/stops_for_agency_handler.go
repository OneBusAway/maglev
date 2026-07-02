package restapi

import (
	"context"
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// stopsForAgencyHandler returns all stops belonging to a given agency with full stop details.
func (api *RestAPI) stopsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	// Validate agency exists
	agency, err := api.GtfsManager.FindAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if agency == nil {
		api.sendNotFound(w, r)
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

	// Build route references from stops
	routeRefs, err := api.BuildRouteReferences(ctx, id, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build references
	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{models.AgencyReferenceFromDatabase(agency)}
	references.Routes = routeRefs

	// Resolve parent stations referenced by any stop into references.stops.
	parentRefs, err := api.buildParentStationReferences(ctx, id, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	references.Stops = parentRefs

	response := models.NewListResponse(stopsList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}

// buildParentStationReferences resolves the unique parent stations referenced by
// stopsList into full Stop records for the references block.
func (api *RestAPI) buildParentStationReferences(ctx context.Context, agencyID string, stopsList []models.Stop) ([]models.Stop, error) {
	seen := make(map[string]struct{})
	var parentRawIDs []string
	for _, s := range stopsList {
		if s.Parent == "" {
			continue
		}
		rawID, err := utils.ExtractCodeID(s.Parent)
		if err != nil {
			continue
		}
		if _, ok := seen[rawID]; ok {
			continue
		}
		seen[rawID] = struct{}{}
		parentRawIDs = append(parentRawIDs, rawID)
	}

	parentRefs, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, parentRawIDs)
	if err != nil {
		return nil, err
	}

	return parentRefs, nil
}

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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		routeIdsString := routesByStop[stop.ID]
		if routeIdsString == nil {
			routeIdsString = []string{}
		}

		parentID := ""
		if nulls.StringOrEmpty(stop.ParentStation) != "" {
			parentID = utils.FormCombinedID(agencyID, stop.ParentStation.String)
		}

		stopsList = append(stopsList, models.Stop{
			Code:               nulls.StringOrDefault(stop.Code, stop.ID),
			Direction:          nulls.StringOrEmpty(stop.Direction),
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			Parent:             parentID,
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	return stopsList, nil
}
