package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// agenciesWithCoverageHandler returns all transit agencies along with their geographic coverage areas.
func (api *RestAPI) agenciesWithCoverageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	// Pagination is intentionally omitted for this endpoint to strictly align
	// with the OneBusAway specification, which defines that maxCount is
	// accepted but not applied, and limitExceeded is always false.
	limitExceeded := false

	boundsMap := api.GtfsManager.GetRegionBounds()
	// Important to use an empty slice rather than nil so that empty json responses don't return nil.
	agenciesWithCoverage := make([]models.AgencyCoverage, 0)
	for _, a := range agencies {
		bounds := boundsMap[a.ID]
		agenciesWithCoverage = append(
			agenciesWithCoverage,
			models.NewAgencyCoverage(a.ID, bounds.Lat, bounds.LatSpan, bounds.Lon, bounds.LonSpan),
		)
	}

	references := models.NewEmptyReferences()

	includeReferences := ShouldIncludeReferences(r)

	if includeReferences {
		references.Agencies = buildAgencyReferences(agencies)
	}

	response := models.NewListResponse(agenciesWithCoverage, *references, limitExceeded, api.Clock)
	api.sendResponse(w, r, response)
}
