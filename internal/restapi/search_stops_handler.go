package restapi

import (
	"net/http"
	"strconv"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

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

	// 2. Perform Full Text Search
	// Wrap in quotes to handle spaces safely and append wildcard
	searchQuery := "\"" + query + "*\""

	searchParams := gtfsdb.SearchStopsByNameParams{
		SearchQuery: searchQuery,
		Limit:       int64(limit),
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStopsByName(ctx, searchParams)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// 3. Batch Fetch Related Data (Routes & Agencies)
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

	// 4. Organize Data for Response Construction
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

	// 5. Construct Stop Models
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

	// 6. Build References
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
