package restapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	gtfsInternal "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tripsForRouteHandler returns all active trips for a route, including their real-time
// status, schedule, and vehicle positions when available.
func (api *RestAPI) tripsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	includeSchedule := r.URL.Query().Get("includeSchedule") != "false"
	includeStatus := r.URL.Query().Get("includeStatus") != "false"

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	currentLocation, err := loadAgencyLocation(currentAgency.ID, currentAgency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	timeParam := r.URL.Query().Get("time")
	formattedDate, currentTime, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Time since midnight of the service day, as a duration.
	serviceDayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
	currentSinceMidnight := max(currentTime.Sub(serviceDayMidnight), 0)

	// Check the previous day's service for trips running past midnight.
	// GTFS allows departure times > 24:00:00 (e.g., 25:30:00 = 1:30 AM next day).
	// These trips belong to yesterday's service but are still active now.
	// TODO: We should add config for runningLateWindow and runningEarlyWindow like Java OBA
	// source:https://groups.google.com/g/onebusaway-developers/c/j-G-1UyfbXI/m/J-Su3BArKW0J
	const (
		runningLate  = 30 * time.Minute // runningLateWindow
		runningEarly = 10 * time.Minute // runningEarlyWindow
	)
	prevDay := currentTime.AddDate(0, 0, -1)
	prevFormattedDate := prevDay.Format("20060102")
	prevServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, prevFormattedDate)
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch previous-day service IDs", "date", prevFormattedDate, "error", err)
		prevServiceIDs = nil
	}
	// I'm confused by adding 24 hours to get the previous day here, but that's the existing behavior.
	prevDaySinceMidnight := currentSinceMidnight + (24 * time.Hour)

	indexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
		RouteID:    routeID,
		ServiceIds: serviceIDs,
	})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	layoverIndices := api.GtfsManager.GetBlockLayoverIndicesForRoute(routeID)

	// Match Java OBA: look back 30 min (catch late vehicles) and ahead 10 min (catch early vehicles).
	timeRangeStart := currentSinceMidnight - runningLate
	timeRangeEnd := currentSinceMidnight + runningEarly

	layoverBlocks := gtfsInternal.GetBlocksInTimeRange(layoverIndices, timeRangeStart.Nanoseconds(), timeRangeEnd.Nanoseconds())

	allLinkedBlocks := make(map[string]bool)

	if len(indexIDs) > 0 {
		blocksFromIndices, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
			IndexIds:   indexIDs,
			ServiceIds: serviceIDs,
		})
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, b := range blocksFromIndices {
			if b.Valid {
				allLinkedBlocks[b.String] = true
			}
		}
	}

	for _, blockID := range layoverBlocks {
		allLinkedBlocks[blockID] = true
	}

	// Find blocks from previous day's service (for trips running past midnight).
	if len(prevServiceIDs) > 0 {
		prevIndexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
			RouteID:    routeID,
			ServiceIds: prevServiceIDs,
		})
		if err != nil {
			api.Logger.Warn("trips-for-route: failed to fetch previous-day block index IDs", "error", err)
		} else if len(prevIndexIDs) > 0 {
			prevBlocks, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
				IndexIds:   prevIndexIDs,
				ServiceIds: prevServiceIDs,
			})
			if err != nil {
				api.Logger.Warn("trips-for-route: failed to fetch previous-day blocks", "error", err)
			} else {
				for _, b := range prevBlocks {
					if b.Valid {
						allLinkedBlocks[b.String] = true
					}
				}
			}
		}
	}

	nullBlockTrips, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
		RouteID:        routeID,
		ServiceIds:     serviceIDs,
		TimeRangeStart: sql.NullInt64{Int64: timeRangeStart.Nanoseconds(), Valid: true},
		TimeRangeEnd:   sql.NullInt64{Int64: timeRangeEnd.Nanoseconds(), Valid: true},
	})
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch null-block trips", "route_id", routeID, "error", err)
		nullBlockTrips = nil
	}

	if len(prevServiceIDs) > 0 {
		prevNullBlockTrips, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
			RouteID:        routeID,
			ServiceIds:     prevServiceIDs,
			TimeRangeStart: sql.NullInt64{Int64: (prevDaySinceMidnight + timeRangeStart - currentSinceMidnight).Nanoseconds(), Valid: true},
			TimeRangeEnd:   sql.NullInt64{Int64: (prevDaySinceMidnight + timeRangeEnd - currentSinceMidnight).Nanoseconds(), Valid: true},
		})
		if err != nil {
			api.Logger.Warn("trips-for-route: failed to fetch previous-day null-block trips", "error", err)
		} else {
			nullBlockTrips = append(nullBlockTrips, prevNullBlockTrips...)
		}
	}

	if len(allLinkedBlocks) == 0 && len(nullBlockTrips) == 0 {
		references := buildTripReferences(api, ctx, includeSchedule, []models.TripsForRouteListEntry{}, []gtfsdb.Stop{}, nil)
		response := models.NewListResponseWithRange([]models.TripsForRouteListEntry{}, references, false, api.Clock, false)
		api.sendResponse(w, r, response)
		return
	}

	var activeTrips []string

	type serviceDayEntry struct {
		serviceIDs    []string
		sinceMidnight time.Duration
	}
	serviceDays := []serviceDayEntry{
		{serviceIDs: serviceIDs, sinceMidnight: currentSinceMidnight},
	}
	if len(prevServiceIDs) > 0 {
		serviceDays = append(serviceDays, serviceDayEntry{
			serviceIDs:    prevServiceIDs,
			sinceMidnight: prevDaySinceMidnight,
		})
	}

	for blockID := range allLinkedBlocks {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		blockIDNullStr := sql.NullString{String: blockID, Valid: true}

		for _, sd := range serviceDays {
			tripsInBlock, err := api.GtfsManager.GtfsDB.Queries.GetTripsInBlock(ctx, gtfsdb.GetTripsInBlockParams{
				BlockID:    blockIDNullStr,
				ServiceIds: sd.serviceIDs,
			})
			if err != nil {
				api.Logger.Warn("trips-for-route: failed to fetch trips in block", "block_id", blockID, "error", err)
				continue
			}
			if len(tripsInBlock) == 0 {
				continue
			}

			activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripInBlockAtTime(ctx, gtfsdb.GetActiveTripInBlockAtTimeParams{
				BlockID:     blockIDNullStr,
				ServiceIds:  sd.serviceIDs,
				CurrentTime: sql.NullInt64{Int64: sd.sinceMidnight.Nanoseconds(), Valid: true}})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("trips-for-route: failed to get active trip in block", "block_id", blockID, "error", err)
				continue
			}
			if errors.Is(err, sql.ErrNoRows) {
				// No currently-running trip; pick the best candidate (most recently
				// completed or next upcoming) so that blocks are never skipped.
				candidates, qErr := api.GtfsManager.GtfsDB.Queries.GetTripsInBlockWithTimeBounds(ctx, gtfsdb.GetTripsInBlockWithTimeBoundsParams{
					BlockID:    blockIDNullStr,
					ServiceIds: sd.serviceIDs,
				})
				if qErr != nil {
					api.Logger.Warn("failed to query block trip candidates", "block_id", blockIDNullStr.String, "error", qErr)
					continue
				}
				if len(candidates) == 0 {
					continue
				}
				activeTrip = selectBestTripInBlock(candidates, sd.sinceMidnight.Nanoseconds())
			}

			activeTrips = append(activeTrips, activeTrip)
			break
		}
	}

	activeTrips = append(activeTrips, nullBlockTrips...)

	tripIDsSet := make(map[string]bool)
	for _, id := range activeTrips {
		tripIDsSet[id] = true
	}
	var tripIDs []string
	for id := range tripIDsSet {
		tripIDs = append(tripIDs, id)
	}

	var fetchedTrips []gtfsdb.Trip
	if len(tripIDs) > 0 {
		fetchedTrips, err = api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	filteredRouteTrips := make(map[string]bool, len(fetchedTrips))
	n := 0
	for _, trip := range fetchedTrips {
		if trip.RouteID == routeID {
			fetchedTrips[n] = trip
			filteredRouteTrips[trip.ID] = true
			n++
		}
	}
	fetchedTrips = fetchedTrips[:n]

	tripAgencyMap := make(map[string]string)
	if len(fetchedTrips) > 0 {
		if route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID); err == nil {
			for _, trip := range fetchedTrips {
				tripAgencyMap[trip.ID] = route.AgencyID
			}
		}
	}

	todayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	stopIDsMap := make(map[string]bool)

	var result []models.TripsForRouteListEntry
	for _, fetchedTrip := range fetchedTrips {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		tripID := fetchedTrip.ID

		agencyID, ok := tripAgencyMap[tripID]
		if !ok {
			continue
		}

		var schedule *models.TripsSchedule
		var status *models.TripStatus

		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, tripID, agencyID, currentTime, currentLocation)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}

			collectStopIDsFromSchedule(schedule, stopIDsMap)
		}

		// Build status if we have a vehicle (either on this trip or we know block has vehicles)
		if includeStatus {
			var statusErr error
			status, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, todayMidnight, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed", "trip_id", tripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(r.Context(), tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}

	// Include DUPLICATED trips from real-time data.
	// DUPLICATED trips (GTFS-RT schedule_relationship=DUPLICATED) are extra runs of
	// a scheduled trip, each assigned to a different vehicle. They only exist in
	// the real-time feed and have no static DB entry.
	//
	// The trip ID format varies by feed:
	//   - Some feeds append a numeric suffix (e.g., _1083.00060) to the base trip ID
	//   - Others reuse the base trip ID as-is
	//   - Others may use entirely synthetic IDs
	// We try the full trip ID first, then fall back to stripping a numeric suffix.
	duplicatedVehicles := api.GtfsManager.GetDuplicatedVehiclesForRoute(routeID)
	for _, vehicle := range duplicatedVehicles {
		if vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
			continue
		}
		dupTripID := vehicle.Trip.ID.ID

		// Resolve the base trip ID for DB lookups.
		// Try the full ID first; if not found, strip a trailing numeric suffix
		// (e.g., ".00060") that some feeds append to distinguish duplicated runs.
		baseTripID := dupTripID
		if _, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, dupTripID); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("trips-for-route: failed to resolve DUPLICATED trip ID",
					"dup_trip_id", dupTripID, "error", err)
			}
			stripped := stripNumericSuffix(dupTripID)
			if stripped != dupTripID {
				baseTripID = stripped
			}
		}

		var schedule *models.TripsSchedule
		if includeSchedule {
			var schedErr error
			schedule, schedErr = api.buildScheduleForTrip(ctx, baseTripID, agencyID, currentTime, currentLocation)
			if schedErr != nil {
				api.serverErrorResponse(w, r, schedErr)
				return
			}
			collectStopIDsFromSchedule(schedule, stopIDsMap)
		}

		var status *models.TripStatus
		if includeStatus {
			var statusErr error
			status, statusErr = api.BuildTripStatus(ctx, agencyID, baseTripID, &vehicle, todayMidnight, currentTime)
			if statusErr != nil {
				api.Logger.Warn("BuildTripStatus failed for DUPLICATED trip", "trip_id", baseTripID, "error", statusErr)
				status = nil
			}
		}

		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(r.Context(), baseTripID),
			TripId:       utils.FormCombinedID(agencyID, dupTripID),
		}
		result = append(result, entry)

		if !filteredRouteTrips[baseTripID] {
			baseTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, baseTripID)
			if err == nil {
				fetchedTrips = append(fetchedTrips, baseTrip)
				filteredRouteTrips[baseTripID] = true
			}
		}
	}

	if result == nil {
		result = []models.TripsForRouteListEntry{}
	}

	var stops []gtfsdb.Stop
	if len(stopIDsMap) > 0 {
		stopIDs := make([]string, 0, len(stopIDsMap))
		for stopID := range stopIDsMap {
			stopIDs = append(stopIDs, stopID)
		}
		var err error
		stops, err = api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
		if err != nil {
			api.Logger.Warn("failed to fetch stops for references", "error", err, "count", len(stopIDs))
			stops = []gtfsdb.Stop{}
		}
	}

	references := buildTripReferences(api, ctx, includeSchedule, result, stops, fetchedTrips)
	response := models.NewListResponseWithRange(result, references, false, api.Clock, false)
	api.sendResponse(w, r, response)
}

func collectStopIDsFromSchedule(schedule *models.TripsSchedule, stopIDsMap map[string]bool) {
	if schedule == nil {
		return
	}
	for _, stopTime := range schedule.StopTimes {
		_, stopID, err := utils.ExtractAgencyIDAndCodeID(stopTime.StopID)
		if err == nil {
			stopIDsMap[stopID] = true
		}
	}
}

func buildTripReferences(
	api *RestAPI,
	ctx context.Context,
	includeTrip bool,
	trips []models.TripsForRouteListEntry,
	stops []gtfsdb.Stop,
	preFetchedTrips []gtfsdb.Trip,
) models.ReferencesModel {

	presentTrips := make(map[string]models.Trip)
	presentRoutes := make(map[string]models.Route)

	for _, trip := range preFetchedTrips {
		presentTrips[trip.ID] = models.Trip{
			ID:            trip.ID,
			RouteID:       trip.RouteID,
			ServiceID:     trip.ServiceID,
			TripHeadsign:  trip.TripHeadsign.String,
			TripShortName: trip.TripShortName.String,
			DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
			BlockID:       trip.BlockID.String,
			ShapeID:       trip.ShapeID.String,
		}
		presentRoutes[trip.RouteID] = models.Route{}
	}

	for _, trip := range trips {
		_, tripID, _ := utils.ExtractAgencyIDAndCodeID(trip.GetTripId())
		if _, exists := presentTrips[tripID]; !exists {
			presentTrips[tripID] = models.Trip{}
		}
	}

	for _, entry := range trips {
		if entry.Schedule != nil {
			if entry.Schedule.NextTripId != "" {
				_, nextTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.NextTripId)
				if err == nil {
					if _, exists := presentTrips[nextTripID]; !exists {
						presentTrips[nextTripID] = models.Trip{}
					}
				}
			}
			if entry.Schedule.PreviousTripId != "" {
				_, prevTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Schedule.PreviousTripId)
				if err == nil {
					if _, exists := presentTrips[prevTripID]; !exists {
						presentTrips[prevTripID] = models.Trip{}
					}
				}
			}
		}

		if entry.Status != nil && entry.Status.ActiveTripID != "" {
			_, activeTripID, err := utils.ExtractAgencyIDAndCodeID(entry.Status.ActiveTripID)
			if err == nil {
				if _, exists := presentTrips[activeTripID]; !exists {
					presentTrips[activeTripID] = models.Trip{}
				}
			}
		}
	}

	var tripIDsToFetch []string
	for id, t := range presentTrips {
		if t.ID == "" {
			tripIDsToFetch = append(tripIDsToFetch, id)
		}
	}

	if len(tripIDsToFetch) > 0 {
		extraTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDsToFetch)
		if err != nil {
			logging.LogError(api.Logger, "failed to fetch trips for references", err)
		}

		for _, trip := range extraTrips {
			presentTrips[trip.ID] = models.Trip{
				ID:            trip.ID,
				RouteID:       trip.RouteID,
				ServiceID:     trip.ServiceID,
				TripHeadsign:  trip.TripHeadsign.String,
				TripShortName: trip.TripShortName.String,
				DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
				BlockID:       trip.BlockID.String,
				ShapeID:       trip.ShapeID.String,
			}
			presentRoutes[trip.RouteID] = models.Route{}
		}
	}

	var routeIDsToFetch []string
	for id := range presentRoutes {
		routeIDsToFetch = append(routeIDsToFetch, id)
	}

	presentAgencies := make(map[string]models.AgencyReference)

	if len(routeIDsToFetch) > 0 {
		fetchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDsToFetch)
		if err != nil {
			logging.LogError(api.Logger, "failed to fetch routes for references", err)
		}

		for _, route := range fetchedRoutes {
			presentRoutes[route.ID] = models.NewRoute(
				utils.FormCombinedID(route.AgencyID, route.ID),
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String)

			if _, exists := presentAgencies[route.AgencyID]; !exists {
				agency, err := api.GtfsManager.FindAgency(ctx, route.AgencyID)
				if err != nil {
					logging.LogError(api.Logger, "failed to fetch agency for references", err, slog.String("agency", route.AgencyID))
				}

				if agency != nil {
					presentAgencies[agency.ID] = models.AgencyReferenceFromDatabase(agency)
				}
			}
		}
	}

	stopRouteIDs := make(map[string][]string)
	if len(stops) > 0 {
		stopIDs := make([]string, len(stops))
		for i, s := range stops {
			stopIDs[i] = s.ID
		}
		if rows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs); err == nil {
			for _, row := range rows {
				if rid, ok := row.RouteID.(string); ok {
					stopRouteIDs[row.StopID] = append(stopRouteIDs[row.StopID], rid)
				}
			}
		}
	}

	stopList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIdsString := stopRouteIDs[stop.ID]
		if routeIdsString == nil {
			routeIdsString = []string{}
		}

		direction := models.UnknownValue
		if stop.Direction.Valid && stop.Direction.String != "" {
			direction = stop.Direction.String
		}

		stopList = append(stopList, models.Stop{
			Code:               utils.NullStringOrEmpty(stop.Code),
			Direction:          direction,
			ID:                 stop.ID,
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			LocationType:       0,
			Name:               utils.NullStringOrEmpty(stop.Name),
			Parent:             "",
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}

	tripsRefList := make([]models.Trip, 0, len(presentTrips))
	if includeTrip {
		for _, trip := range presentTrips {
			// Ensure we have the route to get the Agency ID
			if route, ok := presentRoutes[trip.RouteID]; ok {
				currentAgency := route.AgencyID
				tripsRefList = append(tripsRefList, models.Trip{
					ID:            utils.FormCombinedID(currentAgency, trip.ID),
					RouteID:       utils.FormCombinedID(currentAgency, trip.RouteID),
					ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
					TripHeadsign:  trip.TripHeadsign,
					TripShortName: trip.TripShortName,
					DirectionID:   trip.DirectionID,
					BlockID:       utils.FormCombinedID(currentAgency, trip.BlockID),
					ShapeID:       utils.FormCombinedID(currentAgency, trip.ShapeID),
					PeakOffPeak:   0,
					TimeZone:      "",
				})
			}
		}
	}

	// Convert maps to slices for response
	routes := make([]models.Route, 0, len(presentRoutes))
	for _, route := range presentRoutes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}

	agencyList := utils.MapValues(presentAgencies)

	references := models.NewEmptyReferences()
	references.Agencies = agencyList
	references.Routes = routes
	references.Stops = stopList
	references.Trips = tripsRefList
	return *references
}

// selectBestTripInBlock picks the most relevant trip from a block when no trip
// is currently running (GetActiveTripInBlockAtTime returned ErrNoRows). Priority:
//  1. Most recently completed (max_departure < now → highest max_departure)
//  2. Next upcoming    (min_arrival > now  → lowest  min_arrival)
//  3. Fallback: first row
func selectBestTripInBlock(rows []gtfsdb.GetTripsInBlockWithTimeBoundsRow, nowNanos int64) string {
	bestID := ""
	bestDep := int64(-1)
	for _, r := range rows {
		if r.MaxDepartureTime.Valid && r.MaxDepartureTime.Int64 < nowNanos {
			if r.MaxDepartureTime.Int64 > bestDep {
				bestDep = r.MaxDepartureTime.Int64
				bestID = r.ID
			}
		}
	}
	if bestID != "" {
		return bestID
	}
	bestArr := int64(-1)
	for _, r := range rows {
		if r.MinArrivalTime.Valid && r.MinArrivalTime.Int64 > nowNanos {
			if bestArr == -1 || r.MinArrivalTime.Int64 < bestArr {
				bestArr = r.MinArrivalTime.Int64
				bestID = r.ID
			}
		}
	}
	if bestID != "" {
		return bestID
	}
	return rows[0].ID
}

// stripNumericSuffix removes a trailing ".<digits>" from a trip ID.
// Some GTFS-RT feeds append a numeric suffix to DUPLICATED trip IDs to
// distinguish individual runs (e.g., "LLR_..._1083.00060" -> "LLR_..._1083").
// If the ID has no dot, or the part after the last dot contains non-digits,
// the original string is returned unchanged.
func stripNumericSuffix(tripID string) string {
	idx := strings.LastIndex(tripID, ".")
	if idx == -1 || idx == len(tripID)-1 {
		return tripID
	}
	suffix := tripID[idx+1:]
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return tripID
		}
	}
	return tripID[:idx]
}
