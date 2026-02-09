package restapi

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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
			vehicleID = utils.FormCombinedID(agencyID, vehicle.ID.ID)
		}
	}

	status := &models.TripStatusForTripDetails{
		ServiceDate:     serviceDate.Unix() * 1000,
		VehicleID:       vehicleID,
		OccupancyStatus: occupancyStatus,
		SituationIDs:    []string{},
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
			closestStopID, closestOffset = findClosestStop(api, ctx, vehicle.Position, stopTimesPtrs)
			nextStopID, nextOffset = findNextStop(api, stopTimesPtrs, vehicle)
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

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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

	stopTimesVals := api.calculateBatchStopDistances(stopTimes, shapePoints, stopCoords, agencyID)

	return &models.Schedule{
		StopTimes:      stopTimesVals,
		TimeZone:       loc.String(),
		Frequency:      0,
		NextTripID:     nextTripID,
		PreviousTripID: previousTripID,
	}, nil
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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
) (stopID string, offset int) {

	if vehicle == nil || vehicle.CurrentStopSequence == nil {
		return "", 0
	}

	vehicleCurrentStopSequence := vehicle.CurrentStopSequence

	for i, st := range stopTimes {
		if uint32(st.StopSequence) == *vehicleCurrentStopSequence {
			if len(stopTimes) > 0 {
				nextIdx := (i + 1) % len(stopTimes)
				return stopTimes[nextIdx].StopID, 0
			}
		}
	}

	return "", 0
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func findClosestStop(api *RestAPI, ctx context.Context, pos *gtfs.Position, stopTimes []*gtfsdb.StopTime) (stopID string, offset int) {
	if pos == nil || pos.Latitude == nil || pos.Longitude == nil {
		return "", 0
	}

	var minDist = math.MaxFloat64

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

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) setBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time, status *models.TripStatusForTripDetails) int {
	return api.calculateBlockTripSequence(ctx, tripID, serviceDate)
}

// calculateBlockTripSequence calculates the index of a trip within its block's ordered trip sequence
// for trips that are active on the given service date
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) calculateBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) int {
	// Use a read-only transaction to ensure a consistent view of the data
	// This prevents race conditions during GTFS hot-swap operations where
	// multiple queries could read from different versions of the database
	tx, err := api.GtfsManager.GtfsDB.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0
	}
	defer func() {
		_ = tx.Rollback() // Safe to call even after commit
	}()

	// Use transaction-scoped queries for consistent reads
	qtx := api.GtfsManager.GtfsDB.Queries.WithTx(tx)

	return api.calculateBlockTripSequenceWithTx(ctx, qtx, tripID, serviceDate)
}

// calculateBlockTripSequenceWithTx performs the block trip sequence calculation
// using the provided queries interface (can be regular or transaction-scoped)
func (api *RestAPI) calculateBlockTripSequenceWithTx(ctx context.Context, queries *gtfsdb.Queries, tripID string, serviceDate time.Time) int {
	blockID, err := queries.GetBlockIDByTripID(ctx, tripID)

	if err != nil || !blockID.Valid || blockID.String == "" {
		return 0
	}

	blockTrips, err := queries.GetTripsByBlockID(ctx, blockID)
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
		stopTimes, err := queries.GetStopTimesForTrip(ctx, blockTrip.ID)
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
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
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

// calculateBatchStopDistances calculates distances for the entire trip using Monotonic Search (O(N+M))
func (api *RestAPI) calculateBatchStopDistances(
	timeStops []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	stopCoords map[string]struct{ lat, lon float64 },
	agencyID string,
) []models.StopTime {

	stopTimesList := make([]models.StopTime, 0, len(timeStops))

	if len(shapePoints) < 2 {
		for _, stopTime := range timeStops {
			stopTimesList = append(stopTimesList, models.StopTime{
				StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
				ArrivalTime:         int(stopTime.ArrivalTime / 1e9),
				DepartureTime:       int(stopTime.DepartureTime / 1e9),
				StopHeadsign:        utils.NullStringOrEmpty(stopTime.StopHeadsign),
				DistanceAlongTrip:   0.0,
				HistoricalOccupancy: "",
			})
		}
		return stopTimesList
	}

	// Pre-calculate cumulative distances
	cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
	if len(cumulativeDistances) != len(shapePoints) {
		for _, stopTime := range timeStops {
			stopTimesList = append(stopTimesList, models.StopTime{
				StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
				ArrivalTime:         int(stopTime.ArrivalTime / 1e9),
				DepartureTime:       int(stopTime.DepartureTime / 1e9),
				StopHeadsign:        utils.NullStringOrEmpty(stopTime.StopHeadsign),
				DistanceAlongTrip:   0.0,
				HistoricalOccupancy: "",
			})
		}
		return stopTimesList
	}

	lastMatchedIndex := 0

	for _, stopTime := range timeStops {
		var distanceAlongTrip float64

		// Only calculate if we have valid coordinates
		if coords, exists := stopCoords[stopTime.StopID]; exists {
			stopLat := coords.lat
			stopLon := coords.lon

			// ensure lastMatchedIndex didn't go out of bounds
			if lastMatchedIndex >= len(shapePoints)-1 {
				lastMatchedIndex = len(shapePoints) - 2
			}

			var minDistance = math.Inf(1)
			var closestSegmentIndex = lastMatchedIndex
			var projectionRatio float64

			// Early exit threshold to speed up search
			//This may be too conservative for some cases but helps performance significantly
			const earlyExitThresholdMeters = 100.0

			// Start from lastMatchedIndex
			for i := lastMatchedIndex; i < len(shapePoints)-1; i++ {
				distance, ratio := distanceToLineSegment(
					stopLat, stopLon,
					shapePoints[i].Latitude, shapePoints[i].Longitude,
					shapePoints[i+1].Latitude, shapePoints[i+1].Longitude,
				)

				if distance < minDistance {
					minDistance = distance
					closestSegmentIndex = i
					projectionRatio = ratio
					lastMatchedIndex = i
				} else if distance > minDistance+earlyExitThresholdMeters {
					// Early exit:
					break
				}
			}

			// Calculate distance along trip
			cumulativeDistance := cumulativeDistances[closestSegmentIndex]
			if closestSegmentIndex < len(shapePoints)-1 {
				segmentDistance := utils.Distance(
					shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
					shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
				)
				cumulativeDistance += segmentDistance * projectionRatio
			}
			distanceAlongTrip = cumulativeDistance
		}

		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         int(stopTime.ArrivalTime / 1e9),
			DepartureTime:       int(stopTime.DepartureTime / 1e9),
			StopHeadsign:        utils.NullStringOrEmpty(stopTime.StopHeadsign),
			DistanceAlongTrip:   distanceAlongTrip,
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
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

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) GetSituationIDsForTrip(ctx context.Context, tripID string) []string {
	alerts := api.GtfsManager.GetAlertsForTrip(ctx, tripID)

	var agencyID string
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return nil
	}
	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
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
