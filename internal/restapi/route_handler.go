package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) routeHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)

	// Validate ID
	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, routeID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

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
