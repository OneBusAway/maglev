package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) stopHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)

	// Validate ID
	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, stopID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// Acquire read lock for thread safety
	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	ctx := r.Context()

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil || stop.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	combinedRouteIDs := make([]string, len(routes))
	for i, route := range routes {
		combinedRouteIDs[i] = utils.FormCombinedID(agencyID, route.ID)
	}

	stopData := &models.Stop{
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
		Name:               stop.Name.String,
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		Code:               stop.Code.String,
		Direction:          "",
		LocationType:       int(stop.LocationType.Int64),
		WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		RouteIDs:           combinedRouteIDs,
		StaticRouteIDs:     combinedRouteIDs,
	}

	references := models.NewEmptyReferences()

	for _, route := range routes {
		routeModel := models.NewRoute(
			utils.FormCombinedID(agencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String,
			route.ShortName.String,
		)
		references.Routes = append(references.Routes, routeModel)
	}

	if len(routes) > 0 {
		route := routes[0]
		agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
		if err == nil {
			agencyModel := models.NewAgencyReference(
				agency.ID,
				agency.Name,
				agency.Url,
				agency.Timezone,
				agency.Lang.String,
				agency.Phone.String,
				agency.Email.String,
				agency.FareUrl.String,
				"",    // disclaimer
				false, // privateService
			)
			references.Agencies = append(references.Agencies, agencyModel)
		}
	}

	response := models.NewEntryResponse(stopData, references, api.Clock)
	api.sendResponse(w, r, response)
}
