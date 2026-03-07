package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := utils.GetIDFromContext(r.Context())

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	agency := api.GtfsManager.FindAgency(id)

	if agency == nil {
		api.sendNull(w, r)
		return
	}

	routesForAgency := api.GtfsManager.RoutesForAgencyID(id)

	// Apply pagination
	offset, limit := utils.ParsePaginationParams(r)
	routesForAgency, limitExceeded := utils.PaginateSlice(routesForAgency, offset, limit)
	// Safe allocation logic
	routesList := make([]models.Route, 0, len(routesForAgency))

	for _, route := range routesForAgency {
		routesList = append(routesList, models.NewRoute(
			utils.FormCombinedID(route.Agency.Id, route.Id), route.Agency.Id, route.ShortName, route.LongName,
			route.Description, models.RouteType(route.Type),
			route.Url, route.Color, route.TextColor))
	}

	references := models.ReferencesModel{
		Agencies: []models.AgencyReference{
			models.NewAgencyReference(
				agency.Id, agency.Name, agency.Url, agency.Timezone,
				agency.Language, agency.Phone, agency.Email,
				agency.FareUrl, "", false,
			),
		},
		Routes:     []models.Route{},
		Situations: []models.Situation{},
		StopTimes:  []models.RouteStopTime{},
		Stops:      []models.Stop{},
		Trips:      []models.Trip{},
	}

	response := models.NewListResponse(routesList, references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
