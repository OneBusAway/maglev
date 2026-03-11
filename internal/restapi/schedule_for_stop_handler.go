package restapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	gtfsInternal "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) scheduleForStopHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, stopID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

	// Get the date parameter or use current date
	dateParam := r.URL.Query().Get("date")

	// Validate date parameter
	if err := utils.ValidateDate(dateParam); err != nil {
		fieldErrors := map[string][]string{
			"date": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	loc := utils.LoadLocationWithUTCFallBack(agency.Timezone, agency.ID)
	var date int64
	var targetDate string
	var weekday string

	if dateParam != "" {
		parsedDate, err := time.ParseInLocation("2006-01-02", dateParam, loc)
		if err != nil {
			fieldErrors := map[string][]string{
				"date": {"Invalid date format. Use YYYY-MM-DD"},
			}
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		date = parsedDate.UnixMilli()
		targetDate = parsedDate.Format("20060102")
		weekday = strings.ToLower(parsedDate.Weekday().String())
	} else {
		now := api.Clock.Now().In(loc)
		y, m, d := now.Date()
		startOfDay := time.Date(y, m, d, 0, 0, 0, 0, loc)
		date = startOfDay.UnixMilli()
		targetDate = startOfDay.Format("20060102")
		weekday = strings.ToLower(startOfDay.Weekday().String())
	}

	// Verify stop exists
	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	routesForStop, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routeIDs := make([]string, 0, len(routesForStop))
	for _, rt := range routesForStop {
		routeIDs = append(routeIDs, rt.ID)
	}

	if len(routeIDs) == 0 {
		api.sendResponse(w, r, models.NewEntryResponse(
			models.NewScheduleForStopEntry(utils.FormCombinedID(agencyID, stopID), date, nil),
			models.NewEmptyReferences(),
			api.Clock,
		))
		return
	}

	params := gtfsdb.GetScheduleForStopOnDateParams{
		StopID:     stopID,
		TargetDate: targetDate,
		Weekday:    weekday,
		RouteIds:   routeIDs,
	}
	scheduleRows, err := api.GtfsManager.GtfsDB.Queries.GetScheduleForStopOnDate(ctx, params)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Build references maps
	agencyRefs := make(map[string]models.AgencyReference)

	// add the already fetched agency
	agencyRefs[agencyID] = models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",    // disclaimer
		false, // privateService
	)

	routeRefs := make(map[string]models.Route)
	tripIDsSet := make(map[string]bool)

	// Pre-process to gather unique IDs for batch fetching
	uniqueRouteIDsMap := make(map[string]bool)
	uniqueAgencyIDsMap := make(map[string]bool)

	for _, row := range scheduleRows {
		uniqueRouteIDsMap[row.RouteID] = true
		uniqueAgencyIDsMap[row.AgencyID] = true
	}

	// Batch fetch routes
	routeIDsToFetch := make([]string, 0, len(uniqueRouteIDsMap))
	for routeID := range uniqueRouteIDsMap {
		routeIDsToFetch = append(routeIDsToFetch, routeID)
	}

	if len(routeIDsToFetch) > 0 {
		fetchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDsToFetch)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		for _, route := range fetchedRoutes {
			combinedRouteID := utils.FormCombinedID(agencyID, route.ID)
			routeRefs[combinedRouteID] = models.NewRoute(
				combinedRouteID,
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String)
		}
	}

	// Batch fetch agencies using cached manager
	allAgencies := api.GtfsManager.GetAgencies()
	for _, a := range allAgencies {
		if uniqueAgencyIDsMap[a.Id] {
			if _, exists := agencyRefs[a.Id]; !exists {
				agencyRefs[a.Id] = models.NewAgencyReference(
					a.Id,
					a.Name,
					a.Url,
					a.Timezone,
					a.Language,
					a.Phone,
					a.Email,
					a.FareUrl,
					"",    // disclaimer
					false, // privateService
				)
			}
		}
	}

	// Group schedule data by route
	routeScheduleMap := make(map[string][]models.ScheduleStopTime)
	// Track headsign counts to pick the most common one
	routeHeadsignCounts := make(map[string]map[string]int)

	for _, row := range scheduleRows {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}

		combinedRouteID := utils.FormCombinedID(agencyID, row.RouteID)
		combinedTripID := utils.FormCombinedID(agencyID, row.TripID)

		tripIDsSet[row.TripID] = true

		// Convert GTFS time (nanoseconds since midnight) to Unix timestamp in the agency's timezone in milliseconds
		// GTFS times are stored as time.Duration values (nanoseconds), need to add to the target date
		startOfDay := time.UnixMilli(date).In(loc)
		arrivalDuration := time.Duration(row.ArrivalTime)
		departureDuration := time.Duration(row.DepartureTime)
		arrivalTimeMs := startOfDay.Add(arrivalDuration).UnixMilli()
		departureTimeMs := startOfDay.Add(departureDuration).UnixMilli()

		stopTime := models.NewScheduleStopTime(
			arrivalTimeMs,
			departureTimeMs,
			utils.FormCombinedID(agencyID, row.ServiceID),
			row.StopHeadsign.String,
			combinedTripID,
		)

		routeScheduleMap[combinedRouteID] = append(routeScheduleMap[combinedRouteID], stopTime)

		if row.TripHeadsign.Valid && row.TripHeadsign.String != "" {
			if routeHeadsignCounts[combinedRouteID] == nil {
				routeHeadsignCounts[combinedRouteID] = make(map[string]int)
			}
			routeHeadsignCounts[combinedRouteID][row.TripHeadsign.String]++
		}
	}

	tripIDs := make([]string, 0, len(tripIDsSet))
	for tripID := range tripIDsSet {
		tripIDs = append(tripIDs, tripID)
	}

	var trips []gtfsdb.Trip
	if len(tripIDs) > 0 {
		trips, err = api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDs)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	// Batch-fetch frequencies for all trips contributing to this schedule
	var freqsByTrip map[string][]gtfsdb.Frequency
	if len(tripIDs) > 0 {
		allFreqs, freqErr := api.GtfsManager.GetFrequenciesForTrips(ctx, tripIDs)
		if freqErr != nil {
			api.Logger.Warn("failed to batch fetch frequencies for schedule-for-stop", "error", freqErr)
		}
		if len(allFreqs) > 0 {
			freqsByTrip = gtfsInternal.GroupFrequenciesByTrip(allFreqs)
		}
	}
	if freqsByTrip == nil {
		freqsByTrip = make(map[string][]gtfsdb.Frequency)
	}

	// Track which trips belong to which routes (for frequency enrichment)
	tripRouteMap := make(map[string]string)
	tripServiceMap := make(map[string]string)
	for _, row := range scheduleRows {
		tripRouteMap[row.TripID] = row.RouteID
		tripServiceMap[row.TripID] = row.ServiceID
	}

	startOfDay := time.UnixMilli(date).In(loc)

	// For exact_times=1 trips, expand stop times at each headway interval
	for _, row := range scheduleRows {
		freqs, hasFreqs := freqsByTrip[row.TripID]
		if !hasFreqs {
			continue
		}
		for _, freq := range freqs {
			if freq.ExactTimes != 1 {
				continue
			}
			combinedRouteID := utils.FormCombinedID(agencyID, row.RouteID)
			combinedTripID := utils.FormCombinedID(agencyID, row.TripID)

			// Expand: create a virtual stop-time at each headway offset within the frequency window
			headwayNanos := freq.HeadwaySecs * int64(time.Second)
			if headwayNanos <= 0 {
				continue
			}
			for depBase := freq.StartTime; depBase < freq.EndTime; depBase += headwayNanos {
				shift := depBase - row.ArrivalTime
				if shift == 0 {
					continue // The original stop time already covers this
				}
				arrTimeMs := startOfDay.Add(time.Duration(row.ArrivalTime + shift)).UnixMilli()
				depTimeMs := startOfDay.Add(time.Duration(row.DepartureTime + shift)).UnixMilli()
				expandedST := models.NewScheduleStopTime(
					arrTimeMs, depTimeMs,
					utils.FormCombinedID(agencyID, row.ServiceID),
					row.StopHeadsign.String,
					combinedTripID,
				)
				routeScheduleMap[combinedRouteID] = append(routeScheduleMap[combinedRouteID], expandedST)
			}
		}
	}

	// Build ScheduleFrequency entries for each route from exact_times=0 trips
	routeScheduleFreqs := make(map[string][]models.ScheduleFrequency)
	for tripID, freqs := range freqsByTrip {
		routeID, ok := tripRouteMap[tripID]
		if !ok {
			continue
		}
		combinedRouteID := utils.FormCombinedID(agencyID, routeID)
		serviceID := tripServiceMap[tripID]
		for _, freq := range freqs {
			if freq.ExactTimes != 0 {
				continue
			}
			sf := models.ScheduleFrequency{
				ServiceDate: date,
				StartTime:   startOfDay.Add(time.Duration(freq.StartTime)).UnixMilli(),
				EndTime:     startOfDay.Add(time.Duration(freq.EndTime)).UnixMilli(),
				Headway:     int(freq.HeadwaySecs),
				ServiceID:   utils.FormCombinedID(agencyID, serviceID),
				TripID:      utils.FormCombinedID(agencyID, tripID),
			}
			routeScheduleFreqs[combinedRouteID] = append(routeScheduleFreqs[combinedRouteID], sf)
		}
	}

	// Build the route schedules
	var routeSchedules []models.StopRouteSchedule
	for routeID, stopTimes := range routeScheduleMap {
		// Select the most common headsign for this route
		tripHeadsign := ""
		maxCount := 0
		if headsigns, exists := routeHeadsignCounts[routeID]; exists {
			for headsign, count := range headsigns {
				if count > maxCount {
					maxCount = count
					tripHeadsign = headsign
				}
			}
		}

		directionSchedule := models.NewStopRouteDirectionSchedule(tripHeadsign, stopTimes, routeScheduleFreqs[routeID])
		routeSchedule := models.NewStopRouteSchedule(routeID, []models.StopRouteDirectionSchedule{directionSchedule})
		routeSchedules = append(routeSchedules, routeSchedule)
	}

	// Create the entry
	combinedStopID := utils.FormCombinedID(agencyID, stopID)
	entry := models.NewScheduleForStopEntry(combinedStopID, date, routeSchedules)

	// Convert reference maps to slices
	references := models.NewEmptyReferences()
	references.Agencies = utils.MapValues(agencyRefs)
	references.Routes = utils.MapValues(routeRefs)

	for _, trip := range trips {
		combinedTripID := utils.FormCombinedID(agencyID, trip.ID)
		tripRef := models.NewTripReference(
			combinedTripID,
			utils.FormCombinedID(agencyID, trip.RouteID),
			utils.FormCombinedID(agencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			trip.TripShortName.String,
			strconv.FormatInt(trip.DirectionID.Int64, 10),
			utils.FormCombinedID(agencyID, trip.BlockID.String),
			utils.FormCombinedID(agencyID, trip.ShapeID.String),
		)
		references.Trips = append(references.Trips, *tripRef)
	}

	routeIDsWithAgency := make([]string, 0, len(routeIDs))
	for _, ri := range routeIDs {
		routeIDsWithAgency = append(routeIDsWithAgency, utils.FormCombinedID(agencyID, ri))
	}

	stopRef := models.NewStop(
		utils.NullStringOrEmpty(stop.Code),
		utils.NullStringOrEmpty(stop.Direction),
		utils.FormCombinedID(agencyID, stop.ID),
		utils.NullStringOrEmpty(stop.Name),
		"",
		utils.MapWheelchairBoarding(utils.NullWheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		stop.Lat,
		stop.Lon,
		int(stop.LocationType.Int64),
		routeIDsWithAgency,
		routeIDsWithAgency,
	)

	references.Stops = append(references.Stops, stopRef)
	// Create and send response
	response := models.NewEntryResponse(entry, references, api.Clock)
	api.sendResponse(w, r, response)
}
