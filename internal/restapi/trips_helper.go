package restapi

import (
	"context"
	"math"
	"sort"
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
	vehicle := api.GtfsManager.GetVehicleForTrip(tripID)

	var occupancyStatus string
	var vehicleID string

	if vehicle != nil {
		if vehicle.OccupancyStatus != nil {
			occupancyStatus = vehicle.OccupancyStatus.String()
		}

		if vehicle.ID != nil {
			vehicleID = vehicle.ID.ID
		}
	}

	status := &models.TripStatusForTripDetails{
		ServiceDate:       serviceDate.Unix() * 1000,
		VehicleID:         vehicleID,
		OccupancyStatus:   occupancyStatus,
		SituationIDs:      api.GetSituationIDsForTrip(tripID),
		OccupancyCapacity: -1,
		OccupancyCount:    -1,
	}

	api.BuildVehicleStatus(ctx, vehicle, tripID, agencyID, status)
	activeTripID := GetVehicleActiveTripID(vehicle)

	if vehicle != nil && vehicle.OccupancyPercentage != nil {
		status.OccupancyCapacity = int(*vehicle.OccupancyPercentage)
	}

	scheduleDeviation := api.calculateScheduleDeviationFromTripUpdates(tripID)
	status.ScheduleDeviation = scheduleDeviation

	blockTripSequence := api.setBlockTripSequence(ctx, tripID, serviceDate, status)
	if blockTripSequence > 0 {
		status.BlockTripSequence = blockTripSequence
	}

	shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	if err == nil && len(shapeRows) > 1 {
		shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{
				Latitude:  sp.Lat,
				Longitude: sp.Lon,
			}
		}
		status.TotalDistanceAlongTrip = preCalculateCumulativeDistances(shapePoints)[len(shapePoints)-1]

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			status.DistanceAlongTrip = api.getVehicleDistanceAlongShapeContextual(ctx, tripID, vehicle)
		}
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, activeTripID)
	if err == nil {
		stopTimesPtrs := make([]*gtfsdb.StopTime, len(stopTimes))
		for i := range stopTimes {
			stopTimesPtrs[i] = &stopTimes[i]
		}

		shapeRows, err := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
		if err != nil {
			shapeRows = []gtfsdb.Shape{}
		}

		shapePoints := make([]gtfs.ShapePoint, len(shapeRows))
		for i, sp := range shapeRows {
			shapePoints[i] = gtfs.ShapePoint{
				Latitude:  sp.Lat,
				Longitude: sp.Lon,
			}
		}

		var closestStopID, nextStopID string
		var closestOffset, nextOffset int

		if vehicle != nil && vehicle.Position != nil {
			closestStopID, closestOffset = findClosestStop(api, ctx, vehicle.Position, stopTimesPtrs, currentTime, vehicle)
			nextStopID, nextOffset = findNextStop(api, stopTimesPtrs, vehicle, currentTime)
		} else {
			// Use real-time delays for more accurate stop predictions
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

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, trip.BlockID)
	if err != nil {
		return "", "", nil, err
	}
	if len(blockTrips) == 0 {
		return "", "", nil, nil
	}

	type TripWithDetails struct {
		TripID    string
		StartTime int
		EndTime   int
		IsActive  bool
		StopTimes []gtfsdb.StopTime
	}

	var tripsWithDetails []TripWithDetails

	for _, blockTrip := range blockTrips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		startTime := math.MaxInt // max int value
		endTime := 0

		// Find the first stop time with a valid departure time (intentionally only the first)
		for _, st := range stopTimes {
			if st.DepartureTime > 0 {
				startTime = int(st.DepartureTime)
				break
			}
		}

		// Find the last stop time with a valid arrival time
		for i := len(stopTimes) - 1; i >= 0; i-- {
			if stopTimes[i].ArrivalTime > 0 {
				endTime = int(stopTimes[i].ArrivalTime)
				break
			}
		}

		if startTime != math.MaxInt && endTime > 0 {
			// Only include trips that match the service ID of the original trip
			if trip.ServiceID != blockTrip.ServiceID {
				continue
			}

			tripsWithDetails = append(tripsWithDetails, TripWithDetails{
				TripID:    blockTrip.ID,
				StartTime: startTime,
				EndTime:   endTime,
				IsActive:  true,
				StopTimes: stopTimes,
			})
		}
	}

	// Sort trips first by start time (chronologically), and then by trip ID to ensure a stable and deterministic order when start times are equal.
	// This ensures consistent ordering of trips with identical start times.
	sort.Slice(tripsWithDetails, func(i, j int) bool {
		if tripsWithDetails[i].StartTime == tripsWithDetails[j].StartTime {
			return tripsWithDetails[i].TripID < tripsWithDetails[j].TripID
		}
		return tripsWithDetails[i].StartTime < tripsWithDetails[j].StartTime
	})

	currentIndex := -1
	for i, t := range tripsWithDetails {
		if t.TripID == trip.ID {
			currentIndex = i
			break
		}
	}

	if currentIndex != -1 {
		if currentIndex > 0 {
			previousTripID = utils.FormCombinedID(agencyID, tripsWithDetails[currentIndex-1].TripID)
		}

		if currentIndex < len(tripsWithDetails)-1 {
			nextTripID = utils.FormCombinedID(agencyID, tripsWithDetails[currentIndex+1].TripID)
		}
	}
	if currentIndex == -1 {
		// If the trip is not found, return empty values
		return "", "", nil, nil
	}
	return nextTripID, previousTripID, tripsWithDetails[currentIndex].StopTimes, nil
}

func findNextStop(
	api *RestAPI,
	stopTimes []*gtfsdb.StopTime,
	vehicle *gtfs.Vehicle,
	currentTime time.Time,
) (stopID string, offset int) {

	if vehicle == nil || vehicle.CurrentStopSequence == nil {
		return "", 0
	}

	vehicleCurrentStopSequence := vehicle.CurrentStopSequence
	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	// INCOMING_AT (0): vehicle is just about to arrive at currentStopSequence - that IS the next stop
	// STOPPED_AT (1): vehicle is at currentStopSequence - next stop is the one after
	// IN_TRANSIT_TO (2): vehicle has departed previous stop, heading to currentStopSequence - that IS the next stop
	isAtCurrentStop := vehicle.CurrentStatus != nil && *vehicle.CurrentStatus == gtfs.CurrentStatus(1)

	for i, st := range stopTimes {
		if uint32(st.StopSequence) == *vehicleCurrentStopSequence {
			var nextSt *gtfsdb.StopTime

			if isAtCurrentStop {
				if i+1 < len(stopTimes) {
					nextSt = stopTimes[i+1]
				}
			} else {
				nextSt = st
			}

			if nextSt != nil {
				stopTimeSeconds := nextSt.ArrivalTime / 1e9
				if stopTimeSeconds == 0 {
					stopTimeSeconds = nextSt.DepartureTime / 1e9
				}
				return nextSt.StopID, int(stopTimeSeconds - currentTimeSeconds)
			}
		}
	}

	return "", 0
}

func findClosestStop(api *RestAPI, ctx context.Context, pos *gtfs.Position, stopTimes []*gtfsdb.StopTime, currentTime time.Time, vehicle *gtfs.Vehicle) (stopID string, offset int) {
	if pos == nil || pos.Latitude == nil || pos.Longitude == nil {
		return "", 0
	}

	currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())

	if vehicle != nil && vehicle.CurrentStopSequence != nil {

		isAtCurrentStop := vehicle.CurrentStatus != nil && *vehicle.CurrentStatus == gtfs.CurrentStatus(1)

		for i, st := range stopTimes {
			if uint32(st.StopSequence) == *vehicle.CurrentStopSequence {
				var closestSt *gtfsdb.StopTime

				if isAtCurrentStop {
					closestSt = st
				} else {

					if i > 0 {
						prevSt := stopTimes[i-1]
						currSt := st

						stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, []string{prevSt.StopID, currSt.StopID})
						if err == nil && len(stops) == 2 {
							stopMap := make(map[string]gtfsdb.Stop)
							for _, s := range stops {
								stopMap[s.ID] = s
							}

							prevStop := stopMap[prevSt.StopID]
							currStop := stopMap[currSt.StopID]

							distToPrev := utils.Distance(float64(*pos.Latitude), float64(*pos.Longitude), prevStop.Lat, prevStop.Lon)
							distToCurr := utils.Distance(float64(*pos.Latitude), float64(*pos.Longitude), currStop.Lat, currStop.Lon)

							if distToPrev < distToCurr {
								closestSt = prevSt
							} else {
								closestSt = currSt
							}
						} else {
							closestSt = st
						}
					} else {
						closestSt = st
					}
				}

				if closestSt != nil {
					stopTimeSeconds := closestSt.ArrivalTime / 1e9
					if stopTimeSeconds == 0 {
						stopTimeSeconds = closestSt.DepartureTime / 1e9
					}
					return closestSt.StopID, int(stopTimeSeconds - currentTimeSeconds)
				}
			}
		}
	}

	var minDist = math.MaxFloat64
	var closestStopTime *gtfsdb.StopTime

	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return "", 0
	}

	stopMap := make(map[string]gtfsdb.Stop)
	for _, stop := range stops {
		stopMap[stop.ID] = stop
	}

	for _, st := range stopTimes {
		stop, exists := stopMap[st.StopID]
		if !exists {
			continue
		}

		d := utils.Distance(
			float64(*pos.Latitude),
			float64(*pos.Longitude),
			stop.Lat,
			stop.Lon,
		)

		if d < minDist {
			minDist = d
			stopID = stop.ID
			closestStopTime = st
		}
	}

	if closestStopTime != nil {
		currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())
		stopTimeSeconds := closestStopTime.ArrivalTime / 1e9
		if stopTimeSeconds == 0 {
			stopTimeSeconds = closestStopTime.DepartureTime / 1e9
		}
		offset = int(stopTimeSeconds - currentTimeSeconds)
	}

	return
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

func findNextStopByTime(currentTimeSeconds int64, stopTimes []*gtfsdb.StopTime) (stopID string, offset int) {
	return findNextStopByTimeWithDelays(currentTimeSeconds, stopTimes, nil)
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
// for trips that are active on the given service date
func (api *RestAPI) calculateBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) int {
	blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, tripID)

	if err != nil || !blockID.Valid || blockID.String == "" {
		return 0
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, blockID)
	if err != nil {
		return 0
	}

	type TripWithDetails struct {
		TripID    string
		StartTime int
	}

	activeTrips := []TripWithDetails{}

	for _, blockTrip := range blockTrips {
		// First check if trip is active on the service date
		isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, blockTrip.ServiceID, serviceDate)
		if err != nil || isActive == 0 {
			continue
		}

		// Second, get the start time for this trip
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		startTime := math.MaxInt
		for _, st := range stopTimes {
			if st.DepartureTime > 0 && int(st.DepartureTime) < startTime {
				startTime = int(st.DepartureTime)
			}
		}

		if startTime != math.MaxInt {
			activeTrips = append(activeTrips, TripWithDetails{
				TripID:    blockTrip.ID,
				StartTime: startTime,
			})
		}
	}

	// Third, sort trips by start time
	sort.Slice(activeTrips, func(i, j int) bool {
		return activeTrips[i].StartTime < activeTrips[j].StartTime
	})

	for i, trip := range activeTrips {
		if trip.TripID == tripID {
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

	var bestDeviation int64 = 0
	var foundRelevantUpdate bool

	for _, stopTimeUpdate := range tripUpdate.StopTimeUpdates {
		if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Delay != nil {
			bestDeviation = int64(*stopTimeUpdate.Arrival.Delay)
			foundRelevantUpdate = true
		} else if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Delay != nil {
			bestDeviation = int64(*stopTimeUpdate.Departure.Delay)
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
