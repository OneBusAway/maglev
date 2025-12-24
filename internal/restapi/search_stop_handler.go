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
        fieldErrors := map[string][]string{
            "input": {"input is required"},
        }
        api.validationErrorResponse(w, r, fieldErrors)
        return
    }

    sanitizedQuery := sanitizeFTS5Query(input)

    maxCount := 20
    fieldErrors := make(map[string][]string)
    if maxCountStr := queryParams.Get("maxCount"); maxCountStr != "" {
        parsedMaxCount, err := utils.ParseFloatParam(queryParams, "maxCount", fieldErrors)
        if err == nil {
            maxCount = int(parsedMaxCount)
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

    // 1. Get Stops from DB
    stops, err := api.GtfsManager.GtfsDB.Queries.SearchStops(ctx, gtfsdb.SearchStopsParams{
        Query: sanitizedQuery,
        Limit: int64(maxCount),
    })
    if err != nil {
        api.serverErrorResponse(w, r, err)
        return
    }

    if len(stops) == 0 {
        response := models.NewListResponseWithRange([]models.Stop{}, models.NewEmptyReferences(), false)
        api.sendResponse(w, r, response)
        return
    }

    // 2. Batch Fetch References
    stopIDs := make([]string, len(stops))
    for i, stop := range stops {
        stopIDs[i] = stop.ID
    }

    routeIDsForStops, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
    if err != nil {
        api.serverErrorResponse(w, r, err)
        return
    }

    agenciesForStops, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
    if err != nil {
        api.serverErrorResponse(w, r, err)
        return
    }

    stopRouteIDs := make(map[string][]string)
    stopAgency := make(map[string]*gtfsdb.GetAgenciesForStopsRow)
    routeIDs := map[string]bool{}
    agencyIDs := map[string]bool{}

    for _, routeIDRow := range routeIDsForStops {
        stopID := routeIDRow.StopID
        routeIDStr, ok := routeIDRow.RouteID.(string)
        if !ok {
            continue
        }

        agencyId, routeId, _ := utils.ExtractAgencyIDAndCodeID(routeIDStr)
        stopRouteIDs[stopID] = append(stopRouteIDs[stopID], routeIDStr)
        agencyIDs[agencyId] = true
        routeIDs[routeId] = true
    }

    for _, agencyRow := range agenciesForStops {
        stopID := agencyRow.StopID
        if _, exists := stopAgency[stopID]; !exists {
            stopAgency[stopID] = &agencyRow
        }
    }

    // 3. Build Results (THIS IS THE PART THAT FIXES YOUR EMPTY FIELDS)
    var results []models.Stop
    for _, stop := range stops {
        rids := stopRouteIDs[stop.ID]
        agency := stopAgency[stop.ID]

        if agency == nil {
            continue
        }

        // Logic to handle Direction
        direction := models.UnknownValue
        if stop.Desc.Valid && stop.Desc.String != "" {
            direction = stop.Desc.String
        }

        // Logic to handle Stop Code (Fixes "code": "")
        stopCode := ""
        if stop.Code.Valid {
            stopCode = stop.Code.String
        }

        // Logic to handle Wheelchair Boarding (Fixes "wheelchairBoarding": "")
        // GTFS: 0=Unknown, 1=Accessible, 2=Not Accessible
        wcStatus := "UNKNOWN" 
        if stop.WheelchairBoarding.Valid {
            switch stop.WheelchairBoarding.Int64 {
            case 1:
                wcStatus = "ACCESSIBLE"
            case 2:
                wcStatus = "NOT_ACCESSIBLE"
            default:
                wcStatus = "UNKNOWN"
            }
        }

        results = append(results, models.NewStop(
            stopCode,                                // Pass the mapped code
            direction,
            utils.FormCombinedID(agency.ID, stop.ID),
            utils.NullStringOrEmpty(stop.Name),
            "", 
            wcStatus,                                // Pass the mapped string
            stop.Lat,
            stop.Lon,
            int(stop.LocationType.Int64),
            rids,
            rids,
        ))
    }

    // 4. Build References
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

    response := models.NewListResponseWithRange(results, references, false)
    api.sendResponse(w, r, response)
}

// sanitizeFTS5Query sanitizes user input for FTS5 MATCH queries
func sanitizeFTS5Query(input string) string {
    input = strings.TrimSpace(input)
    if input == "" {
        return ""
    }

    // 1. Escape double quotes to prevent syntax errors
    replacer := strings.NewReplacer(`"`, `""`)
    sanitized := replacer.Replace(input)

    // 2. Wrap in quotes and asterisks
    // The query string becomes: "*Roosevelt*"
    // This tells FTS5 to find this substring anywhere in the token.
    return `"*` + sanitized + `*"`
}