package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// agenciesWithCoverageHandler returns all transit agencies along with their geographic coverage areas.
func (api *RestAPI) agenciesWithCoverageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencies, err := api.GtfsManager.GtfsDB.Queries.ListAgencies(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Apply pagination
	offset, limit := utils.ParsePaginationParams(r)
	agencies, limitExceeded := utils.PaginateSlice(agencies, offset, limit)

	lat, lon, latSpan, lonSpan := api.GtfsManager.GetRegionBounds()
	agenciesWithCoverage := make([]models.AgencyCoverage, 0)
	agencyReferences := make([]models.AgencyReference, 0)

	for _, a := range agencies {
		agenciesWithCoverage = append(
			agenciesWithCoverage,
			models.NewAgencyCoverage(a.ID, lat, latSpan, lon, lonSpan),
		)

		agencyReferences = append(
			agencyReferences,
			models.NewAgencyReference(
				a.ID,
				a.Name,
				a.Url,
				a.Timezone,
				a.Lang.String,
				a.Phone.String,
				a.Email.String,
				a.FareUrl.String,
				"",
				false,
			),
		)
	}

	// Create references with the agency
	references := models.NewEmptyReferences()
	references.Agencies = agencyReferences

	response := models.NewListResponse(agenciesWithCoverage, *references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
