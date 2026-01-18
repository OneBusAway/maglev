package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	id := utils.ExtractIDFromParams(r)

	// Acquire read lock
	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	agency := api.GtfsManager.FindAgency(id)
	if agency == nil {
		http.Error(w, "null", http.StatusNotFound)
		return
	}

	routesForAgency := api.GtfsManager.RoutesForAgencyID(id)
	routesList := make([]models.Route, 0, len(routesForAgency))
	for _, route := range routesForAgency {
		routesList = append(routesList, models.NewRoute(
			utils.FormCombinedID(route.Agency.Id, route.Id), route.Agency.Id, route.ShortName, route.LongName,
			route.Description, models.RouteType(route.Type),
			route.Url, route.Color, route.TextColor, route.ShortName,
		))
	}

	references := models.ReferencesModel{
		Agencies: []models.AgencyReference{
			models.NewAgencyReference(
				agency.Id, agency.Name, agency.Url, agency.Timezone,
				agency.Language, agency.Phone, agency.Email,
				agency.FareUrl, "", false,
			),
		},
		Routes:     []interface{}{},
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponse(routesList, references, api.Clock)
	api.sendResponse(w, r, response)
}
