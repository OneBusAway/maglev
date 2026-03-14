package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// problemReportsForStopHandler returns all user-submitted problem reports for a given stop.
func (api *RestAPI) problemReportsForStopHandler(w http.ResponseWriter, r *http.Request) {
	_, stopID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	// Safety check: Ensure DB is initialized
	if api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		api.sendError(w, r, http.StatusInternalServerError, "internal server error")
		return
	}

	ctx := r.Context()
	reports, err := api.GtfsManager.GtfsDB.Queries.GetProblemReportsByStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	reportList := make([]models.ProblemReportStop, 0, len(reports))
	for _, report := range reports {
		reportList = append(reportList, models.NewProblemReportStop(report))
	}

	references := models.NewEmptyReferences()
	response := models.NewListResponse(reportList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}
