package restapi

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/jamespfennell/gtfs"
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
		ServiceDate:     serviceDate.Unix() * 1000,
		VehicleID:       vehicleID,
		OccupancyStatus: occupancyStatus,
		SituationIDs:    []string{},
	}

	api.BuildVehicleStatus(ctx, vehicle, tripID, agencyID, status)

	if vehicle != nil && vehicle.OccupancyPercentage != nil {
		status.OccupancyCapacity = int(*vehicle.OccupancyPercentage)
	}

	scheduleDeviation := api.calculateScheduleDeviationFromTripUpdates(tripID)
	status.ScheduleDeviation = scheduleDeviation

	blockTripSequence := api.setBlockTripSequence(ctx, tripID, status)
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
		status.TotalDistanceAlongTrip = getDistanceAlongShape(shapePoints[0].Latitude, shapePoints[0].Longitude, shapePoints)

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			status.DistanceAlongTrip = getDistanceAlongShape(float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude), shapePoints)
		}
	}

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
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
			closestStopID, closestOffset = findClosestStop(api, ctx, vehicle.Position, stopTimesPtrs)
			nextStopID, nextOffset = findNextStop(api, ctx, vehicle.Position, stopTimesPtrs, shapePoints)
		} else {
			currentTimeSeconds := int64(currentTime.Hour()*3600 + currentTime.Minute()*60 + currentTime.Second())
			closestStopID, closestOffset = findClosestStopByTime(currentTimeSeconds, stopTimesPtrs)
			nextStopID, nextOffset = findNextStopByTime(currentTimeSeconds, stopTimesPtrs)
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

func (api *RestAPI) BuildTripSchedule(ctx context.Context, agencyID, tripID, nextTripID, previousTripID string, loc *time.Location) (*models.Schedule, error) {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err != nil {
		return nil, err
	}

	stopTimesVals := make([]models.StopTime, len(stopTimes))
	for i, st := range stopTimes {
		stopTimesVals[i] = models.StopTime{
			ArrivalTime:         int(st.ArrivalTime),
			DepartureTime:       int(st.DepartureTime),
			StopID:              utils.FormCombinedID(agencyID, st.StopID),
			StopHeadsign:        st.StopHeadsign.String,
			DistanceAlongTrip:   st.ShapeDistTraveled.Float64,
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

func (api *RestAPI) GetNextAndPreviousTripIDs(ctx context.Context, trip *gtfsdb.Trip, tripID string, agencyID string, serviceDate time.Time) (nextTripID string, previousTripID string, err error) {
	if !trip.BlockID.Valid {
		return "", "", nil
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockID(ctx, trip.BlockID)
	if err != nil {
		return "", "", err
	}

	if len(blockTrips) == 0 {
		return "", "", nil
	}

	type TripWithDetails struct {
		TripID    string
		StartTime int
		EndTime   int
		IsActive  bool
	}

	tripsWithDetails := []TripWithDetails{}

	for _, blockTrip := range blockTrips {
		isActive, err := api.GtfsManager.IsServiceActiveOnDate(ctx, blockTrip.ServiceID, serviceDate)
		if err != nil || isActive == 0 {
			continue
		}

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		startTime := int(^uint(0) >> 1) // max int value
		endTime := 0

		for _, st := range stopTimes {
			if st.DepartureTime > 0 && int(st.DepartureTime) < startTime {
				startTime = int(st.DepartureTime)
			}

			if st.ArrivalTime > 0 && int(st.ArrivalTime) > endTime {
				endTime = int(st.ArrivalTime)
			}
		}

		if startTime != int(^uint(0)>>1) && endTime > 0 {
			tripsWithDetails = append(tripsWithDetails, TripWithDetails{
				TripID:    blockTrip.ID,
				StartTime: startTime,
				EndTime:   endTime,
				IsActive:  isActive > 0,
			})
		}
	}

	sort.Slice(tripsWithDetails, func(i, j int) bool {
		if tripsWithDetails[i].IsActive && !tripsWithDetails[j].IsActive {
			return true
		}
		if !tripsWithDetails[i].IsActive && tripsWithDetails[j].IsActive {
			return false
		}
		return tripsWithDetails[i].StartTime < tripsWithDetails[j].StartTime
	})

	currentIndex := -1
	for i, t := range tripsWithDetails {
		if t.TripID == tripID {
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

	return nextTripID, previousTripID, nil
}

func findNextStop(
	api *RestAPI,
	ctx context.Context,
	pos *gtfs.Position,
	stopTimes []*gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
) (stopID string, offset int) {
	if pos == nil || pos.Latitude == nil || pos.Longitude == nil {
		return "", 0
	}

	currentDistance := getDistanceAlongShape(float64(*pos.Latitude), float64(*pos.Longitude), shapePoints)

	var minDiff float64 = math.MaxFloat64

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

		stopDist := getDistanceAlongShape(stop.Lat, stop.Lon, shapePoints)
		if stopDist > currentDistance && stopDist-currentDistance < minDiff {
			minDiff = stopDist - currentDistance
			stopID = stop.ID
			offset = int(st.StopSequence)
		}
	}
	return
}

func findClosestStop(api *RestAPI, ctx context.Context, pos *gtfs.Position, stopTimes []*gtfsdb.StopTime) (stopID string, offset int) {
	if pos == nil || pos.Latitude == nil || pos.Longitude == nil {
		return "", 0
	}

	var minDist float64 = math.MaxFloat64

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

		d := utils.Haversine(
			float64(*pos.Latitude),
			float64(*pos.Longitude),
			stop.Lat,
			stop.Lon,
		)

		if d < minDist {
			minDist = d
			stopID = stop.ID
			offset = int(st.StopSequence)
		}
	}

	return
}

func findClosestStopByTime(currentTimeSeconds int64, stopTimes []*gtfsdb.StopTime) (stopID string, offset int) {
	var minTimeDiff int64 = math.MaxInt64

	for _, st := range stopTimes {
		var stopTime int64
		if st.DepartureTime > 0 {
			stopTime = int64(st.DepartureTime)
		} else if st.ArrivalTime > 0 {
			stopTime = int64(st.ArrivalTime)
		} else {
			continue
		}

		timeDiff := int64(math.Abs(float64(currentTimeSeconds - stopTime)))
		if timeDiff < minTimeDiff {
			minTimeDiff = timeDiff
			stopID = st.StopID
			offset = int(st.StopSequence)
		}
	}

	return
}

func findNextStopByTime(currentTimeSeconds int64, stopTimes []*gtfsdb.StopTime) (stopID string, offset int) {
	var minTimeDiff int64 = math.MaxInt64

	for _, st := range stopTimes {
		var stopTime int64
		if st.DepartureTime > 0 {
			stopTime = int64(st.DepartureTime)
		} else if st.ArrivalTime > 0 {
			stopTime = int64(st.ArrivalTime)
		} else {
			continue
		}

		// Only consider stops that are in the future
		if stopTime > currentTimeSeconds {
			timeDiff := stopTime - currentTimeSeconds
			if timeDiff < minTimeDiff {
				minTimeDiff = timeDiff
				stopID = st.StopID
				offset = int(st.StopSequence)
			}
		}
	}

	return
}

func getDistanceAlongShape(lat, lon float64, shape []gtfs.ShapePoint) float64 {
	var total float64
	var closestIndex int
	var minDist = math.MaxFloat64

	for i := range shape {
		dist := utils.Haversine(lat, lon, shape[i].Latitude, shape[i].Longitude)
		if dist < minDist {
			minDist = dist
			closestIndex = i
		}
	}

	for i := 1; i <= closestIndex; i++ {
		total += utils.Haversine(shape[i-1].Latitude, shape[i-1].Longitude, shape[i].Latitude, shape[i].Longitude)
	}

	return total
}

func (api *RestAPI) setBlockTripSequence(ctx context.Context, tripID string, status *models.TripStatusForTripDetails) int {
	blockID, err := api.GtfsManager.GtfsDB.Queries.GetBlockIDByTripID(ctx, tripID)

	if err != nil || !blockID.Valid || blockID.String == "" {
		return 0
	}

	blockTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, blockID)
	if err == nil {
		for _, bt := range blockTrips {
			if bt.ID == tripID {
				return status.BlockTripSequence
			}
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

func (api *RestAPI) GetSituationIDsForTrip(tripID string) []string {
	alerts := api.GtfsManager.GetAlertsForTrip(tripID)
	situationIDs := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		if alert.ID != "" {
			situationIDs = append(situationIDs, alert.ID)
		}
	}
	return situationIDs
}
