package restapi

import (
	"net/http"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) searchStopHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	input := queryParams.Get("input")
	if input == "" {
		api.validationErrorResponse(w, r, map[string][]string{
			"input": {"input is required"},
		})
		return
	}

	sanitizedQuery := sanitizeFTS5Query(input)

	maxCount := 20
	fieldErrors := make(map[string][]string)
	if maxCountStr := queryParams.Get("maxCount"); maxCountStr != "" {
		parsed, err := utils.ParseFloatParam(queryParams, "maxCount", fieldErrors)
		if err == nil {
			maxCount = int(parsed)
			if maxCount <= 0 {
				fieldErrors["maxCount"] = []string{"must be greater than zero"}
			} else if maxCount > 250 {
				fieldErrors["maxCount"] = []string{"must not exceed 250"}
			}
		}
	}

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()

	// 1️⃣ Fetch stops
	stops, err := api.GtfsManager.GtfsDB.Queries.SearchStops(ctx, gtfsdb.SearchStopsParams{
		Query: sanitizedQuery,
		Limit: int64(maxCount),
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	

	if len(stops) == 0 {
		api.sendResponse(w, r,
			models.NewListResponseWithRange([]models.Stop{}, models.NewEmptyReferences(), false),
		)
		return
	}

	// 2️⃣ Collect stop IDs
	stopIDs := make([]string, len(stops))
	for i, s := range stops {
		stopIDs[i] = s.ID
	}

	// 3️⃣ Fetch routes & agencies (best-effort)
	routeIDsForStops, _ := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	agenciesForStops, _ := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)

	stopRouteIDs := make(map[string][]string)
	stopAgency := make(map[string]*gtfsdb.GetAgenciesForStopsRow)

	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}

	for _, row := range routeIDsForStops {
		routeIDStr, ok := row.RouteID.(string)
		if !ok {
			continue
		}
		stopRouteIDs[row.StopID] = append(stopRouteIDs[row.StopID], routeIDStr)

		agencyID, routeID, _ := utils.ExtractAgencyIDAndCodeID(routeIDStr)
		agencyIDs[agencyID] = true
		routeIDs[routeID] = true
	}

	for _, row := range agenciesForStops {
		if _, exists := stopAgency[row.StopID]; !exists {
			tmp := row
			stopAgency[row.StopID] = &tmp
		}
	}

	// 4️⃣ Build results (FIXED LOGIC)
	results := make([]models.Stop, 0, len(stops))

	for _, stop := range stops {
		rids := stopRouteIDs[stop.ID]
		agency := stopAgency[stop.ID]

		// Direction ONLY for platforms
		direction := ""
		if stop.LocationType.Valid && stop.LocationType.Int64 == 5 {
			if stop.Desc.Valid {
				direction = stop.Desc.String
			}
		}

		// Stop code
		stopCode := ""
		if stop.Code.Valid {
			stopCode = stop.Code.String
		}

		// Wheelchair boarding
		wheelchair := ""
		if stop.WheelchairBoarding.Valid {
			switch stop.WheelchairBoarding.Int64 {
			case 1:
				wheelchair = "ACCESSIBLE"
			case 2:
				wheelchair = "NOT_ACCESSIBLE"
			}
		}

		// Agency-safe combined ID
		agencyID := ""
		if agency != nil {
			agencyID = agency.ID
		}

		results = append(results, models.NewStop(
			stopCode,
			direction,
			utils.FormCombinedID(agencyID, stop.ID),
			utils.NullStringOrEmpty(stop.Name),
			"",
			wheelchair,
			stop.Lat,
			stop.Lon,
			int(stop.LocationType.Int64),
			rids,
			rids,
		))
	}

	// 5️⃣ Build references
	agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)
	routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)

	references := models.ReferencesModel{
		Agencies:   agencies,
		Routes:     routes,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      []models.Stop{},
		Trips:      []interface{}{},
	}

	api.sendResponse(w, r,
		models.NewListResponseWithRange(results, references, false),
	)
}

func sanitizeFTS5Query(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	parts := strings.Fields(input)
	for i, p := range parts {
		p = strings.ReplaceAll(p, `"`, `""`)
		parts[i] = p + "*"
	}
	return strings.Join(parts, " ")
}
