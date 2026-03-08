package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) stopHandler(w http.ResponseWriter, r *http.Request) {
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	stopID := parsed.CodeID // The raw GTFS stop ID

	// agencyID here is specifically the *Stop's* agency.
	// Routes serving this stop might belong to different agencies.
	agencyID := parsed.AgencyID

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
		// Use route.AgencyID, not the stop's agencyID.
		// A stop can be served by routes from other agencies.
		combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)
	}

	stopData := &models.Stop{
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
		Name:               utils.NullStringOrEmpty(stop.Name),
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		Code:               utils.NullStringOrEmpty(stop.Code),
		Direction:          utils.NullStringOrEmpty(stop.Direction),
		LocationType:       int(stop.LocationType.Int64),
		WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		RouteIDs:           combinedRouteIDs,
		StaticRouteIDs:     combinedRouteIDs,
	}

	references := models.NewEmptyReferences()
	uniqueAgencyIDs := make(map[string]bool)

	// Add routes to references and collect unique agency IDs
	for _, route := range routes {
		routeModel := models.NewRoute(
			utils.FormCombinedID(route.AgencyID, route.ID),
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String)

		references.Routes = append(references.Routes, routeModel)
		uniqueAgencyIDs[route.AgencyID] = true
	}

	// Fetch references for ALL unique agencies involved, not just the first one.
	for aid := range uniqueAgencyIDs {
		agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, aid)
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
				"",
				false,
			)
			references.Agencies = append(references.Agencies, agencyModel)
		}
	}

	// Populate situation references for alerts affecting this stop and its serving routes
	rawRouteIDs := make([]string, len(routes))
	for i, route := range routes {
		rawRouteIDs[i] = route.ID
	}
	alerts := api.collectAlertsForStopsAndRoutes([]string{stopID}, rawRouteIDs)
	situations := api.BuildSituationReferences(alerts)
	references.Situations = append(references.Situations, situations...)

	response := models.NewEntryResponse(stopData, references, api.Clock)
	api.sendResponse(w, r, response)
}
