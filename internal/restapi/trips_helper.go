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
		status.TotalDistanceAlongTrip = getDistanceAlongShape(shapePoints[0].Latitude, shapePoints[0].Longitude, shapePoints)

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			status.DistanceAlongTrip = getDistanceAlongShape(float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude), shapePoints)
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

	stopTimesVals := make([]models.StopTime, len(stopTimes))
	for i, st := range stopTimes {
		distanceAlongTrip := api.calculatePreciseDistanceAlongTrip(ctx, st.StopID, shapePoints)

		stopTimesVals[i] = models.StopTime{
			ArrivalTime:         int(st.ArrivalTime),
			DepartureTime:       int(st.DepartureTime),
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

func (api *RestAPI) calculatePreciseDistanceAlongTrip(ctx context.Context, stopID string, shapePoints []gtfs.ShapePoint) float64 {
	if len(shapePoints) == 0 {
		return 0.0
	}

	// Get stop coordinates
	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		return 0.0
	}

	stopLat, stopLon := stop.Lat, stop.Lon

	// Find the closest point on the shape to this stop
	var minDistance float64 = math.Inf(1)
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

	// Calculate cumulative distance up to the closest segment
	var cumulativeDistance float64
	for i := 1; i <= closestSegmentIndex; i++ {
		cumulativeDistance += utils.Haversine(
			shapePoints[i-1].Latitude, shapePoints[i-1].Longitude,
			shapePoints[i].Latitude, shapePoints[i].Longitude,
		)
	}

	// Add the projection distance within the closest segment
	if closestSegmentIndex < len(shapePoints)-1 {
		segmentDistance := utils.Haversine(
			shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
			shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
		)
		cumulativeDistance += segmentDistance * projectionRatio
	}

	return cumulativeDistance
}

// Helper function to calculate distance from point to line segment
func distanceToLineSegment(px, py, x1, y1, x2, y2 float64) (distance, ratio float64) {
	dx := x2 - x1
	dy := y2 - y1

	if dx == 0 && dy == 0 {
		// Line segment is a point
		return utils.Haversine(px, py, x1, y1), 0
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

	return utils.Haversine(px, py, closestX, closestY), t
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
