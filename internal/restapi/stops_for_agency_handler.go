package restapi

import (
	"context"
	"net/http"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) stopsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

func (api *RestAPI) buildStopsListForAgency(ctx context.Context, agencyID string, stopIDs []string) ([]models.Stop, error) {
	stopsList := make([]models.Stop, 0, len(stopIDs))

	for _, stopID := range stopIDs {
		stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if err != nil {
			continue
		}

		routeIds, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStop(ctx, stop.ID)
		if err != nil {
			continue
		}

		routeIdsString := make([]string, len(routeIds))
		for i, id := range routeIds {
			routeIdsString[i] = utils.FormCombinedID(agencyID, id.(string))
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
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(gtfs.WheelchairBoarding(stop.WheelchairBoarding.Int64)),
		})
	}

	return stopsList, nil
}
