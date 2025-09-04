package restapi

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"net/http"
	"time"
)

func (api *RestAPI) tripsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	agencyID, routeID, _ := utils.ExtractAgencyIDAndCodeID(utils.ExtractIDFromParams(r))

	if routeID == "" || agencyID == "" {
		http.Error(w, "null", http.StatusBadRequest)
		return
	}

	includeSchedule := r.URL.Query().Get("includeSchedule") != "false"
	includeStatus := r.URL.Query().Get("includeStatus") != "false"

	currentAgency := api.GtfsManager.FindAgency(agencyID)
	if currentAgency == nil {
		http.Error(w, "null", http.StatusNotFound)
		return
	}

	currentLocation, err := time.LoadLocation(currentAgency.Timezone)
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
	fmt.Println(formattedDate)
	fmt.Println(serviceIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	routeTrips, err := api.GtfsManager.GtfsDB.Queries.GetAllTripsForRoute(ctx, routeID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	blockIDs := make(map[string]struct{})
	for _, trip := range routeTrips {
		if trip.BlockID.String != "" {
			blockIDs[trip.BlockID.String] = struct{}{}
		}
	}

	var allRelatedTrips []gtfsdb.GetTripsByBlockIDRow
	for blockID := range blockIDs {
		relatedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, sql.NullString{String: blockID, Valid: true})
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		for _, trip := range relatedTrips {
			if trip.RouteID != routeID {
				allRelatedTrips = append(allRelatedTrips, trip)
			}
		}
	}
	activeTrips := make(map[string]gtfs.Vehicle)
	realTimeVehicles := api.GtfsManager.GetRealTimeVehicles()
	realTimeTrips := api.GtfsManager.GetRealTimeTrips()

	for _, trip := range realTimeTrips {
		fmt.Println("Trip id: " + trip.ID.ID)
	}
	for _, vehicle := range realTimeVehicles {
		if vehicle.Position == nil || vehicle.Trip == nil {
			continue
		}

		isOnRequestedRoute := vehicle.Trip.ID.RouteID == routeID
		isLinkedTrip := false
		for _, trip := range allRelatedTrips {
			if trip.ID == vehicle.Trip.ID.ID {
				isLinkedTrip = true
				break
			}
		}

		if isOnRequestedRoute || isLinkedTrip {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}

	allRoutes, allTrips, err := api.getAllRoutesAndTrips(ctx, w, r)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	todayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	tripAgencyResolver := NewTripAgencyResolver(allRoutes, allTrips)
	result := api.buildTripsForRouteEntries(ctx, activeTrips, tripAgencyResolver, includeSchedule, includeStatus, currentLocation, currentTime, todayMidnight, w, r)

	references := BuildTripReferences(api, w, r, ctx, includeSchedule, allRoutes, allTrips, nil, result)
	response := models.NewListResponseWithRange(result, references, len(result) == 0)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) buildTripsForRouteEntries(
	ctx context.Context,
	activeTrips map[string]gtfs.Vehicle,
	tripAgencyResolver *TripAgencyResolver,
	includeSchedule bool,
	includeStatus bool,
	currentLocation *time.Location,
	currentTime time.Time,
	todayMidnight time.Time,
	w http.ResponseWriter,
	r *http.Request,
) []models.TripsForRouteListEntry {
	var result []models.TripsForRouteListEntry
	for _, vehicle := range activeTrips {
		pos := vehicle.Position
		if pos == nil {
			continue
		}

		tripID := vehicle.Trip.ID.ID
		agencyID := tripAgencyResolver.GetAgencyNameByTripID(tripID)
		var schedule *models.TripsSchedule
		var status *models.TripStatusForTripDetails
		if includeSchedule {
			schedule = api.buildScheduleForTrip(ctx, tripID, agencyID, currentTime, currentLocation, w, r)
			if schedule == nil {
				continue
			}
		}

		if includeStatus {
			status, _ = api.BuildTripStatus(ctx, agencyID, tripID, currentTime, currentTime)

			if status == nil {
				continue
			}
		}
		entry := models.TripsForRouteListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			Status:       status,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}
	return result
}
