package restapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type TripForVehicleParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
}

func (api *RestAPI) parseTripForVehicleParams(r *http.Request) TripForVehicleParams {
	params := TripForVehicleParams{
		IncludeTrip:     false,
		IncludeSchedule: false,
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

func (api *RestAPI) tripForVehicleHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)
	agencyID, vehicleID, err := utils.ExtractAgencyIDAndCodeID(queryParamID)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	vehicle, err := api.GtfsManager.GetVehicleByID(vehicleID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}
	if vehicle == nil || vehicle.Trip == nil {
		api.sendNotFound(w, r)
		return
	}

	ctx := r.Context()
	params := api.parseTripForVehicleParams(r)

	tripID := vehicle.Trip.ID.ID

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
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

	serviceDate, serviceDateMillis := utils.ServiceDateMillis(params.ServiceDate, currentTime)

	var status *models.TripStatusForTripDetails
	if params.IncludeStatus {
		status, _ = api.BuildTripStatus(ctx, agencyID, tripID, serviceDate, currentTime)
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		fmt.Println("GetTrip error:", err)
		return
	}

	var schedule *models.Schedule
	if params.IncludeSchedule {
		schedule, _ = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, time.Local)
	}

	situationIDs := []string{}

	if status != nil {
		alerts := api.GtfsManager.GetAlertsForTrip(vehicle.Trip.ID.ID)
		for _, alert := range alerts {
			if alert.ID != "" {
				situationIDs = append(situationIDs, alert.ID)
			}
		}
	}

	entry := &models.TripDetails{
		TripID:       tripID,
		ServiceDate:  serviceDateMillis,
		Frequency:    nil,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: situationIDs,
	}

	// Build references

	references := models.NewEmptyReferences()

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

	stopIDs := []string{}

	if status != nil {
		if status.ClosestStop != "" {
			_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(status.ClosestStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, closestStopID)
		}
		if status.NextStop != "" {
			_, nextStopID, err := utils.ExtractAgencyIDAndCodeID(status.NextStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, nextStopID)
		}
	}
	stops, uniqueRouteMap, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references.Stops = stops

	fmt.Println("RefStops:", stops)
	fmt.Println("RefRoutes:", uniqueRouteMap)

	for _, route := range uniqueRouteMap {
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
	}

	references.Agencies = append(references.Agencies, agencyModel)

	tripRef := models.NewTripReference(
		utils.FormCombinedID(agencyID, trip.ID),
		utils.FormCombinedID(agencyID, trip.RouteID),
		utils.FormCombinedID(agencyID, trip.ServiceID),
		trip.TripHeadsign.String,
		trip.TripShortName.String,
		trip.DirectionID.Int64,
		utils.FormCombinedID(agencyID, trip.BlockID.String),
		utils.FormCombinedID(agencyID, trip.ShapeID.String),
	)
	references.Trips = append(references.Trips, tripRef)
	response := models.NewEntryResponse(entry, references)
	api.sendResponse(w, r, response)

}

// BuildStopReferencesAndRouteIDsForStops builds stop references and collects unique routes for the given stop IDs.
func BuildStopReferencesAndRouteIDsForStops(api *RestAPI, ctx context.Context, agencyID string, stopIDs []string) ([]models.Stop, map[string]gtfsdb.GetRoutesForStopsRow, error) {
	if len(stopIDs) == 0 {
		return []models.Stop{}, map[string]gtfsdb.GetRoutesForStopsRow{}, nil
	}

	stopIDSet := make(map[string]struct{})
	uniqueStopIDs := make([]string, 0, len(stopIDs))
	for _, id := range stopIDs {
		if _, exists := stopIDSet[id]; !exists {
			stopIDSet[id] = struct{}{}
			uniqueStopIDs = append(uniqueStopIDs, id)
		}
	}

	stopsDB, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}
	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stopsDB {
		stopMap[stop.ID] = stop
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}

	routesByStop := make(map[string][]gtfsdb.Route)
	uniqueRouteMap := make(map[string]gtfsdb.GetRoutesForStopsRow)
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
		combinedID := utils.FormCombinedID(agencyID, routeRow.ID)
		uniqueRouteMap[combinedID] = routeRow
	}

	modelStops := make([]models.Stop, 0, len(uniqueStopIDs))
	for _, stopID := range uniqueStopIDs {
		stop, exists := stopMap[stopID]
		if !exists {
			continue
		}
		routesForStop := routesByStop[stopID]
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
			Direction:          api.calculateStopDirection(ctx, stop.ID),
			LocationType:       int(stop.LocationType.Int64),
			WheelchairBoarding: models.UnknownValue,
			RouteIDs:           combinedRouteIDs,
			StaticRouteIDs:     combinedRouteIDs,
		}
		modelStops = append(modelStops, stopModel)
	}

	return modelStops, uniqueRouteMap, nil
}
