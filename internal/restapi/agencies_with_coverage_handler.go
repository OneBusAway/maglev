package restapi

import (
	"fmt"
	"net/http"
	"strconv"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// agenciesWithCoverageHandler returns all transit agencies along with their geographic coverage areas.
func (api *RestAPI) agenciesWithCoverageHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	queryParams := r.URL.Query()

	// Validate version parameter
	if versionStr := queryParams.Get("version"); versionStr != "" {
		version, err := strconv.Atoi(versionStr)
		if err != nil || (version != 1 && version != 2) {
			api.sendError(w, r, http.StatusInternalServerError, fmt.Sprintf("unknown version: %s", versionStr))
			return
		}
	}

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

	// Parse includeReferences
	includeReferences := true
	if val := queryParams.Get("includeReferences"); val != "" {
		if parsed, parseErr := strconv.ParseBool(val); parseErr == nil {
			includeReferences = parsed
		}
	}

	data := map[string]any{
		"limitExceeded": limitExceeded,
		"list":          agenciesWithCoverage,
	}

	if includeReferences {
	references := models.NewEmptyReferences()
	references.Agencies = buildAgencyReferences(agencies)
		data["references"] = *references
	}

	response := models.NewOKResponse(data, api.Clock)
	api.sendResponse(w, r, response)
}
