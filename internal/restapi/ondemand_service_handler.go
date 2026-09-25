package restapi

import (
	"database/sql"
	"errors"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

// onDemandServiceHandler returns one on-demand service with its rules and the
// full /ondemand references block. geometryDetail defaults to full here: the
// rider has asked for this service, so one large payload is acceptable.
func (api *RestAPI) onDemandServiceHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, serviceID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	geometryDetail, fieldErrors := parseGeometryDetail(r.URL.Query(), GeometryDetailFull, nil)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	service, err := api.GtfsManager.GtfsDB.Queries.GetOnDemandService(ctx, serviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			api.sendNotFound(w, r)
			return
		}
		api.serverErrorResponse(w, r, err)
		return
	}
	if service.AgencyID != agencyID {
		api.sendNotFound(w, r)
		return
	}

	services, references, err := api.buildOnDemandServices(ctx, []gtfsdb.OndemandService{service}, onDemandBuildOptions{GeometryDetail: geometryDetail})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	api.sendResponse(w, r, models.NewOnDemandEntryResponse(services[0], *references, api.Clock))
}
