package restapi

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// Pre-compiled regex pattern for FTS5 query sanitization
var fts5SpecialCharsRegex = regexp.MustCompile(`[*"():^$@#~<>{}[\]\\|&!]`)

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

// sanitizeFTS5Query removes special FTS5 characters by replacing them with spaces
// to prevent query syntax errors. Does not preserve the original characters.
//
// Operator words (AND, OR, NOT, NEAR) are deliberately left intact: every term is wrapped
// in double quotes before the query is assembled, so FTS5 reads them as literal tokens
// rather than syntax. Stripping them made stop names containing those words unsearchable.
func sanitizeFTS5Query(input string) string {
	sanitized := fts5SpecialCharsRegex.ReplaceAllString(input, " ")
	sanitized = strings.TrimSpace(sanitized)

	return strings.Join(strings.Fields(sanitized), " ")
}

// extractFTS5Terms splits sanitized input into terms and filters out stray punctuation
// (such as "/" or "-") that FTS5 tokenizes to nothing and would otherwise cause syntax errors.
func extractFTS5Terms(sanitizedQuery string) []string {
	rawTerms := strings.Fields(sanitizedQuery)
	terms := make([]string, 0, len(rawTerms))
	for _, term := range rawTerms {
		if utils.ContainsLetterOrDigit(term) {
			terms = append(terms, term)
		}
	}
	return terms
}

// searchStopsHandler searches for stops matching a user-provided query string
// using full-text search, with optional geographic bounds filtering.
func (api *RestAPI) searchStopsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Parse Parameters
	queryParams := r.URL.Query()
	fieldErrors := make(map[string][]string)

	includeReferences := ShouldIncludeReferences(r)

	// Standardized parameter parsing
	query, fieldErrors := utils.ParseRequiredStringParam(queryParams, "input", fieldErrors)
	limit, fieldErrors := utils.ParseMaxCount(queryParams, 20, fieldErrors)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	// 2. Sanitize and construct FTS5 query
	sanitizedQuery := sanitizeFTS5Query(query)
	terms := extractFTS5Terms(sanitizedQuery)

	if len(terms) == 0 {
		response := models.NewListResponseWithRange([]models.Stop{}, *models.NewEmptyReferences(), false, api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	queryTerms := make([]string, len(terms))
	for i, term := range terms {
		queryTerms[i] = `"` + term + `"*`
	}
	searchQuery := strings.Join(queryTerms, " AND ")

	searchParams := gtfsdb.SearchStopsByNameParams{
		SearchQuery: searchQuery,
		Limit:       int64(limit + 1), // Request limit + 1 to accurately determine if pagination boundaries are exceeded.
	}

	// 3. Perform Full Text Search (with logged fallback)
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
	if err != nil {
		// Check for FTS5-specific errors before retrying
		// This prevents retries on infrastructure errors (context canceled, db locked, etc.)
		errStr := err.Error()
		if strings.Contains(errStr, "fts5") || strings.Contains(errStr, "syntax") {
			api.Logger.Warn(
				"FTS5 wildcard query failed, retrying without wildcard",
				"original_error", err,
				"fts_query", searchQuery,
				"sanitized_input", sanitizedQuery,
			)

			fallbackTerms := make([]string, len(terms))
			for i, term := range terms {
				fallbackTerms[i] = `"` + term + `"`
			}
			searchQuery = strings.Join(fallbackTerms, " AND ")

			searchParams.SearchQuery = searchQuery

			stops, err = api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
			if err != nil {
				api.serverErrorResponse(
					w,
					r,
					fmt.Errorf("SearchStopsByName failed for query %q: %w", searchParams.SearchQuery, err),
				)
				return
			}
		} else {
			api.serverErrorResponse(
				w,
				r,
				fmt.Errorf("SearchStopsByName failed for query %q: %w", searchParams.SearchQuery, err),
			)
			return
		}
	}

	stops, isLimitExceeded := utils.PaginateSlice(stops, 0, limit)

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
	keptStopIDs := make([]string, 0, len(stops))
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
		keptStopIDs = append(keptStopIDs, s.ID)
		keptStopsSet[s.ID] = true

		if parentID := nulls.StringOrEmpty(s.ParentStation); parentID != "" {
			parentIDsByAgency[agencyID] = append(parentIDsByAgency[agencyID], parentID)
		}
	}

	// 7. Build References
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

		// Populate situation references for alerts affecting the returned stops
		alerts := api.collectAlertsForStops(keptStopIDs)
		situations := api.BuildSituationReferences(alerts)
		references.Situations = append(references.Situations, situations...)

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

// buildSearchStopModel builds a stop model for a search result, adding the parent
// station field that the shared buildStopModel does not set.
func (api *RestAPI) buildSearchStopModel(ctx context.Context, agencyID string, stop gtfsdb.Stop, combinedRouteIDs []string) models.Stop {
	stopModel := api.buildStopModel(ctx, agencyID, stop, combinedRouteIDs)
	stopModel.Parent = utils.FormCombinedID(agencyID, nulls.StringOrEmpty(stop.ParentStation))
	return stopModel
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
