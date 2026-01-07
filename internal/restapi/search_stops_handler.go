package restapi

import (
	"net/http"
	"strconv"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// sanitizeFTS5Query escapes special FTS5 characters to prevent query syntax errors
func sanitizeFTS5Query(input string) string {
	// FTS5 special characters that need escaping: " * ( ) AND OR NOT
	// Replace them with spaces to maintain word boundaries
	replacer := strings.NewReplacer(
		`"`, " ",
		`*`, " ",
		`(`, " ",
		`)`, " ",
	)
	
	sanitized := replacer.Replace(input)
	
	// Remove FTS5 operators (case-insensitive)
	sanitized = strings.ReplaceAll(sanitized, " AND ", " ")
	sanitized = strings.ReplaceAll(sanitized, " OR ", " ")
	sanitized = strings.ReplaceAll(sanitized, " NOT ", " ")
	sanitized = strings.ReplaceAll(sanitized, " and ", " ")
	sanitized = strings.ReplaceAll(sanitized, " or ", " ")
	sanitized = strings.ReplaceAll(sanitized, " not ", " ")
	
	// Trim and collapse multiple spaces
	sanitized = strings.TrimSpace(sanitized)
	sanitized = strings.Join(strings.Fields(sanitized), " ")
	
	return sanitized
}

func (api *RestAPI) searchStopsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Parse Parameters
	query := r.URL.Query().Get("input")
	if query == "" {
		api.validationErrorResponse(w, r, map[string][]string{"input": {"required"}})
		return
	}

	limit := 50
	if maxCountStr := r.URL.Query().Get("maxCount"); maxCountStr != "" {
		if parsed, err := strconv.Atoi(maxCountStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	// 2. Sanitize and construct FTS5 query
	sanitizedQuery := sanitizeFTS5Query(query)
	
	// If sanitization removed everything, return empty results
	if sanitizedQuery == "" {
		data := struct {
			LimitExceeded bool                   `json:"limitExceeded"`
			List          []models.Stop          `json:"list"`
			OutOfRange    bool                   `json:"outOfRange"`
			References    models.ReferencesModel `json:"references"`
		}{
			LimitExceeded: false,
			List:          []models.Stop{},
			OutOfRange:    false,
			References:    models.NewEmptyReferences(),
		}

		response := models.ResponseModel{
			Code:        200,
			CurrentTime: models.ResponseCurrentTime(),
			Version:     2,
			Text:        "OK",
			Data:        data,
		}

		api.sendResponse(w, r, response)
		return
	}
	
	// Wrap in quotes and add wildcard for prefix matching
	searchQuery := `"` + sanitizedQuery + `*"`

	searchParams := gtfsdb.SearchStopsByNameParams{
		SearchQuery: searchQuery,
		Limit:       int64(limit),
	}

	// 3. Perform Full Text Search with error handling
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
	if err != nil {
		// If FTS5 query still fails (edge case), try without wildcard as fallback
		searchQuery = `"` + sanitizedQuery + `"`
		searchParams.SearchQuery = searchQuery
		
		stops, err = api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	// 4. Batch Fetch Related Data (Routes & Agencies)
	stopIDs := make([]string, len(stops))
	for i, s := range stops {
		stopIDs[i] = s.ID
	}

	// Fetch Routes for all found stops
	routesRows, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Fetch Agencies for all found stops
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// 5. Organize Data for Response Construction
	routesByStopID := make(map[string][]string)
	routesMap := make(map[string]models.Route)

	for _, row := range routesRows {
		combinedRouteID := utils.FormCombinedID(row.AgencyID, row.ID)

		routesByStopID[row.StopID] = append(routesByStopID[row.StopID], combinedRouteID)

		if _, exists := routesMap[combinedRouteID]; !exists {
			routesMap[combinedRouteID] = models.NewRoute(
				combinedRouteID,
				row.AgencyID,
				row.ShortName.String,
				row.LongName.String,
				row.Desc.String,
				models.RouteType(row.Type),
				row.Url.String,
				row.Color.String,
				row.TextColor.String,
				row.ShortName.String,
			)
		}
	}

	agenciesMap := make(map[string]models.AgencyReference)
	for _, row := range agencyRows {
		if _, exists := agenciesMap[row.ID]; !exists {
			agenciesMap[row.ID] = models.NewAgencyReference(
				row.ID,
				row.Name,
				row.Url,
				row.Timezone,
				row.Lang.String,
				row.Phone.String,
				row.Email.String,
				row.FareUrl.String,
				"",
				false,
			)
		}
	}

	// 6. Construct Stop Models
	stopModels := make([]models.Stop, 0, len(stops))

	for _, s := range stops {
		var agencyID string

		// Attempt to derive Agency ID from routes serving this stop
		if rts, ok := routesByStopID[s.ID]; ok && len(rts) > 0 {
			agencyID, _, _ = utils.ExtractAgencyIDAndCodeID(rts[0])
		} else {
			if len(agenciesMap) == 1 {
				for id := range agenciesMap {
					agencyID = id
					break
				}
			}
		}

		var combinedStopID string
		if agencyID != "" {
			combinedStopID = utils.FormCombinedID(agencyID, s.ID)
		} else {
			// Absolute fallback: Return the raw database ID rather than an empty string
			combinedStopID = s.ID
		}

		routeIDs := routesByStopID[s.ID]
		if routeIDs == nil {
			routeIDs = []string{}
		}

		stopModel := models.Stop{
			ID:                 combinedStopID,
			Name:               s.Name.String,
			Lat:                s.Lat,
			Lon:                s.Lon,
			Code:               s.Code.String,
			Direction:          s.Direction.String,
			LocationType:       int(s.LocationType.Int64),
			WheelchairBoarding: models.UnknownValue,
			RouteIDs:           routeIDs,
			StaticRouteIDs:     routeIDs,
			Parent:             "", // s.ParentStation.String if you add it to the query
		}

		if s.WheelchairBoarding.Valid {
			switch s.WheelchairBoarding.Int64 {
			case 1:
				stopModel.WheelchairBoarding = "ACCESSIBLE"
			case 2:
				stopModel.WheelchairBoarding = "NOT_ACCESSIBLE"
			}
		}

		stopModels = append(stopModels, stopModel)
	}

	// 7. Build References
	references := models.NewEmptyReferences()
	for _, r := range routesMap {
		references.Routes = append(references.Routes, r)
	}
	for _, a := range agenciesMap {
		references.Agencies = append(references.Agencies, a)
	}

	data := struct {
		LimitExceeded bool                   `json:"limitExceeded"`
		List          []models.Stop          `json:"list"`
		OutOfRange    bool                   `json:"outOfRange"`
		References    models.ReferencesModel `json:"references"`
	}{
		LimitExceeded: len(stops) >= limit,
		List:          stopModels,
		OutOfRange:    false,
		References:    references,
	}

	response := models.ResponseModel{
		Code:        200,
		CurrentTime: models.ResponseCurrentTime(),
		Version:     2,
		Text:        "OK",
		Data:        data,
	}

	api.sendResponse(w, r, response)
}
