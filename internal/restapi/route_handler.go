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

	_, routeID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	ctx := r.Context()

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil || route.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	routeData := &models.Route{
		ID:                utils.FormCombinedID(route.AgencyID, route.ID),
		AgencyID:          route.AgencyID,
		Color:             route.Color.String,
		Description:       route.Desc.String,
		ShortName:         route.ShortName.String,
		LongName:          route.LongName.String,
		NullSafeShortName: utils.NullStringOrEmpty(route.ShortName),
		TextColor:         route.TextColor.String,
		Type:              models.RouteType(route.Type),
		URL:               route.Url.String,
	}

	references := models.NewEmptyReferences()

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

	response := models.NewEntryResponse(routeData, references)
	api.sendResponse(w, r, response)
}
