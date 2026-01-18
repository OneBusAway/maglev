package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

func (api *RestAPI) agenciesWithCoverageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Acquire read lock
	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return
	}

	agencies, err := api.GtfsManager.GtfsDB.Queries.ListAgencies(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	lat, lon, latSpan, lonSpan := api.GtfsManager.GetRegionBounds()
	var agenciesWithCoverage []models.AgencyCoverage
	var agencyReferences []models.AgencyReference

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
	references := models.ReferencesModel{
		Agencies:   agencyReferences,
		Routes:     []interface{}{},
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	response := models.NewListResponse(agenciesWithCoverage, references, api.Clock)
	api.sendResponse(w, r, response)
}
