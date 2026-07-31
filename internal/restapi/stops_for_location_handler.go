package restapi

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// stopsForLocationHandler returns stops near a geographic location, specified by
// lat/lon coordinates with an optional radius or latSpan/lonSpan bounding box.
func (api *RestAPI) stopsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	var fieldErrors map[string][]string
	loc, fieldErrors := api.parseLocationParams(r, fieldErrors)
	maxCount, fieldErrors := utils.ParseMaxCountClamped(queryParams, models.DefaultMaxCountForStops, fieldErrors)
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

	// Sanitize query
	query = utils.SanitizeInput(query)

	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	allAgencies, err := api.GtfsManager.GetAgencies(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Bounds that miss every agency's coverage area yield an empty list, so no search
	// runs — otherwise a stop-code query would fall back to its closest match worldwide.
	outOfRange := api.GtfsManager.CheckIfOutOfBounds(loc)

	var stops []gtfsdb.Stop
	var limitExceeded bool
	if !outOfRange {
		stops, limitExceeded = api.GtfsManager.GetStopsForLocation(ctx, loc, query, maxCount, routeTypes)
	}

	results := []models.Stop{}
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
		agencies := utils.FilterAgencies(allAgencies, agencyIDs)
		if agencies == nil {
			agencies = []models.AgencyReference{}
		}

		routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)
		if routes == nil {
			routes = []models.Route{}
		}

		references := models.NewEmptyReferences()
		references.Agencies = agencies
		references.Routes = routes
		response := models.NewListResponseWithRange(results, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, false)
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

	stopRouteIDs, err := api.routeIDsByStop(ctx, stopIDs, query, activeServiceIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	for _, routeIDsForStop := range stopRouteIDs {
		for _, routeID := range routeIDsForStop {
			agencyID, _, err := utils.ExtractAgencyIDAndCodeID(routeID)
			if err != nil {
				continue
			}
			agencyIDs[agencyID] = true
			routeIDs[routeID] = true
		}
	}

	// Batch query to get agencies for all stops
	agenciesForStops, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stopAgency := make(map[string]*gtfsdb.GetAgenciesForStopsRow)

	// Group agencies by stop (take the first agency for each stop)
	for _, agencyRow := range agenciesForStops {
		stopID := agencyRow.StopID
		if _, exists := stopAgency[stopID]; !exists {
			stopAgency[stopID] = &agencyRow
		}
	}

	isLimitExceeded := limitExceeded
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

		// A stop-code lookup still resolves the stop when nothing is scheduled that day.
		if agency == nil || (len(rids) == 0 && query == "") {
			continue
		}

		resultRawStopIDs = append(resultRawStopIDs, stopID)

		direction := api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		results = append(results, models.NewStop(
			nulls.StringOrEmpty(stop.Code),
			direction,
			utils.FormCombinedID(agency.ID, stop.ID),
			nulls.StringOrEmpty(stop.Name),
			"",
			utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
			stop.Lat,
			stop.Lon,
			0,
			rids,
			rids,
		))
	}

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	// Spec: results are ordered lexicographically by combined stop ID.
	slices.SortStableFunc(results, func(a, b models.Stop) int {
		return cmp.Compare(a.ID, b.ID)
	})

	agencies := utils.FilterAgencies(allAgencies, agencyIDs)
	routes := utils.FilterRoutes(api.GtfsManager.GtfsDB.Queries, ctx, routeIDs)

	if agencies == nil {
		agencies = []models.AgencyReference{}
	}
	if routes == nil {
		routes = []models.Route{}
	}

	// Populate situation references for alerts affecting the returned stops
	alerts := api.collectAlertsForStops(resultRawStopIDs)
	situations := api.BuildSituationReferences(alerts)

	references := models.NewEmptyReferences()
	references.Agencies = agencies
	references.Routes = routes
	references.Situations = situations

	response := models.NewListResponseWithRange(results, *references, api.GtfsManager.CheckIfOutOfBounds(loc), api.Clock, isLimitExceeded)
	api.sendResponse(w, r, response)
}

// routeIDsByStop maps each stop to the routes serving it. Stop-code queries report every
// route serving the stop, matching legacy; geospatial searches report only the routes
// active on the queried date.
func (api *RestAPI) routeIDsByStop(
	ctx context.Context,
	stopIDs []string,
	stopCodeQuery string,
	activeServiceIDs []string,
) (map[string][]string, error) {
	byStop := make(map[string][]string)
	collect := func(stopID string, routeID interface{}) {
		routeIDStr, ok := routeID.(string)
		if !ok {
			api.Logger.Warn("unexpected RouteID type", "stopID", stopID, "routeID", routeID)
			return
		}
		if _, _, err := utils.ExtractAgencyIDAndCodeID(routeIDStr); err != nil {
			return
		}
		byStop[stopID] = append(byStop[stopID], routeIDStr)
	}

	if stopCodeQuery != "" {
		rows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			collect(row.StopID, row.RouteID)
		}
		return byStop, nil
	}

	if len(activeServiceIDs) == 0 {
		return byStop, nil
	}

	rows, err := api.GtfsManager.GtfsDB.Queries.GetActiveRouteIDsForStopsOnDate(ctx, gtfsdb.GetActiveRouteIDsForStopsOnDateParams{
		StopIds:    stopIDs,
		ServiceIds: activeServiceIDs,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		collect(row.StopID, row.RouteID)
	}
	return byStop, nil
}
