package restapi

import (
	"fmt"
	"github.com/jamespfennell/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"net/http"
	"time"
)

func (api *RestAPI) tripsForLocationHandler(w http.ResponseWriter, r *http.Request) {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseFloatParam(queryParams, "lat", nil)
	lon, _ := utils.ParseFloatParam(queryParams, "lon", fieldErrors)
	latSpan, _ := utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ := utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)
	//includeTrip := queryParams.Get("includeTrip") == "true"
	//includeSchedule := queryParams.Get("includeSchedule") == "true"

	currentAgency := api.GtfsManager.GetAgencies()[0]

	currentLocation, _ := time.LoadLocation(currentAgency.Timezone)
	timeParam := r.URL.Query().Get("time")

	formattedDate, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)

	ctx := r.Context()

	if ctx.Err() != nil {
		api.serverErrorResponse(w, r, ctx.Err())
		return
	}

	if !success {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)

	for _, serviceID := range serviceIDs {
		fmt.Println(serviceID)
	}

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Validate location parameters
	locationErrors := utils.ValidateLocationParams(lat, lon, 0, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return
	}

	stops := api.GtfsManager.GetStopsForLocation(ctx, lat, lon, -1, latSpan, lonSpan, "", 100, false)
	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.Id
	}
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesByStopIDs(ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	realTimeVehicles := api.GtfsManager.GetRealTimeVehicles()
	trips := make(map[string]bool)

	activeTrips := make(map[string]gtfs.Vehicle)

	for _, stopTime := range stopTimes {
		trips[stopTime.TripID] = true
	}

	for _, vehicle := range realTimeVehicles {
		if vehicle.Trip == nil {
			continue
		}
		if trips[vehicle.Trip.ID.ID] {
			activeTrips[vehicle.Trip.ID.ID] = vehicle
		}
	}

	var result []models.TripsForLocationListEntry

	// Calculate bounding box for the given lat/lon and span

	const epsilon = 1e-6
	var minLat, maxLat, minLon, maxLon float64

	minLat = lat - latSpan - epsilon
	maxLat = lat + latSpan + epsilon
	minLon = lon - lonSpan - epsilon
	maxLon = lon + lonSpan + epsilon
	for _, vehicle := range activeTrips {
		var vehiclePos = vehicle.Position

		if vehiclePos == nil {
			continue
		}
		if float64(*vehiclePos.Latitude) >= minLat && float64(*vehiclePos.Latitude) <= maxLat &&
			float64(*vehiclePos.Longitude) >= minLon && float64(*vehiclePos.Longitude) <= maxLon {
			entry := models.TripsForLocationListEntry{
				Frequency:    nil,
				Schedule:     nil,
				ServiceDate:  vehicle.Timestamp.Unix(),
				SituationIds: []string{},
				TripId:       utils.FormCombinedID(currentAgency.Name, vehicle.Trip.ID.ID),
			}
			result = append(result, entry)
		}
	}

	response := models.NewListResponseWithRange(result, models.NewEmptyReferences(), len(result) == 0)
	api.sendResponse(w, r, response)

}
