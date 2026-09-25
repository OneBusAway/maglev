package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// onDemandServicesForAgencyHandler lists an agency's on-demand services. An
// unknown agency is a 404 (matching routes-for-agency); a known agency with no
// on-demand services returns an empty list.
func (api *RestAPI) onDemandServicesForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}
	geometryDetail, fieldErrors := parseGeometryDetail(r.URL.Query(), GeometryDetailSimplified, nil)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if agency == nil {
		api.sendNotFound(w, r)
		return
	}

	services, err := api.GtfsManager.GtfsDB.Queries.GetOnDemandServicesForAgency(ctx, agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	list, references, err := api.buildOnDemandServices(ctx, services, onDemandBuildOptions{GeometryDetail: geometryDetail})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	api.sendResponse(w, r, models.NewOnDemandListResponse(list, *references, api.Clock))
}
