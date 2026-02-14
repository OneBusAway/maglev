package restapi

import (
	"context"
	"math"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) BuildTripStatus(
	ctx context.Context,
	agencyID, tripID string,
	serviceDate time.Time,
	currentTime time.Time,

) (*models.TripStatusForTripDetails, error) {
	status := &models.TripStatusForTripDetails{
		ActiveTripID:      utils.FormCombinedID(agencyID, tripID),
		ServiceDate:       serviceDate.Unix() * 1000,
		SituationIDs:      api.GetSituationIDsForTrip(tripID),
		OccupancyCapacity: -1,
		OccupancyCount:    -1,
	}

	vehicle := api.GtfsManager.GetVehicleForTrip(tripID)
	if vehicle != nil {
		if vehicle.ID != nil {
			status.VehicleID = vehicle.ID.ID
		}
		if vehicle.OccupancyStatus != nil {
			status.OccupancyStatus = vehicle.OccupancyStatus.String()
		}
		if vehicle.OccupancyPercentage != nil {
			status.OccupancyCapacity = int(*vehicle.OccupancyPercentage)
		}
		api.BuildVehicleStatus(ctx, vehicle, tripID, agencyID, status)
	}

	if vehicle != nil && vehicle.ID != nil {
		staleDetector := NewStaleDetector()
		if !staleDetector.Check(vehicle, currentTime) {
			vehiclePos, err := api.realtimeService.GetVehiclePosition(ctx, vehicle.ID.ID)
			if err == nil {
				status.ScheduleDeviation = vehiclePos.ScheduleDeviation
				status.DistanceAlongTrip = vehiclePos.Distance
				status.Predicted = vehiclePos.IsPredicted

				if vehiclePos.ActiveTripID != "" {
					status.ActiveTripID = utils.FormCombinedID(agencyID, vehiclePos.ActiveTripID)
				}

				if vehiclePos.CurrentStopID != "" {
					status.ClosestStop = utils.FormCombinedID(agencyID, vehiclePos.CurrentStopID)
				}

				if vehiclePos.NextStopID != "" {
					status.NextStop = utils.FormCombinedID(agencyID, vehiclePos.NextStopID)
				}

				status.ClosestStopTimeOffset = vehiclePos.CurrentStopTimeOffset
				status.NextStopTimeOffset = vehiclePos.NextStopTimeOffset
			}
		} else {
			status.Predicted = false
		}
	}

	if status.ScheduleDeviation == 0 || status.ClosestStop == "" {
		deviation := api.calculateScheduleDeviationFromTripUpdates(tripID)
		if deviation != 0 {
			status.ScheduleDeviation = deviation
			activeTripID, closestStopID, nextStopID, distance, err := api.calculateBlockLevelPosition(
				ctx, tripID, vehicle, currentTime, deviation,
			)
			if err == nil {
				if activeTripID != "" {
					status.ActiveTripID = utils.FormCombinedID(agencyID, activeTripID)
				}
				if closestStopID != "" {
					status.ClosestStop = utils.FormCombinedID(agencyID, closestStopID)
				}
				if nextStopID != "" {
					status.NextStop = utils.FormCombinedID(agencyID, nextStopID)
				}
				if distance > 0 {
					status.DistanceAlongTrip = distance
				}
				status.Predicted = true
			}
		}
	}

	blockTripSequence := api.setBlockTripSequence(ctx, tripID, serviceDate, status)
	if blockTripSequence > 0 {
		status.BlockTripSequence = blockTripSequence
	}

	if status.ClosestStop == "" || status.NextStop == "" {
		api.fillStopsFromSchedule(ctx, status, tripID, currentTime, agencyID)
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, status.ActiveTripID)

	shapeRows, shapeErr := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if shapeErr == nil && len(shapeRows) > 1 {
		shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{
				Latitude:  sp.Lat,
				Longitude: sp.Lon,
			}
		}
		cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
		status.TotalDistanceAlongTrip = cumulativeDistances[len(cumulativeDistances)-1]

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			actualDistance := api.getVehicleDistanceAlongShapeContextual(ctx, tripID, vehicle)

			if err == nil {
				effectiveDistance := api.calculateEffectiveDistanceAlongTrip(
					actualDistance,
					status.ScheduleDeviation,
					currentTime,
					stopTimes,
					cumulativeDistances,
				)
				status.DistanceAlongTrip = effectiveDistance
			} else {
				status.DistanceAlongTrip = actualDistance
			}
		}
	}

	if err == nil {
		stopTimesPtrs := make([]*gtfsdb.StopTime, len(stopTimes))
		for i := range stopTimes {
			stopTimesPtrs[i] = &stopTimes[i]
		}

		if shapeErr != nil {
			shapeRows = []gtfsdb.Shape{}
		}

		shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{
				Latitude:  sp.Lat,
				Longitude: sp.Lon,
			}
		}

		if status.ClosestStop == "" || status.NextStop == "" {
			var closestStopID, nextStopID string
			var closestOffset, nextOffset int

			if vehicle != nil && vehicle.Position != nil {
				if vehicle.StopID != nil && *vehicle.StopID != "" {
					closestStopID = *vehicle.StopID
					closestOffset = api.calculateOffsetForStop(closestStopID, stopTimesPtrs, currentTime, status.ScheduleDeviation)

					nextStopID, nextOffset = api.findNextStopAfter(closestStopID, stopTimesPtrs, currentTime, status.ScheduleDeviation)
				} else if vehicle.CurrentStopSequence != nil {
					closestStopID, closestOffset = api.findClosestStopBySequence(
						stopTimesPtrs, *vehicle.CurrentStopSequence, currentTime, status.ScheduleDeviation, vehicle,
					)
					nextStopID, nextOffset = api.findNextStopBySequence(
						ctx, stopTimesPtrs, *vehicle.CurrentStopSequence, currentTime, status.ScheduleDeviation, vehicle, tripID, serviceDate,
					)
				} else {
					closestStopID, closestOffset, nextStopID, nextOffset = api.findStopsByScheduleDeviation(
						stopTimesPtrs, currentTime, status.ScheduleDeviation,
					)
				}
			} else {
				stopDelays := api.getStopDelaysFromTripUpdates(tripID)
				currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())
				closestStopID, closestOffset = findClosestStopByTimeWithDelays(currentTimeSeconds, stopTimesPtrs, stopDelays)
				nextStopID, nextOffset = findNextStopByTimeWithDelays(currentTimeSeconds, stopTimesPtrs, stopDelays)
			}

			if closestStopID != "" {
				status.ClosestStop = utils.FormCombinedID(agencyID, closestStopID)
				status.ClosestStopTimeOffset = closestOffset
			}
			if nextStopID != "" {
				status.NextStop = utils.FormCombinedID(agencyID, nextStopID)
				status.NextStopTimeOffset = nextOffset
			}
		}
	}

	return status, nil
}

func (api *RestAPI) BuildTripSchedule(ctx context.Context, agencyID string, serviceDate time.Time, trip *gtfsdb.Trip, loc *time.Location) (*models.Schedule, error) {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	if err != nil {
		return nil, err
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, trip.ID)
	var shapePoints []gtfs.ShapePoint
	if err == nil && len(shapeRows) > 0 {
		shapePoints = make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{
				Latitude:  sp.Lat,
				Longitude: sp.Lon,
			}
		}
	}

	var nextTripID, previousTripID string
	nextTripID, previousTripID, _, err = api.GetNextAndPreviousTripIDs(ctx, trip, agencyID, serviceDate)
	if err != nil {
		return nil, err
	}

	cumulativeDistances := preCalculateCumulativeDistances(shapePoints)

	// Batch-fetch all stop coordinates at once
	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Create a map for quick stop coordinate lookup
	stopCoords := make(map[string]struct{ lat, lon float64 })
	for _, stop := range stops {
		stopCoords[stop.ID] = struct{ lat, lon float64 }{lat: stop.Lat, lon: stop.Lon}
	}

	stopTimesVals := make([]models.StopTime, len(stopTimes))
	for i, st := range stopTimes {
		var distanceAlongTrip float64
		if coords, exists := stopCoords[st.StopID]; exists && len(shapePoints) > 0 {
			distanceAlongTrip = api.calculatePreciseDistanceAlongTripWithCoords(
				coords.lat, coords.lon, shapePoints, cumulativeDistances,
			)
		}

		stopTimesVals[i] = models.StopTime{
			ArrivalTime:         int(st.ArrivalTime / 1e9),
			DepartureTime:       int(st.DepartureTime / 1e9),
			StopID:              utils.FormCombinedID(agencyID, st.StopID),
			StopHeadsign:        st.StopHeadsign.String,
			DistanceAlongTrip:   distanceAlongTrip,
			HistoricalOccupancy: "",
		}
	}

	return &models.Schedule{
		StopTimes:      stopTimesVals,
		TimeZone:       loc.String(),
		Frequency:      0,
		NextTripID:     nextTripID,
		PreviousTripID: previousTripID,
	}, nil
}

func (api *RestAPI) GetNextAndPreviousTripIDs(ctx context.Context, trip *gtfsdb.Trip, agencyID string, serviceDate time.Time) (nextTripID string, previousTripID string, stopTimes []gtfsdb.StopTime, err error) {
	if !trip.BlockID.Valid {
		return "", "", nil, nil
	}

	orderedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		return "", "", nil, err
	}
	if len(orderedTrips) == 0 {
		return "", "", nil, nil
	}

	currentIndex := -1
	for i, t := range orderedTrips {
		if t.ID == trip.ID {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return "", "", nil, nil
	}

	if currentIndex > 0 {
		previousTripID = utils.FormCombinedID(agencyID, orderedTrips[currentIndex-1].ID)
	}

	if currentIndex < len(orderedTrips)-1 {
		nextTripID = utils.FormCombinedID(agencyID, orderedTrips[currentIndex+1].ID)
	}

	stopTimes, err = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	if err != nil {
		return nextTripID, previousTripID, nil, err
	}

	return nextTripID, previousTripID, stopTimes, nil
}

func findClosestStopByTimeWithDelays(currentTimeSeconds int64, stopTimes []*gtfsdb.StopTime, stopDelays map[string]StopDelayInfo) (stopID string, offset int) {
	var minTimeDiff int64 = math.MaxInt64
	var closestStopTimeSeconds int64

	for _, st := range stopTimes {
		var stopTimeSeconds int64
		if st.DepartureTime > 0 {
			stopTimeSeconds = st.DepartureTime / 1e9
		} else if st.ArrivalTime > 0 {
			stopTimeSeconds = st.ArrivalTime / 1e9
		} else {
			continue
		}

		if stopDelays != nil {
			if delayInfo, exists := stopDelays[st.StopID]; exists {
				if st.DepartureTime > 0 && delayInfo.DepartureDelay != 0 {
					stopTimeSeconds += delayInfo.DepartureDelay
				} else if delayInfo.ArrivalDelay != 0 {
					stopTimeSeconds += delayInfo.ArrivalDelay
				}
			}
		}

		timeDiff := int64(math.Abs(float64(currentTimeSeconds - stopTimeSeconds)))
		if timeDiff < minTimeDiff {
			minTimeDiff = timeDiff
			stopID = st.StopID
			closestStopTimeSeconds = stopTimeSeconds
		}
	}

	if stopID != "" {
		offset = int(closestStopTimeSeconds - currentTimeSeconds)
	}

	return
}

func findNextStopByTimeWithDelays(currentTimeSeconds int64, stopTimes []*gtfsdb.StopTime, stopDelays map[string]StopDelayInfo) (stopID string, offset int) {
	var minTimeDiff int64 = math.MaxInt64
	var nextStopTimeSeconds int64

	for _, st := range stopTimes {
		var stopTimeSeconds int64
		if st.DepartureTime > 0 {
			stopTimeSeconds = st.DepartureTime / 1e9
		} else if st.ArrivalTime > 0 {
			stopTimeSeconds = st.ArrivalTime / 1e9
		} else {
			continue
		}

		if stopDelays != nil {
			if delayInfo, exists := stopDelays[st.StopID]; exists {
				if st.DepartureTime > 0 && delayInfo.DepartureDelay != 0 {
					stopTimeSeconds += delayInfo.DepartureDelay
				} else if delayInfo.ArrivalDelay != 0 {
					stopTimeSeconds += delayInfo.ArrivalDelay
				}
			}
		}

		if stopTimeSeconds > currentTimeSeconds {
			timeDiff := stopTimeSeconds - currentTimeSeconds
			if timeDiff < minTimeDiff {
				minTimeDiff = timeDiff
				stopID = st.StopID
				nextStopTimeSeconds = stopTimeSeconds
			}
		}
	}

	if stopID != "" {
		offset = int(nextStopTimeSeconds - currentTimeSeconds)
	}

	return
}

func getDistanceAlongShape(lat, lon float64, shape []gtfs.ShapePoint) float64 {
	if len(shape) == 0 {
		return 0
	}
	var total float64
	var closestIndex int
	var minDist = math.MaxFloat64

	for i := range shape {
		dist := utils.Distance(lat, lon, shape[i].Latitude, shape[i].Longitude)
		if dist < minDist {
			minDist = dist
			closestIndex = i
		}
	}

	for i := 1; i <= closestIndex; i++ {
		total += utils.Distance(shape[i-1].Latitude, shape[i-1].Longitude, shape[i].Latitude, shape[i].Longitude)
	}

	return total
}

func getDistanceAlongShapeInRange(lat, lon float64, shape []gtfs.ShapePoint, minDistTraveled, maxDistTraveled float64) float64 {
	if len(shape) == 0 {
		return 0
	}

	cumulativeDistances := preCalculateCumulativeDistances(shape)
	var bestDist float64
	var minPointDist = math.MaxFloat64

	// If maxDistTraveled is 0, it might mean it wasn't provided or it's the start.
	// We should probably use the whole shape if the range is invalid.
	useRange := maxDistTraveled > minDistTraveled

	for i := 0; i < len(shape); i++ {
		if useRange && (cumulativeDistances[i] < minDistTraveled-10 || cumulativeDistances[i] > maxDistTraveled+10) {
			continue
		}

		dist := utils.Distance(lat, lon, shape[i].Latitude, shape[i].Longitude)
		if dist < minPointDist {
			minPointDist = dist
			bestDist = cumulativeDistances[i]
		}
	}

	return bestDist
}

func (api *RestAPI) setBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time, status *models.TripStatusForTripDetails) int {
	return api.calculateBlockTripSequence(ctx, tripID, serviceDate)
}

// calculateBlockTripSequence calculates the index of a trip within its block's ordered trip sequence
// for trips that are active on the given service date.
// Uses GetTripsByBlockIDOrdered to perform a single SQL JOIN instead of N+1 queries.
func (api *RestAPI) calculateBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) int {
	// Get the trip to find both block_id and service_id in one query
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return 0
	}

	if !trip.BlockID.Valid || trip.BlockID.String == "" {
		return 0
	}

	// Use the optimized query that JOINs trips with stop_times in SQL,
	// ordered by first departure time — replaces N+1 queries
	orderedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		return 0
	}

	for i, t := range orderedTrips {
		if t.ID == tripID {
			return i
		}
	}
	return 0
}

func (api *RestAPI) calculateScheduleDeviationFromTripUpdates(
	tripID string,
) int {
	tripUpdates := api.GtfsManager.GetTripUpdatesForTrip(tripID)
	if len(tripUpdates) == 0 {
		return 0
	}

	tripUpdate := tripUpdates[0]

	if tripUpdate.Delay != nil {
		return int(tripUpdate.Delay.Seconds())
	}

	var bestDeviation int64 = 0
	var foundRelevantUpdate bool

	for _, stopTimeUpdate := range tripUpdate.StopTimeUpdates {
		if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Delay != nil {
			bestDeviation = int64(*stopTimeUpdate.Arrival.Delay / 1e9)
			foundRelevantUpdate = true
		} else if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Delay != nil {
			bestDeviation = int64(*stopTimeUpdate.Departure.Delay / 1e9)
			foundRelevantUpdate = true
		}

		if foundRelevantUpdate {
			break
		}
	}

	return int(bestDeviation)
}

type StopDelayInfo struct {
	ArrivalDelay   int64
	DepartureDelay int64
}

func (api *RestAPI) getStopDelaysFromTripUpdates(tripID string) map[string]StopDelayInfo {
	delays := make(map[string]StopDelayInfo)

	tripUpdates := api.GtfsManager.GetTripUpdatesForTrip(tripID)
	if len(tripUpdates) == 0 {
		return delays
	}

	tripUpdate := tripUpdates[0]

	for _, stopTimeUpdate := range tripUpdate.StopTimeUpdates {
		if stopTimeUpdate.StopID == nil {
			continue
		}

		info := StopDelayInfo{}
		if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Delay != nil {
			info.ArrivalDelay = int64(stopTimeUpdate.Arrival.Delay.Seconds())
		}
		if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Delay != nil {
			info.DepartureDelay = int64(stopTimeUpdate.Departure.Delay.Seconds())
		}

		// Only add if we have at least one delay value
		if info.ArrivalDelay != 0 || info.DepartureDelay != 0 {
			delays[*stopTimeUpdate.StopID] = info
		}
	}

	return delays
}

// calculatePreciseDistanceAlongTripWithCoords calculates the distance along a trip's shape to a stop
// This optimized version accepts pre-calculated cumulative distances and stop coordinates
func (api *RestAPI) calculatePreciseDistanceAlongTripWithCoords(
	stopLat, stopLon float64,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
) float64 {
	// Validate inputs
	if len(shapePoints) < 2 {
		return 0.0
	}

	// Validate that cumulative distances array matches shape points
	if len(cumulativeDistances) != len(shapePoints) {
		return 0.0
	}

	// Find the closest point on the shape to this stop
	var minDistance = math.Inf(1)
	var closestSegmentIndex int
	var projectionRatio float64

	for i := 0; i < len(shapePoints)-1; i++ {
		// Calculate distance from stop to this line segment
		distance, ratio := distanceToLineSegment(
			stopLat, stopLon,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
			shapePoints[i+1].Latitude, shapePoints[i+1].Longitude,
		)

		if distance < minDistance {
			minDistance = distance
			closestSegmentIndex = i
			projectionRatio = ratio
		}
	}

	// Get cumulative distance to the start of the closest segment
	// cumulativeDistances[i] represents the total distance from start to point i
	cumulativeDistance := cumulativeDistances[closestSegmentIndex]

	// Add the projection distance within the closest segment
	if closestSegmentIndex < len(shapePoints)-1 {
		segmentDistance := utils.Distance(
			shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
			shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
		)
		cumulativeDistance += segmentDistance * projectionRatio
	}

	return cumulativeDistance
}

// calculatePreciseDistanceAlongTrip is the legacy version that fetches stop coordinates from the database
// Deprecated: Use calculatePreciseDistanceAlongTripWithCoords with batch-fetched coordinates instead
func (api *RestAPI) calculatePreciseDistanceAlongTrip(ctx context.Context, stopID string, shapePoints []gtfs.ShapePoint) float64 {
	if len(shapePoints) == 0 {
		return 0.0
	}

	// Get stop coordinates
	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		return 0.0
	}

	// Pre-calculate cumulative distances (this is inefficient for multiple stops)
	cumulativeDistances := preCalculateCumulativeDistances(shapePoints)

	return api.calculatePreciseDistanceAlongTripWithCoords(stop.Lat, stop.Lon, shapePoints, cumulativeDistances)
}

// preCalculateCumulativeDistances pre-calculates cumulative distances along shape points
// Returns an array where cumulativeDistances[i] is the cumulative distance up to (but not including) segment i
func preCalculateCumulativeDistances(shapePoints []gtfs.ShapePoint) []float64 {
	if len(shapePoints) <= 1 {
		return []float64{0}
	}

	cumulativeDistances := make([]float64, len(shapePoints))
	cumulativeDistances[0] = 0

	for i := 1; i < len(shapePoints); i++ {
		segmentDistance := utils.Distance(
			shapePoints[i-1].Latitude, shapePoints[i-1].Longitude,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
		)
		cumulativeDistances[i] = cumulativeDistances[i-1] + segmentDistance
	}

	return cumulativeDistances
}

// Helper function to calculate distance from point to line segment
func distanceToLineSegment(px, py, x1, y1, x2, y2 float64) (distance, ratio float64) {
	dx := x2 - x1
	dy := y2 - y1

	if dx == 0 && dy == 0 {
		// Line segment is a point
		return utils.Distance(px, py, x1, y1), 0
	}

	// Calculate the parameter t for the projection of point onto the line
	t := ((px-x1)*dx + (py-y1)*dy) / (dx*dx + dy*dy)

	// Clamp t to [0, 1] to stay within the line segment
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	// Find the closest point on the line segment
	closestX := x1 + t*dx
	closestY := y1 + t*dy

	return utils.Distance(px, py, closestX, closestY), t
}

func (api *RestAPI) GetSituationIDsForTrip(tripID string) []string {
	alerts := api.GtfsManager.GetAlertsForTrip(tripID)

	var agencyID string
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(context.Background(), tripID)
	if err != nil {
		return nil
	}
	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(context.Background(), trip.RouteID)
	if err == nil {
		agencyID = route.AgencyID
	}

	situationIDs := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		if alert.ID != "" {
			if agencyID != "" {
				situationIDs = append(situationIDs, utils.FormCombinedID(agencyID, alert.ID))
			} else {
				situationIDs = append(situationIDs, alert.ID)
			}
		}
	}
	return situationIDs
}

type TripAgencyResolver struct {
	RouteToAgency map[string]string
	TripToRoute   map[string]string
}

// NewTripAgencyResolver creates a new TripAgencyResolver that maps trip IDs to their respective agency IDs.
func NewTripAgencyResolver(allRoutes []gtfsdb.Route, allTrips []gtfsdb.Trip) *TripAgencyResolver {
	routeToAgency := make(map[string]string, len(allRoutes))
	for _, route := range allRoutes {
		routeToAgency[route.ID] = route.AgencyID
	}
	tripToRoute := make(map[string]string, len(allTrips))
	for _, trip := range allTrips {
		tripToRoute[trip.ID] = trip.RouteID
	}
	return &TripAgencyResolver{
		RouteToAgency: routeToAgency,
		TripToRoute:   tripToRoute,
	}
}

// GetAgencyNameByTripID retrieves the agency name for a given trip ID.
func (r *TripAgencyResolver) GetAgencyNameByTripID(tripID string) string {
	routeID := r.TripToRoute[tripID]

	agency := r.RouteToAgency[routeID]

	return agency
}

func (api *RestAPI) calculateEffectiveDistanceAlongTrip(
	actualDistance float64,
	scheduleDeviation int,
	currentTime time.Time,
	stopTimes []gtfsdb.StopTime,
	cumulativeDistances []float64,
) float64 {
	if scheduleDeviation == 0 || len(stopTimes) == 0 {
		return actualDistance
	}

	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())
	effectiveScheduleTime := currentTimeSeconds - int64(scheduleDeviation)

	return api.interpolateDistanceAtScheduledTime(effectiveScheduleTime, stopTimes, cumulativeDistances)
}

func (api *RestAPI) interpolateDistanceAtScheduledTime(
	scheduledTime int64,
	stopTimes []gtfsdb.StopTime,
	cumulativeDistances []float64,
) float64 {
	if len(stopTimes) == 0 {
		return 0
	}

	for i := 0; i < len(stopTimes)-1; i++ {
		fromStop := stopTimes[i]
		toStop := stopTimes[i+1]

		fromTime := fromStop.DepartureTime / 1e9
		toTime := toStop.ArrivalTime / 1e9

		if scheduledTime >= fromTime && scheduledTime <= toTime {
			if toTime == fromTime {
				return cumulativeDistances[i]
			}

			timeRatio := float64(scheduledTime-fromTime) / float64(toTime-fromTime)

			fromDistance := cumulativeDistances[i*len(cumulativeDistances)/len(stopTimes)]
			toDistance := cumulativeDistances[(i+1)*len(cumulativeDistances)/len(stopTimes)]

			return fromDistance + timeRatio*(toDistance-fromDistance)
		}
	}

	if scheduledTime < stopTimes[0].ArrivalTime/1e9 {
		return 0
	}

	return cumulativeDistances[len(cumulativeDistances)-1]
}

func (api *RestAPI) calculateOffsetForStop(
	stopID string,
	stopTimes []*gtfsdb.StopTime,
	currentTime time.Time,
	scheduleDeviation int,
) int {
	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	for _, st := range stopTimes {
		if st.StopID == stopID {
			stopTimeSeconds := st.ArrivalTime / 1e9
			if stopTimeSeconds == 0 {
				stopTimeSeconds = st.DepartureTime / 1e9
			}
			predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
			return int(predictedArrival - currentTimeSeconds)
		}
	}

	return 0
}

func (api *RestAPI) findNextStopAfter(
	currentStopID string,
	stopTimes []*gtfsdb.StopTime,
	currentTime time.Time,
	scheduleDeviation int,
) (stopID string, offset int) {
	if len(stopTimes) == 0 {
		return "", 0
	}

	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	for i, st := range stopTimes {
		if st.StopID == currentStopID {
			if i+1 < len(stopTimes) {
				nextSt := stopTimes[i+1]
				stopTimeSeconds := nextSt.ArrivalTime / 1e9
				if stopTimeSeconds == 0 {
					stopTimeSeconds = nextSt.DepartureTime / 1e9
				}
				predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
				return nextSt.StopID, int(predictedArrival - currentTimeSeconds)
			}
			break
		}
	}

	return "", 0
}

func (api *RestAPI) findStopsByScheduleDeviation(
	stopTimes []*gtfsdb.StopTime,
	currentTime time.Time,
	scheduleDeviation int,
) (closestStopID string, closestOffset int, nextStopID string, nextOffset int) {
	if len(stopTimes) == 0 {
		return "", 0, "", 0
	}

	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	effectiveScheduleTime := currentTimeSeconds - int64(scheduleDeviation)

	var closestStop *gtfsdb.StopTime
	var closestTimeDiff int64 = math.MaxInt64

	for _, st := range stopTimes {
		stopTime := st.ArrivalTime / 1e9
		if stopTime == 0 {
			stopTime = st.DepartureTime / 1e9
		}

		timeDiff := stopTime - effectiveScheduleTime
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		if timeDiff < closestTimeDiff {
			closestTimeDiff = timeDiff
			closestStop = st
		}
	}

	if closestStop == nil {
		return "", 0, "", 0
	}

	closestStopID = closestStop.StopID

	closestStopTime := closestStop.ArrivalTime / 1e9
	if closestStopTime == 0 {
		closestStopTime = closestStop.DepartureTime / 1e9
	}
	predictedClosestArrival := closestStopTime + int64(scheduleDeviation)
	closestOffset = int(predictedClosestArrival - currentTimeSeconds)

	for i, st := range stopTimes {
		if st.StopID == closestStopID {
			if i+1 < len(stopTimes) {
				nextSt := stopTimes[i+1]
				nextStopID = nextSt.StopID

				nextStopTime := nextSt.ArrivalTime / 1e9
				if nextStopTime == 0 {
					nextStopTime = nextSt.DepartureTime / 1e9
				}
				predictedNextArrival := nextStopTime + int64(scheduleDeviation)
				nextOffset = int(predictedNextArrival - currentTimeSeconds)
			}
			break
		}
	}

	return closestStopID, closestOffset, nextStopID, nextOffset
}

func (api *RestAPI) findClosestStopBySequence(
	stopTimes []*gtfsdb.StopTime,
	currentStopSequence uint32,
	currentTime time.Time,
	scheduleDeviation int,
	vehicle *gtfs.Vehicle,
) (stopID string, offset int) {
	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	for _, st := range stopTimes {
		if uint32(st.StopSequence) == currentStopSequence {
			isAtCurrentStop := vehicle != nil && vehicle.CurrentStatus != nil &&
				*vehicle.CurrentStatus == gtfs.CurrentStatus(1)

			var closestStop *gtfsdb.StopTime
			if isAtCurrentStop {
				closestStop = st
			} else {
				closestStop = st
			}

			if closestStop != nil {
				stopTimeSeconds := closestStop.ArrivalTime / 1e9
				if stopTimeSeconds == 0 {
					stopTimeSeconds = closestStop.DepartureTime / 1e9
				}
				predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
				return closestStop.StopID, int(predictedArrival - currentTimeSeconds)
			}
		}
	}

	return "", 0
}

func (api *RestAPI) findNextStopBySequence(
	ctx context.Context,
	stopTimes []*gtfsdb.StopTime,
	currentStopSequence uint32,
	currentTime time.Time,
	scheduleDeviation int,
	vehicle *gtfs.Vehicle,
	tripID string,
	serviceDate time.Time,
) (stopID string, offset int) {
	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	isAtCurrentStop := vehicle != nil && vehicle.CurrentStatus != nil &&
		*vehicle.CurrentStatus == gtfs.CurrentStatus(1)

	for i, st := range stopTimes {
		if uint32(st.StopSequence) == currentStopSequence {
			var nextStop *gtfsdb.StopTime

			if isAtCurrentStop {
				if i+1 < len(stopTimes) {
					nextStop = stopTimes[i+1]
				} else {
					nextStop = api.getFirstStopOfNextTripInBlock(ctx, tripID, serviceDate)
				}
			} else {
				nextStop = st
			}

			if nextStop != nil {
				stopTimeSeconds := nextStop.ArrivalTime / 1e9
				if stopTimeSeconds == 0 {
					stopTimeSeconds = nextStop.DepartureTime / 1e9
				}
				predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
				return nextStop.StopID, int(predictedArrival - currentTimeSeconds)
			}
		}
	}

	return "", 0
}

func (api *RestAPI) getFirstStopOfNextTripInBlock(ctx context.Context, currentTripID string, serviceDate time.Time) *gtfsdb.StopTime {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, currentTripID)
	if err != nil || !trip.BlockID.Valid {
		return nil
	}

	orderedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		return nil
	}

	currentIndex := -1
	for i, t := range orderedTrips {
		if t.ID == currentTripID {
			currentIndex = i
			break
		}
	}

	if currentIndex >= 0 && currentIndex+1 < len(orderedTrips) {
		nextTripID := orderedTrips[currentIndex+1].ID
		nextTripStopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, nextTripID)
		if err == nil && len(nextTripStopTimes) > 0 {
			return &nextTripStopTimes[0]
		}
	}

	return nil
}

func (api *RestAPI) calculateBlockLevelPosition(
	ctx context.Context,
	tripID string,
	vehicle *gtfs.Vehicle,
	currentTime time.Time,
	scheduleDeviation int,
) (activeTripID string, closestStopID string, nextStopID string, distanceAlongTrip float64, err error) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return tripID, "", "", 0, err
	}

	if !trip.BlockID.Valid {
		return tripID, "", "", 0, nil
	}

	year, month, day := currentTime.Date()
	serviceDate := time.Date(year, month, day, 0, 0, 0, 0, currentTime.Location())
	serviceDateUnix := serviceDate.Unix()

	currentTimestamp := currentTime.Unix()
	effectiveScheduledTime := currentTimestamp - int64(scheduleDeviation)
	effectiveTimeFromMidnight := effectiveScheduledTime - serviceDateUnix

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		return tripID, "", "", 0, err
	}

	var blockStopTimes []BlockStopTimeInfo
	var cumulativeDistance float64

	for _, blockTrip := range blockTrips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil {
			continue
		}

		shapeRows, _ := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, blockTrip.ID)
		tripDistance := calculateTripDistance(shapeRows)

		for i, st := range stopTimes {
			arrivalTime := st.ArrivalTime / 1e9
			if arrivalTime == 0 {
				arrivalTime = st.DepartureTime / 1e9
			}

			var stopDistance float64
			if len(stopTimes) > 1 {
				stopDistance = cumulativeDistance + (float64(i) / float64(len(stopTimes)-1) * tripDistance)
			} else {
				stopDistance = cumulativeDistance
			}

			blockStopTimes = append(blockStopTimes, BlockStopTimeInfo{
				TripID:        blockTrip.ID,
				StopID:        st.StopID,
				ArrivalTime:   arrivalTime,
				StopSequence:  int(st.StopSequence),
				Distance:      stopDistance,
				IsFirstInTrip: i == 0,
				IsLastInTrip:  i == len(stopTimes)-1,
			})
		}

		cumulativeDistance += tripDistance
	}

	if len(blockStopTimes) == 0 {
		return tripID, "", "", 0, nil
	}

	closestIndex := -1
	var minTimeDiff int64 = math.MaxInt64

	for i, bst := range blockStopTimes {
		timeDiff := bst.ArrivalTime - effectiveTimeFromMidnight
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}
		if timeDiff < minTimeDiff {
			minTimeDiff = timeDiff
			closestIndex = i
		}
	}

	if closestIndex < 0 {
		return tripID, "", "", 0, nil
	}

	activeTripID = blockStopTimes[closestIndex].TripID
	closestStopID = blockStopTimes[closestIndex].StopID
	distanceAlongTrip = blockStopTimes[closestIndex].Distance

	if closestIndex+1 < len(blockStopTimes) {
		nextStopID = blockStopTimes[closestIndex+1].StopID
	}

	return activeTripID, closestStopID, nextStopID, distanceAlongTrip, nil
}

type BlockStopTimeInfo struct {
	TripID        string
	StopID        string
	ArrivalTime   int64
	StopSequence  int
	Distance      float64
	IsFirstInTrip bool
	IsLastInTrip  bool
}

func calculateTripDistance(shapeRows []gtfsdb.Shape) float64 {
	if len(shapeRows) < 2 {
		return 0
	}

	totalDistance := 0.0
	for i := 1; i < len(shapeRows); i++ {
		dist := utils.Distance(
			shapeRows[i-1].Lat, shapeRows[i-1].Lon,
			shapeRows[i].Lat, shapeRows[i].Lon,
		)
		totalDistance += dist
	}
	return totalDistance
}

func (api *RestAPI) fillStopsFromSchedule(ctx context.Context, status *models.TripStatusForTripDetails, tripID string, currentTime time.Time, agencyID string) {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err != nil || len(stopTimes) == 0 {
		return
	}

	currentSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	for i, st := range stopTimes {
		arrivalTime := st.ArrivalTime / 1e9
		if arrivalTime == 0 {
			arrivalTime = st.DepartureTime / 1e9
		}

		predictedArrival := arrivalTime + int64(status.ScheduleDeviation)

		if predictedArrival > currentSeconds {
			if i > 0 {
				status.ClosestStop = utils.FormCombinedID(agencyID, stopTimes[i-1].StopID)
				closestArrival := stopTimes[i-1].ArrivalTime / 1e9
				if closestArrival == 0 {
					closestArrival = stopTimes[i-1].DepartureTime / 1e9
				}
				status.ClosestStopTimeOffset = int(closestArrival + int64(status.ScheduleDeviation) - currentSeconds)
			}
			status.NextStop = utils.FormCombinedID(agencyID, st.StopID)
			status.NextStopTimeOffset = int(predictedArrival - currentSeconds)
			return
		}
	}

	if len(stopTimes) > 0 {
		lastStop := stopTimes[len(stopTimes)-1]
		status.ClosestStop = utils.FormCombinedID(agencyID, lastStop.StopID)
		arrivalTime := lastStop.ArrivalTime / 1e9
		if arrivalTime == 0 {
			arrivalTime = lastStop.DepartureTime / 1e9
		}
		status.ClosestStopTimeOffset = int(arrivalTime + int64(status.ScheduleDeviation) - currentSeconds)
	}
}
