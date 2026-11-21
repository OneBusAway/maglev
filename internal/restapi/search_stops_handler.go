package restapi

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// GTFS extended special-vehicle route types. A stop served by exactly one route of
// one of these types is excluded from stop search results.
const (
	routeTypeShuttleBus                int64 = 711
	routeTypeSchoolBus                 int64 = 712
	routeTypeSchoolAndPublicServiceBus int64 = 713
	routeTypeRailReplacementBus        int64 = 714
)

// isSpecialVehicleRouteType reports whether a GTFS route type denotes a special
// vehicle service that does not qualify a stop for stop search results on its own.
func isSpecialVehicleRouteType(routeType int64) bool {
	switch routeType {
	case routeTypeShuttleBus, routeTypeSchoolBus, routeTypeSchoolAndPublicServiceBus, routeTypeRailReplacementBus:
		return true
	}
	return false
}

// searchStopsHandler searches for stops matching a user-provided query string
// using full-text search, with optional geographic bounds filtering.
func (api *RestAPI) searchStopsHandler(w http.ResponseWriter, r *http.Request) {
	includeReferences, referenceErrors := ShouldIncludeReferences(r, nil)

	ctx := r.Context()

	// 1. Parse Parameters
	queryParams := r.URL.Query()
	fieldErrors := referenceErrors

	// Standardized parameter parsing
	query, fieldErrors := utils.ParseRequiredStringParam(queryParams, "input", fieldErrors)
	limit, fieldErrors := utils.ParseMaxCount(queryParams, 20, fieldErrors)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// 2. Perform Full Text Search via gtfsdb
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStops(ctx, gtfsdb.SearchRequest{
		Query:       query,
		Operator:    gtfsdb.SearchOperatorAnd,
		PrefixMatch: true,
		Limit:       int64(limit + 1), // Request limit + 1 to accurately determine if pagination boundaries are exceeded.
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stops, isLimitExceeded := utils.PaginateSlice(stops, 0, limit)

	if len(stops) == 0 {
		response := models.NewListResponseWithRange([]models.Stop{}, *models.NewEmptyReferences(), false, api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	// 4. Batch Fetch Related Data
	stopIDs := make([]string, len(stops))
	for i, s := range stops {
		stopIDs[i] = s.ID
	}

	routesRows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, fmt.Errorf("failed to fetch routes for stops: %w", err))
		return
	}

	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, fmt.Errorf("failed to fetch agencies for stops: %w", err))
		return
	}

	// 5. Organize Data
	routesByStopID := make(map[string][]string)
	routeTypes := make(map[string]int64)

	for _, row := range routesRows {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		combinedRouteID := utils.FormCombinedID(row.AgencyID, row.ID)
		routesByStopID[row.StopID] = append(routesByStopID[row.StopID], combinedRouteID)
		routeTypes[combinedRouteID] = row.Type
	}

	// 6. Construct Stop Models
	stopModels := make([]models.Stop, 0, len(stops))
	parentIDsByAgency := make(map[string][]string)
	keptStopsSet := make(map[string]bool)

	for _, s := range stops {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		routeIDs := routesByStopID[s.ID]
		if len(routeIDs) == 0 {
			continue
		}

		// Legacy behaviour: only stops with exactly one route are type-filtered
		if len(routeIDs) == 1 && isSpecialVehicleRouteType(routeTypes[routeIDs[0]]) {
			continue
		}

		// The search query resolves this from the precomputed index, so the stop's combined
		// ID here matches the key the results were sorted by.
		agencyID := nulls.StringOrEmpty(s.AgencyID)

		stopModels = append(stopModels, api.buildSearchStopModel(ctx, agencyID, stopFromSearchRow(s), routeIDs))
		keptStopsSet[s.ID] = true

		if parentID := nulls.StringOrEmpty(s.ParentStation); parentID != "" {
			parentIDsByAgency[agencyID] = append(parentIDsByAgency[agencyID], parentID)
		}
	}

	// 7. Build References
	// This response has no situationIds, so situation references remain empty.
	references := models.NewEmptyReferences()
	if includeReferences {
		keptRoutesRows := make([]gtfsdb.GetRoutesForStopsRow, 0, len(routesRows))
		for _, row := range routesRows {
			if keptStopsSet[row.StopID] {
				keptRoutesRows = append(keptRoutesRows, row)
			}
		}
		keptAgencyRows := make([]gtfsdb.GetAgenciesForStopsRow, 0, len(agencyRows))
		for _, row := range agencyRows {
			if keptStopsSet[row.StopID] {
				keptAgencyRows = append(keptAgencyRows, row)
			}
		}
		references.Agencies = agencyReferencesForStops(keptAgencyRows)

		var parentRoutes map[string]gtfsdb.GetRoutesForStopsRow
		references.Stops, parentRoutes, err = api.buildSearchParentStationReferences(ctx, parentIDsByAgency)
		if err != nil {
			api.serverErrorResponse(w, r, fmt.Errorf("failed to fetch parent stops: %w", err))
			return
		}
		utils.SortModelStopsByID(references.Stops)

		// Append missing agencies from parent routes to references.Agencies
		existingAgencies := make(map[string]bool)
		for _, agency := range references.Agencies {
			existingAgencies[agency.ID] = true
		}
		for _, row := range parentRoutes {
			if !existingAgencies[row.AgencyID] {
				api.appendRouteAgencyReference(ctx, references, row.AgencyID, "")
				existingAgencies[row.AgencyID] = true
			}
		}
		utils.SortAgencyReferencesByID(references.Agencies)

		references.Routes = mergeParentRouteReferences(routeReferencesForStops(keptRoutesRows), parentRoutes)
		utils.SortModelRoutesByName(references.Routes)
	}

	response := models.NewListResponseWithRange(stopModels, *references, false, api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}

// stopFromSearchRow converts a full-text search result row into the stop record the
// shared reference builders operate on.
func stopFromSearchRow(row gtfsdb.SearchStopsByNameRow) gtfsdb.Stop {
	return gtfsdb.Stop{
		ID:                 row.ID,
		Code:               row.Code,
		Name:               row.Name,
		Lat:                row.Lat,
		Lon:                row.Lon,
		LocationType:       row.LocationType,
		WheelchairBoarding: row.WheelchairBoarding,
		Direction:          row.Direction,
		ParentStation:      row.ParentStation,
	}
}

// buildSearchStopModel builds a stop model for a search result.
func (api *RestAPI) buildSearchStopModel(ctx context.Context, agencyID string, stop gtfsdb.Stop, combinedRouteIDs []string) models.Stop {
	return api.buildStopModel(ctx, agencyID, stop, combinedRouteIDs)
}

// buildSearchParentStationReferences resolves parent stations into references, emitting one
// entry per (parent station, agency) pair referenced by the result set so that every
// non-empty parent value in the returned list has a matching reference. It also returns
// the unique routes serving those parent stations, keyed the same way their routeIds are,
// so callers can merge them into references.routes.
func (api *RestAPI) buildSearchParentStationReferences(ctx context.Context, parentIDsByAgency map[string][]string) ([]models.Stop, map[string]gtfsdb.GetRoutesForStopsRow, error) {
	parentRefs := make([]models.Stop, 0, len(parentIDsByAgency))
	parentRoutes := make(map[string]gtfsdb.GetRoutesForStopsRow)

	for agencyID, parentIDs := range parentIDsByAgency {
		refs, routes, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, parentIDs)
		if err != nil {
			return nil, nil, err
		}
		parentRefs = append(parentRefs, refs...)
		maps.Copy(parentRoutes, routes)
	}

	return parentRefs, parentRoutes, nil
}
