package restapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) stopsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseRequiredFloatParam(queryParams, "lat", nil)
	lon, _ := utils.ParseRequiredFloatParam(queryParams, "lon", fieldErrors)
	radius, _ := utils.ParseFloatParam(queryParams, "radius", fieldErrors)
	latSpan, _ := utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ := utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)
	maxCount, _ := utils.ParseMaxCount(queryParams, models.DefaultMaxCountForStops, fieldErrors)
	query := queryParams.Get("query")

	var routeTypes []int
	if routeTypeStr := queryParams.Get("routeType"); routeTypeStr != "" {
		routeTypeStrs := strings.Split(routeTypeStr, ",")

		const maxRouteTypeTokens = 100

		if len(routeTypeStrs) > maxRouteTypeTokens {
			if fieldErrors == nil {
				fieldErrors = make(map[string][]string)
			}
			fieldErrors["routeType"] = []string{
				fmt.Sprintf("too many route types (maximum %d allowed)", maxRouteTypeTokens),
			}
		} else {
			for _, rtStr := range routeTypeStrs {
				rtStr = strings.TrimSpace(rtStr)
				if rtStr != "" {
					var rt int
					if _, err := fmt.Sscanf(rtStr, "%d", &rt); err != nil {
						if fieldErrors == nil {
							fieldErrors = make(map[string][]string)
						}
						if _, exists := fieldErrors["routeType"]; !exists {
							fieldErrors["routeType"] = []string{
								`Invalid field value for field "routeType".`,
							}
						}
					} else {
						routeTypes = append(routeTypes, rt)
					}
				}
			}
		}
	}

	queryTime := api.Clock.Now()

	if timeStr := queryParams.Get("time"); timeStr != "" {
		var timeMs int64
		if _, err := fmt.Sscanf(timeStr, "%d", &timeMs); err == nil {
			// Bin to 15 minutes
			binnedMs := timeMs - (timeMs % 900000)
			queryTime = time.UnixMilli(binnedMs)
		}
	}

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	locationErrors := utils.ValidateLocationParams(lat, lon, radius, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return
	}

	// Validate and sanitize query
	sanitizedQuery, err := utils.ValidateAndSanitizeQuery(query)
	if err != nil {
		fieldErrors := map[string][]string{
			"query": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}
	query = sanitizedQuery

	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, radius, latSpan, lonSpan, query, maxCount, false, routeTypes, queryTime)

	// Referenced Java code: "here we sort by distance for possible truncation, but later it will be re-sorted by stopId"
	sort.SliceStable(stops, func(i, j int) bool {
		return stops[i].ID < stops[j].ID
	})

	var results []models.Stop
	routeIDs := map[string]bool{}
	agencyIDs := map[string]bool{}

	stopIDs := make([]string, 0, len(stops))
	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stops {
		stopIDs = append(stopIDs, stop.ID)
		stopMap[stop.ID] = stop
	}

	if len(stopIDs) == 0 {
		// Return empty response if no stops found
		agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)
		routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)
		references := models.ReferencesModel{
			Agencies:   agencies,
			Routes:     routes,
			Situations: []models.Situation{},
			StopTimes:  []models.RouteStopTime{},
			Stops:      []models.Stop{},
			Trips:      []models.Trip{},
		}
		response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, lat, lon, latSpan, lonSpan, radius), api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	// Get active service IDs for the requested queryTime
	currentDate := queryTime.Format("20060102")
	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, currentDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Batch query to get route IDs for all stops, strictly filtered by active service IDs
	var routeIDsForStops []gtfsdb.GetActiveRouteIDsForStopsOnDateRow
	if len(activeServiceIDs) > 0 {
		routeIDsForStops, err = api.GtfsManager.GtfsDB.Queries.GetActiveRouteIDsForStopsOnDate(ctx, gtfsdb.GetActiveRouteIDsForStopsOnDateParams{
			StopIds:    stopIDs,
			ServiceIds: activeServiceIDs,
		})
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	// Batch query to get agencies for all stops
	agenciesForStops, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Create maps for efficient lookup
	stopRouteIDs := make(map[string][]string)
	stopAgency := make(map[string]*gtfsdb.GetAgenciesForStopsRow)

	for _, routeIDRow := range routeIDsForStops {
		stopID := routeIDRow.StopID
		routeIDStr, ok := routeIDRow.RouteID.(string)
		if !ok {
			api.Logger.Warn("unexpected RouteID type",
				"stopID", stopID,
				"routeID", routeIDRow.RouteID,
			)
			continue
		}

		agencyId, _, err := utils.ExtractAgencyIDAndCodeID(routeIDStr)
		if err != nil {
			continue // Skip malformed route IDs
		}
		stopRouteIDs[stopID] = append(stopRouteIDs[stopID], routeIDStr)
		agencyIDs[agencyId] = true
		routeIDs[routeIDStr] = true
	}

	// Group agencies by stop (take the first agency for each stop)
	for _, agencyRow := range agenciesForStops {
		stopID := agencyRow.StopID
		if _, exists := stopAgency[stopID]; !exists {
			stopAgency[stopID] = &agencyRow
		}
	}

	isLimitExceeded := false
	var resultRawStopIDs []string

	// Build results using the pre-fetched data
	for _, stopID := range stopIDs {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		stop := stopMap[stopID]
		rids := stopRouteIDs[stopID]
		agency := stopAgency[stopID]

		if len(rids) == 0 || agency == nil {
			continue
		}

		resultRawStopIDs = append(resultRawStopIDs, stopID)

		direction := api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		results = append(results, models.NewStop(
			utils.NullStringOrEmpty(stop.Code),
			direction,
			utils.FormCombinedID(agency.ID, stop.ID),
			utils.NullStringOrEmpty(stop.Name),
			"",
			utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
			stop.Lat,
			stop.Lon,
			0,
			rids,
			rids,
		))
		if len(results) >= maxCount {
			isLimitExceeded = true
			break
		}
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencies := utils.FilterAgencies(api.GtfsManager.GetAgencies(), agencyIDs)
	routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)

	// Populate situation references for alerts affecting the returned stops
	alerts := api.collectAlertsForStops(resultRawStopIDs)
	situations := api.BuildSituationReferences(alerts)

	references := models.ReferencesModel{
		Agencies:   agencies,
		Routes:     routes,
		Situations: situations,
		StopTimes:  []models.RouteStopTime{},
		Stops:      []models.Stop{},
		Trips:      []models.Trip{},
	}

	response := models.NewListResponseWithRange(results, references, checkIfOutOfBounds(api, lat, lon, latSpan, lonSpan, radius), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}
