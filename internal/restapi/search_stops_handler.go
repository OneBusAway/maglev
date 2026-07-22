package restapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
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
	stopModels := make([]models.Stop, 0, len(stops))

	for _, s := range stops {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		var agencyID string

		if rts, ok := routesByStopID[s.ID]; ok && len(rts) > 0 {
			agencyID, _, _ = utils.ExtractAgencyIDAndCodeID(rts[0])
		} else if len(uniqueAgencies) == 1 {
			for id := range uniqueAgencies {
				agencyID = id
				break
			}
		}

		var combinedStopID string
		if agencyID != "" {
			combinedStopID = utils.FormCombinedID(agencyID, s.ID)
		} else {
			combinedStopID = s.ID
		}

		routeIDs := routesByStopID[s.ID]
		if routeIDs == nil {
			routeIDs = []string{}
		}

		name := ""
		if s.Name.Valid {
			name = s.Name.String
		}

		code := ""
		if s.Code.Valid {
			code = s.Code.String
		}

		direction := ""
		if s.Direction.Valid {
			direction = s.Direction.String
		}

		parentStation := ""
		if s.ParentStation.Valid {
			parentStation = s.ParentStation.String
		}

		stopModel := models.Stop{
			ID:                 combinedStopID,
			Name:               name,
			Lat:                s.Lat,
			Lon:                s.Lon,
			Code:               code,
			Direction:          direction,
			LocationType:       int(s.LocationType.Int64),
			WheelchairBoarding: utils.MapWheelchairBoarding(gtfs.WheelchairBoarding(s.WheelchairBoarding.Int64)),
			RouteIDs:           routeIDs,
			StaticRouteIDs:     routeIDs,
			Parent:             parentStation,
		}

		stopModels = append(stopModels, stopModel)
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
	}

	response := models.NewListResponseWithRange(stopModels, *references, false, api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
