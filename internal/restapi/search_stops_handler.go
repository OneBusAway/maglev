package restapi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// Pre-compiled regex patterns for FTS5 query sanitization
var (
	fts5SpecialCharsRegex = regexp.MustCompile(`[*"():^$@#~<>{}[\]\\|&!]`)
	fts5OperatorsRegex    = regexp.MustCompile(`(?i)\b(AND|OR|NOT|NEAR)\b`)
)

// sanitizeFTS5Query removes special FTS5 characters by replacing them with spaces
// to prevent query syntax errors. Does not preserve the original characters.
func sanitizeFTS5Query(input string) string {
	// 1. Remove ALL FTS5 special characters using pre-compiled regex
	sanitized := fts5SpecialCharsRegex.ReplaceAllString(input, " ")

	// 2. Remove FTS5 operators using pre-compiled regex
	sanitized = fts5OperatorsRegex.ReplaceAllString(sanitized, " ")

	// 3. Trim and collapse whitespace
	sanitized = strings.TrimSpace(sanitized)
	sanitized = strings.Join(strings.Fields(sanitized), " ")

	return sanitized
}

// extractFTS5Terms splits sanitized input into terms and filters out stray punctuation
// (such as "/" or "-") that FTS5 tokenizes to nothing and would otherwise cause syntax errors.
func extractFTS5Terms(sanitizedQuery string) []string {
	rawTerms := strings.Fields(sanitizedQuery)
	terms := make([]string, 0, len(rawTerms))
	for _, term := range rawTerms {
		if strings.ContainsFunc(term, func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		}) {
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

	for _, row := range routesRows {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		combinedRouteID := utils.FormCombinedID(row.AgencyID, row.ID)
		routesByStopID[row.StopID] = append(routesByStopID[row.StopID], combinedRouteID)
	}

	uniqueAgencies := make(map[string]bool)
	for _, row := range agencyRows {
		uniqueAgencies[row.ID] = true
	}

	// 6. Construct Stop Models
	agencyByStopID := resolveSearchStopAgencies(stops, routesByStopID, uniqueAgencies)

	stopModels := make([]models.Stop, 0, len(stops))
	parentIDsByAgency := make(map[string][]string)

	for _, s := range stops {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		agencyID := agencyByStopID[s.ID]
		stopModels = append(stopModels, api.buildSearchStopModel(ctx, agencyID, stopFromSearchRow(s), routesByStopID[s.ID]))

		if parentID := nulls.StringOrEmpty(s.ParentStation); parentID != "" {
			parentIDsByAgency[agencyID] = append(parentIDsByAgency[agencyID], parentID)
		}
	}

	// 7. Build References
	references := models.NewEmptyReferences()
	if includeReferences {
		references.Routes = routeReferencesForStops(routesRows)
		utils.SortModelRoutesByName(references.Routes)

		references.Agencies = agencyReferencesForStops(agencyRows)
		utils.SortAgencyReferencesByID(references.Agencies)

		// Populate situation references for alerts affecting the returned stops
		alerts := api.collectAlertsForStops(stopIDs)
		situations := api.BuildSituationReferences(alerts)
		references.Situations = append(references.Situations, situations...)

		references.Stops, err = api.buildSearchParentStationReferences(ctx, parentIDsByAgency)
		if err != nil {
			api.serverErrorResponse(w, r, fmt.Errorf("failed to fetch parent stops: %w", err))
			return
		}
		utils.SortModelStopsByID(references.Stops)
	}

	response := models.NewListResponseWithRange(stopModels, *references, false, api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}

// resolveSearchStopAgencies determines the agency prefix for each searched stop. A stop
// with no routes of its own inherits the agency resolved for a sibling stop under the
// same parent station, so that its combined IDs still resolve for the client. Where
// siblings span several agencies the lowest agency ID wins, so the inherited prefix does
// not depend on how the result set happens to be ordered or truncated.
func resolveSearchStopAgencies(stops []gtfsdb.SearchStopsByNameRow, routesByStopID map[string][]string, uniqueAgencies map[string]bool) map[string]string {
	agencyByStopID := make(map[string]string, len(stops))
	agencyByParentID := make(map[string]string)

	for _, s := range stops {
		agencyID := resolveAgencyID(s.ID, routesByStopID, uniqueAgencies)
		agencyByStopID[s.ID] = agencyID

		parentID := nulls.StringOrEmpty(s.ParentStation)
		if agencyID == "" || parentID == "" {
			continue
		}
		if lowest, seen := agencyByParentID[parentID]; !seen || agencyID < lowest {
			agencyByParentID[parentID] = agencyID
		}
	}

	for _, s := range stops {
		if agencyByStopID[s.ID] == "" {
			agencyByStopID[s.ID] = agencyByParentID[nulls.StringOrEmpty(s.ParentStation)]
		}
	}

	return agencyByStopID
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

// buildSearchStopModel builds a stop model for a search result. Search spans agencies, so
// the agency is resolved per stop rather than being fixed for the request, and combined
// IDs fall back to raw IDs when it cannot be determined.
func (api *RestAPI) buildSearchStopModel(ctx context.Context, agencyID string, stop gtfsdb.Stop, combinedRouteIDs []string) models.Stop {
	if combinedRouteIDs == nil {
		combinedRouteIDs = []string{}
	}

	stopModel := api.buildStopModel(ctx, agencyID, stop, combinedRouteIDs)
	stopModel.ID = formatStopID(agencyID, stop.ID)
	stopModel.Parent = formatStopID(agencyID, nulls.StringOrEmpty(stop.ParentStation))

	return stopModel
}

// buildSearchParentStationReferences resolves parent stations into references, emitting one
// entry per (parent station, agency) pair referenced by the result set so that every
// non-empty parent value in the returned list has a matching reference.
func (api *RestAPI) buildSearchParentStationReferences(ctx context.Context, parentIDsByAgency map[string][]string) ([]models.Stop, error) {
	parentRefs := make([]models.Stop, 0, len(parentIDsByAgency))

	for agencyID, parentIDs := range parentIDsByAgency {
		// The shared builder always prefixes IDs with the agency, so stops whose agency
		// could never be determined need their raw IDs preserved instead.
		if agencyID == "" {
			refs, err := api.buildUnprefixedParentReferences(ctx, parentIDs)
			if err != nil {
				return nil, err
			}
			parentRefs = append(parentRefs, refs...)
			continue
		}

		refs, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, parentIDs)
		if err != nil {
			return nil, err
		}
		parentRefs = append(parentRefs, refs...)
	}

	return parentRefs, nil
}

// buildUnprefixedParentReferences builds parent station references keyed by their raw,
// unprefixed IDs, matching the parent values emitted for stops with no resolvable agency.
func (api *RestAPI) buildUnprefixedParentReferences(ctx context.Context, parentIDs []string) ([]models.Stop, error) {
	parentStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, dedupeStrings(parentIDs))
	if err != nil {
		return nil, err
	}

	refs := make([]models.Stop, 0, len(parentStops))
	for _, parentStop := range parentStops {
		refs = append(refs, api.buildSearchStopModel(ctx, "", parentStop, nil))
	}

	return refs, nil
}
