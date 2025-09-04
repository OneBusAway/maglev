package restapi

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// buildScheduleForTrip assembles schedule details for a given trip.
func (api *RestAPI) buildScheduleForTrip(
	ctx context.Context,
	tripID, agencyID string, serviceDate time.Time,
	currentLocation *time.Location,
	w http.ResponseWriter,
	r *http.Request,
) *models.TripsSchedule {
	shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	var shapePoints []gtfs.ShapePoint
	if len(shapeRows) > 1 {
		shapePoints = make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{Latitude: sp.Lat, Longitude: sp.Lon}
		}
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil
	}

	nextTripID, previousTripID, stopTimes, err := api.GetNextAndPreviousTripIDs(ctx, &trip, agencyID, serviceDate)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return nil
	}

	stopTimesList := buildStopTimesList(api, ctx, stopTimes, shapePoints, agencyID)
	return &models.TripsSchedule{
		Frequency:      nil,
		NextTripId:     nextTripID,
		PreviousTripId: previousTripID,
		StopTimes:      stopTimesList,
		TimeZone:       currentLocation.String(),
	}
}

// buildStopTimesList converts DB stop times to API model, estimating distance along trip.
func buildStopTimesList(api *RestAPI, ctx context.Context, stopTimes []gtfsdb.StopTime, shapePoints []gtfs.ShapePoint, agencyID string) []models.StopTime {
	// Precompute cumulative distances along the shape
	cumDist := make([]float64, len(shapePoints))
	for i := 1; i < len(shapePoints); i++ {
		cumDist[i] = cumDist[i-1] + utils.Haversine(
			shapePoints[i-1].Latitude, shapePoints[i-1].Longitude,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
		)
	}
	stopTimesList := make([]models.StopTime, 0, len(stopTimes))
	for _, stopTime := range stopTimes {
		stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopTime.StopID)
		if err != nil {
			continue
		}
		stopLat, stopLon := stop.Lat, stop.Lon
		minIdx, minDist := 0, math.MaxFloat64
		for i, sp := range shapePoints {
			d := utils.Haversine(stopLat, stopLon, sp.Latitude, sp.Longitude)
			if d < minDist {
				minDist = d
				minIdx = i
			}
		}
		distanceAlongTheTrip := cumDist[minIdx]
		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         int(stopTime.ArrivalTime),
			DepartureTime:       int(stopTime.DepartureTime),
			StopHeadsign:        stopTime.StopHeadsign.String,
			DistanceAlongTrip:   distanceAlongTheTrip,
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
}

// BuildTripReferences builds reference data for trips; accepts any list entry type that exposes GetTripId.
func BuildTripReferences[T interface{ GetTripId() string }](api *RestAPI, w http.ResponseWriter, r *http.Request, ctx context.Context, includeTrip bool, allRoutes []gtfsdb.Route, allTrips []gtfsdb.Trip, stops []*gtfs.Stop, trips []T) models.ReferencesModel {
	// Collect present trip IDs
	presentTrips := make(map[string]models.Trip, len(trips))
	presentRoutes := make(map[string]models.Route)
	for _, trip := range trips {
		_, tripID, _ := utils.ExtractAgencyIDAndCodeID(trip.GetTripId())
		presentTrips[tripID] = models.Trip{}
	}
	stopList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		if stop.Latitude == nil || stop.Longitude == nil {
			continue
		}
		routeIds, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStop(ctx, stop.Id)
		if err != nil {
			continue
		}

		routeIdsString := make([]string, len(routeIds))
		for i, id := range routeIds {
			presentRoutes[id.(string)] = models.Route{}
			routeIdsString[i] = id.(string)
		}

		stopList = append(stopList, models.Stop{
			Code:               stop.Code,
			Direction:          "NA", // TODO add direction
			ID:                 stop.Id,
			Lat:                *stop.Latitude,
			Lon:                *stop.Longitude,
			LocationType:       0,
			Name:               stop.Name,
			Parent:             "",
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(stop.WheelchairBoarding),
		})
	}

	// Collect present routes and fill presentTrips with details
	for _, trip := range allTrips {
		if _, exists := presentTrips[trip.ID]; exists {
			presentTrips[trip.ID] = models.Trip{
				ID:            trip.ID,
				RouteID:       trip.RouteID,
				ServiceID:     trip.ServiceID,
				TripHeadsign:  trip.TripHeadsign.String,
				TripShortName: trip.TripShortName.String,
				DirectionID:   trip.DirectionID.Int64,
				BlockID:       trip.BlockID.String,
				ShapeID:       trip.ShapeID.String,
				PeakOffPeak:   0,
				TimeZone:      "",
			}
			presentRoutes[trip.RouteID] = models.Route{}
		}
	}

	// Collect agencies for present routes
	presentAgencies := make(map[string]models.AgencyReference)
	for _, route := range allRoutes {
		if _, exists := presentRoutes[route.ID]; exists {
			presentRoutes[route.ID] = models.NewRoute(
				utils.FormCombinedID(route.AgencyID, route.ID),
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String,
				route.ShortName.String,
			)
			currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return models.ReferencesModel{}
			}
			presentAgencies[currentAgency.ID] = models.NewAgencyReference(
				currentAgency.ID,
				currentAgency.Name,
				currentAgency.Url,
				currentAgency.Timezone,
				currentAgency.Lang.String,
				currentAgency.Phone.String,
				currentAgency.Email.String,
				currentAgency.FareUrl.String,
				"",
				false,
			)
		}
	}

	// Optionally include trip details
	tripsRefList := make([]interface{}, 0, len(presentTrips))
	if includeTrip {
		for _, trip := range presentTrips {
			tripDetails, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, trip.ID)
			if err == nil {
				var currentAgency = presentRoutes[tripDetails.RouteID].AgencyID
				tripsRefList = append(tripsRefList, models.Trip{
					ID:            utils.FormCombinedID(currentAgency, trip.ID),
					RouteID:       utils.FormCombinedID(currentAgency, tripDetails.RouteID),
					ServiceID:     utils.FormCombinedID(currentAgency, trip.ServiceID),
					TripHeadsign:  tripDetails.TripHeadsign.String,
					TripShortName: tripDetails.TripShortName.String,
					DirectionID:   tripDetails.DirectionID.Int64,
					BlockID:       tripDetails.BlockID.String,
					ShapeID:       utils.FormCombinedID(currentAgency, tripDetails.ShapeID.String),
					PeakOffPeak:   0,
					TimeZone:      "",
				})
			}
		}
	}

	// Convert presentRoutes and presentTrips maps to slices
	routes := make([]interface{}, 0, len(presentRoutes))
	for _, route := range presentRoutes {
		routes = append(routes, route)
	}

	agencyList := make([]models.AgencyReference, 0, len(presentAgencies))
	for _, agency := range presentAgencies {
		agencyList = append(agencyList, agency)
	}

	return models.ReferencesModel{
		Agencies:   agencyList,
		Routes:     routes,
		Situations: []interface{}{},
		StopTimes:  []interface{}{},
		Stops:      stopList,
		Trips:      tripsRefList,
	}
}
