package restapi

import (
	"context"
	"net/http"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, err := api.parseAndValidateRequest(w, r)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, -1, latSpan, lonSpan, "", 100, false)
	stopIDs := extractStopIDs(stops)
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesByStopIDs(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	activeTrips := api.getActiveTrips(stopTimes, api.GtfsManager.GetRealTimeVehicles())
	bbox := boundingBox(lat, lon, latSpan, lonSpan)

	allRoutes, allTrips, err := api.getAllRoutesAndTrips(ctx, w, r)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	tripAgencyResolver := NewTripAgencyResolver(allRoutes, allTrips)

	result := api.buildTripsForLocationEntries(ctx, activeTrips, bbox, tripAgencyResolver, includeSchedule, currentLocation, todayMidnight, serviceDate, w, r)
	references := BuildTripReferences(api, w, r, ctx, includeTrip, allRoutes, allTrips, stops, result)
	response := models.NewListResponseWithRange(result, references, len(result) == 0)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) parseAndValidateRequest(w http.ResponseWriter, r *http.Request) (lat, lon, latSpan, lonSpan float64, includeTrip, includeSchedule bool, currentLocation *time.Location, todayMidnight time.Time, serviceDate time.Time, err error) {
	queryParams := r.URL.Query()
	lat, fieldErrors := utils.ParseFloatParam(queryParams, "lat", nil)
	lon, _ = utils.ParseFloatParam(queryParams, "lon", fieldErrors)
	latSpan, _ = utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ = utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)
	includeTrip = queryParams.Get("includeTrip") == "true"
	includeSchedule = queryParams.Get("includeSchedule") == "true"

	currentAgency := api.GtfsManager.GetAgencies()[0]
	currentLocation, _ = time.LoadLocation(currentAgency.Timezone)
	timeParam := queryParams.Get("time")
	currentTime := time.Now().In(currentLocation)
	todayMidnight = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentLocation)
	_, serviceDate, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)

	ctx := r.Context()
	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, ctx.Err()
	}
	if !success || len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, err
	}
	locationErrors := utils.ValidateLocationParams(lat, lon, 0, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return 0, 0, 0, 0, false, false, nil, time.Time{}, time.Time{}, err
	}
	return lat, lon, latSpan, lonSpan, includeTrip, includeSchedule, currentLocation, todayMidnight, serviceDate, nil
}

func extractStopIDs(stops []*gtfs.Stop) []string {
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.Id
	}
	return stopIDs
}

func (api *RestAPI) getActiveTrips(stopTimes []gtfsdb.StopTime, realTimeVehicles []gtfs.Vehicle) map[string]gtfs.Vehicle {
	trips := make(map[string]bool)
	for _, stopTime := range stopTimes {
		trips[stopTime.TripID] = true
	}
	activeTrips := make(map[string]gtfs.Vehicle)
	for _, vehicle := range realTimeVehicles {
		if vehicle.Trip != nil && trips[vehicle.Trip.ID.ID] {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}
	return activeTrips
}

type boundingBoxStruct struct{ minLat, maxLat, minLon, maxLon float64 }

func boundingBox(lat, lon, latSpan, lonSpan float64) boundingBoxStruct {
	const epsilon = 1e-6
	return boundingBoxStruct{
		minLat: lat - latSpan - epsilon,
		maxLat: lat + latSpan + epsilon,
		minLon: lon - lonSpan - epsilon,
		maxLon: lon + lonSpan + epsilon,
	}
}

func (api *RestAPI) getAllRoutesAndTrips(ctx context.Context, w http.ResponseWriter, r *http.Request) ([]gtfsdb.Route, []gtfsdb.Trip, error) {
	allRoutes, err := api.GtfsManager.GtfsDB.Queries.ListRoutes(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil, nil, err
	}
	allTrips, err := api.GtfsManager.GtfsDB.Queries.ListTrips(ctx)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil, nil, err
	}
	return allRoutes, allTrips, nil
}

func (api *RestAPI) buildTripsForLocationEntries(
	ctx context.Context,
	activeTrips map[string]gtfs.Vehicle,
	bbox boundingBoxStruct,
	tripAgencyResolver *TripAgencyResolver,
	includeSchedule bool,
	currentLocation *time.Location,
	todayMidnight time.Time,
	serviceDate time.Time,
	w http.ResponseWriter,
	r *http.Request,
) []models.TripsForLocationListEntry {
	var result []models.TripsForLocationListEntry
	for _, vehicle := range activeTrips {
		pos := vehicle.Position
		if pos == nil {
			continue
		}
		lat, lon := float64(*pos.Latitude), float64(*pos.Longitude)
		if lat < bbox.minLat || lat > bbox.maxLat || lon < bbox.minLon || lon > bbox.maxLon {
			continue
		}
		tripID := vehicle.Trip.ID.ID
		agencyID := tripAgencyResolver.GetAgencyNameByTripID(tripID)
		var schedule *models.TripsSchedule
		if includeSchedule {
			schedule = api.buildScheduleForTrip(ctx, tripID, agencyID, serviceDate, currentLocation, w, r)
			if schedule == nil {
				continue
			}
		}
		entry := models.TripsForLocationListEntry{
			Frequency:    nil,
			Schedule:     schedule,
			ServiceDate:  todayMidnight.UnixMilli(),
			SituationIds: api.GetSituationIDsForTrip(tripID),
			TripId:       utils.FormCombinedID(agencyID, tripID),
		}
		result = append(result, entry)
	}
	return result
}
