package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routeHandler(w http.ResponseWriter, r *http.Request) {
	// Retrieve pre-validated ID from context (Middleware handles parsing)
	parsed, _ := utils.GetParsedIDFromContext(r.Context())
	agencyID := parsed.AgencyID
	routeID := parsed.CodeID // The raw GTFS route ID

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	ctx := r.Context()

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil || route.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	routeData := models.NewRoute(
		utils.FormCombinedID(agencyID, route.ID),
		agencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String,
		utils.NullStringOrEmpty(route.ShortName),
	)

	references := models.NewEmptyReferences()

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
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

	response := models.NewEntryResponse(routeData, references, api.Clock)
	api.sendResponse(w, r, response)
}
