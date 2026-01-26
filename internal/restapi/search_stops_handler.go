package restapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// sanitizeFTS5Query removes special FTS5 characters by replacing them with spaces
// to prevent query syntax errors. Does not preserve the original characters.
func sanitizeFTS5Query(input string) string {
	replacer := strings.NewReplacer(
		`"`, " ",
		`*`, " ",
		`(`, " ",
		`)`, " ",
	)

	sanitized := replacer.Replace(input)

	// Remove AND / OR / NOT tokens anywhere (case-insensitive, word-boundary based)
	re := regexp.MustCompile(`(?i)\b(AND|OR|NOT)\b`)
	sanitized = re.ReplaceAllString(sanitized, " ")

	// Trim and collapse whitespace
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

	searchQuery := `"` + sanitizedQuery + `*"`

	searchParams := gtfsdb.SearchStopsByNameParams{
		SearchQuery: searchQuery,
		Limit:       int64(limit),
	}

	// 3. Perform Full Text Search (with logged fallback)
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
	if err != nil {

		api.Logger.Debug(
			"FTS5 wildcard query failed, retrying without wildcard",
			"original_error", err,
			"query", sanitizedQuery,
		)

		searchQuery = `"` + sanitizedQuery + `"`
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
	routesMap := make(map[string]models.Route)

	for _, row := range routesRows {
		combinedRouteID := utils.FormCombinedID(row.AgencyID, row.ID)

		routesByStopID[row.StopID] = append(routesByStopID[row.StopID], combinedRouteID)

		if _, exists := routesMap[combinedRouteID]; !exists {

			shortName := ""
			if row.ShortName.Valid {
				shortName = row.ShortName.String
			}

			longName := ""
			if row.LongName.Valid {
				longName = row.LongName.String
			}

			desc := ""
			if row.Desc.Valid {
				desc = row.Desc.String
			}

			url := ""
			if row.Url.Valid {
				url = row.Url.String
			}

			color := ""
			if row.Color.Valid {
				color = row.Color.String
			}

			textColor := ""
			if row.TextColor.Valid {
				textColor = row.TextColor.String
			}

			routesMap[combinedRouteID] = models.NewRoute(
				combinedRouteID,
				row.AgencyID,
				shortName,
				longName,
				desc,
				models.RouteType(row.Type),
				url,
				color,
				textColor,
				shortName,
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

		if rts, ok := routesByStopID[s.ID]; ok && len(rts) > 0 {
			agencyID, _, _ = utils.ExtractAgencyIDAndCodeID(rts[0])
		} else if len(agenciesMap) == 1 {
			for id := range agenciesMap {
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
