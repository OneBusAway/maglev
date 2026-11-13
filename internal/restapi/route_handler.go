package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// routeHandler returns details for a single transit route identified by its combined agency_routeID.
func (api *RestAPI) routeHandler(w http.ResponseWriter, r *http.Request) {
	includeReferences, referenceErrors := ShouldIncludeReferences(r, nil)
	if len(referenceErrors) > 0 {
		api.validationErrorResponse(w, r, referenceErrors)
		return
	}

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		api.dbErrorResponse(w, r, err)
		return
	}
	if route.ID == "" || route.AgencyID != agencyID {
		api.sendNotFound(w, r)
		return
	}

	routeData := models.NewRoute(
		utils.FormCombinedID(agencyID, route.ID),
		agencyID,
		nulls.StringOrEmpty(route.ShortName),
		nulls.StringOrEmpty(route.LongName),
		nulls.StringOrEmpty(route.Desc),
		models.RouteType(route.Type),
		nulls.StringOrEmpty(route.Url),
		nulls.StringOrEmpty(route.Color),
		nulls.StringOrEmpty(route.TextColor))

	// This response has no situationIds, so situation references remain empty.
	references := models.NewEmptyReferences()

	if includeReferences {
		agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
		if err != nil {
			api.dbErrorResponse(w, r, err)
			return
		}
		// Use the existing helper to map the database row to the model
		references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))
	}

	response := models.NewEntryResponse(routeData, *references, api.Clock)
	api.sendResponse(w, r, response)
}
