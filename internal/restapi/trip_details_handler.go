package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type TripDetailsParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
}

func (api *RestAPI) parseTripIdDetailsParams(r *http.Request) TripDetailsParams {
	params := TripDetailsParams{
		IncludeTrip:     true,
		IncludeSchedule: true,
		IncludeStatus:   true,
	}

	if serviceDateStr := r.URL.Query().Get("serviceDate"); serviceDateStr != "" {
		if serviceDateMs, err := strconv.ParseInt(serviceDateStr, 10, 64); err == nil {
			serviceDate := time.Unix(serviceDateMs/1000, 0)
			params.ServiceDate = &serviceDate
		}
	}

	if includeTripStr := r.URL.Query().Get("includeTrip"); includeTripStr != "" {
		params.IncludeTrip = includeTripStr == "true"
	}

	if includeScheduleStr := r.URL.Query().Get("includeSchedule"); includeScheduleStr != "" {
		params.IncludeSchedule = includeScheduleStr == "true"
	}

	if includeStatusStr := r.URL.Query().Get("includeStatus"); includeStatusStr != "" {
		params.IncludeStatus = includeStatusStr == "true"
	}

	if timeStr := r.URL.Query().Get("time"); timeStr != "" {
		if timeMs, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			timeParam := time.Unix(timeMs/1000, 0)
			params.Time = &timeParam
		}
	}

	return params
}

func (api *RestAPI) tripDetailsHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)
	agencyID, tripID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	ctx := r.Context()

	params := api.parseTripIdDetailsParams(r)

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	loc, _ := time.LoadLocation(agency.Timezone)

	var currentTime time.Time
	if params.Time != nil {
		currentTime = params.Time.In(loc)
	} else {
		currentTime = time.Now().In(loc)
	}

	var serviceDate time.Time
	if params.ServiceDate != nil {
		serviceDate = *params.ServiceDate
	} else {
		serviceDate = currentTime.Truncate(24 * time.Hour)
	}

	serviceDateMillis := serviceDate.Unix() * 1000

	var nextTripID, previousTripID string
	var schedule *models.Schedule
	var status *models.TripStatusForTripDetails

	if params.IncludeTrip || params.IncludeSchedule {
		nextTripID, previousTripID, _, err = api.GetNextAndPreviousTripIDs(ctx, &trip, tripID, agencyID, serviceDate)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	if params.IncludeStatus {
		status, _ = api.BuildTripStatus(ctx, agencyID, trip.ID, serviceDate, currentTime)
	}

	if params.IncludeSchedule {
		schedule, err = api.BuildTripSchedule(ctx, agencyID, tripID, nextTripID, previousTripID, loc)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	alerts := api.GtfsManager.GetAlertsForTrip(tripID)
	situationIDs := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		if alert.ID != "" {
			situationIDs = append(situationIDs, alert.ID)
		}
	}
	tripDetails := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, trip.ID),
		ServiceDate:  serviceDateMillis,
		Schedule:     schedule,
		Frequency:    nil,
		SituationIDs: api.GetSituationIDsForTrip(tripID),
	}

	if status != nil {
		tripDetails.Status = status
	}

	references := models.NewEmptyReferences()

	if params.IncludeTrip {
		tripsToInclude := []string{utils.FormCombinedID(agencyID, trip.ID)}

		if nextTripID != "" {
			tripsToInclude = append(tripsToInclude, nextTripID)
		}
		if previousTripID != "" {
			tripsToInclude = append(tripsToInclude, previousTripID)
		}

		referencedTrips, err := api.buildReferencedTrips(ctx, agencyID, tripsToInclude, trip)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		referencedTripsIface := make([]interface{}, len(referencedTrips))
		for i, t := range referencedTrips {
			referencedTripsIface[i] = t
		}
		references.Trips = referencedTripsIface
	}

	routeModel := models.NewRoute(
		utils.FormCombinedID(agencyID, route.ID),
		agencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String,
		route.ShortName.String,
	)
	references.Routes = append(references.Routes, routeModel)

	agencyModel := models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",
		false,
	)
	references.Agencies = append(references.Agencies, agencyModel)

	if params.IncludeSchedule && schedule != nil {
		stops, err := api.buildStopReferences(ctx, agencyID, schedule.StopTimes)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Stops = stops
	}

	response := models.NewEntryResponse(tripDetails, references)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) buildReferencedTrips(ctx context.Context, agencyID string, tripsToInclude []string, mainTrip gtfsdb.Trip) ([]*models.Trip, error) {
	referencedTrips := []*models.Trip{}

	for _, tripID := range tripsToInclude {
		_, refTripID, err := utils.ExtractAgencyIDAndCodeID(tripID)
		if err != nil {
			continue
		}

		if refTripID == mainTrip.ID && len(referencedTrips) > 0 {
			continue
		}

		refTrip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, refTripID)
		if err != nil {
			continue
		}

		refRoute, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, refTrip.RouteID)
		if err != nil {
			continue
		}

		var blockID string
		if refTrip.BlockID.Valid && refTrip.BlockID.String != "" {
			blockID = utils.FormCombinedID(agencyID, refTrip.BlockID.String)
		}

		refTripModel := &models.Trip{
			ID:             tripID,
			RouteID:        utils.FormCombinedID(agencyID, refTrip.RouteID),
			ServiceID:      utils.FormCombinedID(agencyID, refTrip.ServiceID),
			ShapeID:        utils.FormCombinedID(agencyID, refTrip.ShapeID.String),
			TripHeadsign:   refTrip.TripHeadsign.String,
			TripShortName:  refTrip.TripShortName.String,
			DirectionID:    refTrip.DirectionID.Int64,
			BlockID:        blockID,
			RouteShortName: refRoute.ShortName.String,
			TimeZone:       "",
			PeakOffPeak:    0,
		}

		referencedTrips = append(referencedTrips, refTripModel)
	}

	return referencedTrips, nil
}

func (api *RestAPI) buildStopReferences(ctx context.Context, agencyID string, stopTimes []models.StopTime) ([]models.Stop, error) {
	stopIDSet := make(map[string]bool)
	originalStopIDs := make([]string, 0, len(stopTimes))

	for _, st := range stopTimes {
		_, originalStopID, err := utils.ExtractAgencyIDAndCodeID(st.StopID)
		if err != nil {
			continue
		}

		if !stopIDSet[originalStopID] {
			stopIDSet[originalStopID] = true
			originalStopIDs = append(originalStopIDs, originalStopID)
		}
	}

	if len(originalStopIDs) == 0 {
		return []models.Stop{}, nil
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, originalStopIDs)
	if err != nil {
		return nil, err
	}

	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stops {
		stopMap[stop.ID] = stop
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, originalStopIDs)
	if err != nil {
		return nil, err
	}

	routesByStop := make(map[string][]gtfsdb.Route)
	for _, routeRow := range allRoutes {
		route := gtfsdb.Route{
			ID:        routeRow.ID,
			AgencyID:  routeRow.AgencyID,
			ShortName: routeRow.ShortName,
			LongName:  routeRow.LongName,
			Desc:      routeRow.Desc,
			Type:      routeRow.Type,
			Url:       routeRow.Url,
			Color:     routeRow.Color,
			TextColor: routeRow.TextColor,
		}
		routesByStop[routeRow.StopID] = append(routesByStop[routeRow.StopID], route)
	}

	modelStops := make([]models.Stop, 0, len(stopTimes))
	processedStops := make(map[string]bool)

	for _, st := range stopTimes {
		_, originalStopID, err := utils.ExtractAgencyIDAndCodeID(st.StopID)
		if err != nil {
			continue
		}

		if processedStops[originalStopID] {
			continue
		}
		processedStops[originalStopID] = true

		stop, exists := stopMap[originalStopID]
		if !exists {
			continue
		}

		routesForStop := routesByStop[originalStopID]
		combinedRouteIDs := make([]string, len(routesForStop))
		for i, rt := range routesForStop {
			combinedRouteIDs[i] = utils.FormCombinedID(agencyID, rt.ID)
		}

		stopModel := models.Stop{
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Name:               stop.Name.String,
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			Code:               stop.Code.String,
			Direction:          "NE", // TODO
			LocationType:       int(stop.LocationType.Int64),
			WheelchairBoarding: "UNKNOWN",
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		modelStops = append(modelStops, stopModel)
	}

	return modelStops, nil
}
