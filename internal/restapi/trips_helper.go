package restapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
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
	status := &models.TripStatusForTripDetails{
		ActiveTripID:      utils.FormCombinedID(agencyID, tripID),
		ServiceDate:       serviceDate.Unix() * 1000,
		SituationIDs:      api.GetSituationIDsForTrip(ctx, tripID),
		OccupancyCapacity: -1,
		OccupancyCount:    -1,
	}

	vehicle := api.GtfsManager.GetVehicleForTrip(ctx, tripID)

	if vehicle != nil {
		if vehicle.ID != nil {
			status.VehicleID = utils.FormCombinedID(agencyID, vehicle.ID.ID)
		}
		if vehicle.OccupancyStatus != nil {
			status.OccupancyStatus = vehicle.OccupancyStatus.String()
		}
		// NOTE: GTFS-RT OccupancyPercentage (0-100%) has no direct equivalent in the
		// OBA TripStatus schema. The Java OBA server populates occupancyCapacity from
		// agency-provided vehicle capacity data, not from GTFS-RT percentages.
		// We intentionally leave OccupancyCapacity at its default (-1) here.
		// See: TripStatusBeanServiceImpl.java in onebusaway-transit-data-federation.
	}
	api.BuildVehicleStatus(ctx, vehicle, tripID, agencyID, status, currentTime)

	_, activeTripRawID, err := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID)
	if err != nil {
		return status, err
	}

	scheduleDeviation, hasRealtimeTripUpdate := api.GetScheduleDeviation(activeTripRawID)

	if hasRealtimeTripUpdate {
		status.ScheduleDeviation = scheduleDeviation
	}

	hasVehicleRealtimeData := vehicle != nil && !defaultStaleDetector.Check(vehicle, currentTime)
	status.Predicted = hasVehicleRealtimeData || hasRealtimeTripUpdate
	status.Scheduled = !status.Predicted

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, activeTripRawID)
	if err != nil {
		slog.Warn("BuildTripStatus: failed to get stop times",
			slog.String("trip_id", activeTripRawID),
			slog.String("error", err.Error()))
	}
	if err == nil && len(stopTimes) > 0 {
		stopTimesPtrs := make([]*gtfsdb.StopTime, len(stopTimes))
		for i := range stopTimes {
			stopTimesPtrs[i] = &stopTimes[i]
		}

		var closestStopID, nextStopID string
		var closestOffset, nextOffset int

		if vehicle != nil && vehicle.Position != nil {
			if vehicle.StopID != nil && *vehicle.StopID != "" {
				closestStopID = *vehicle.StopID
				closestOffset = api.calculateOffsetForStop(closestStopID, stopTimesPtrs, currentTime, serviceDate, scheduleDeviation)
				isStoppedAt := vehicle.CurrentStatus != nil && *vehicle.CurrentStatus == gtfs.CurrentStatus(1)
				if isStoppedAt {
					nextStopID, nextOffset = api.findNextStopAfter(closestStopID, stopTimesPtrs, currentTime, serviceDate, scheduleDeviation)
				} else {
					nextStopID = closestStopID
					nextOffset = closestOffset
				}
			} else if vehicle.CurrentStopSequence != nil {
				closestStopID, closestOffset = api.findClosestStopBySequence(
					stopTimesPtrs, *vehicle.CurrentStopSequence, currentTime, serviceDate, scheduleDeviation,
				)
				nextStopID, nextOffset = api.findNextStopBySequence(
					ctx, stopTimesPtrs, *vehicle.CurrentStopSequence, currentTime, serviceDate, scheduleDeviation, vehicle, tripID,
				)
			} else {
				closestStopID, closestOffset, nextStopID, nextOffset = api.findStopsByScheduleDeviation(
					stopTimesPtrs, currentTime, serviceDate, scheduleDeviation,
				)
			}
		} else {
			stopDelays := api.GetStopDelaysFromTripUpdates(activeTripRawID)
			closestStopID, closestOffset = findClosestStopByTimeWithDelays(currentTime, serviceDate, stopTimesPtrs, stopDelays)
			nextStopID, nextOffset = findNextStopByTimeWithDelays(currentTime, serviceDate, stopTimesPtrs, stopDelays)
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

	if status.ClosestStop == "" || status.NextStop == "" {
		api.fillStopsFromSchedule(ctx, status, activeTripRawID, currentTime, serviceDate, agencyID)
	}

	shapeRows, shapeErr := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, activeTripRawID)
	if shapeErr != nil {
		slog.Warn("BuildTripStatus: failed to get shape points",
			slog.String("trip_id", activeTripRawID),
			slog.String("error", shapeErr.Error()))
	}
	if shapeErr == nil && len(shapeRows) > 1 {
		shapePoints := shapeRowsToPoints(shapeRows)
		cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
		status.TotalDistanceAlongTrip = cumulativeDistances[len(cumulativeDistances)-1]

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			// Refine the raw GPS position (set by BuildVehicleStatus) by projecting
			// it onto the route shape. Reuses the already-fetched shapePoints.
			actualPosition := status.LastKnownLocation
			if projected := projectPositionWithShapePoints(shapePoints, actualPosition); projected != nil {
				status.Position = *projected
			}

			actualDistance := api.getVehicleDistanceAlongShapeContextual(ctx, activeTripRawID, vehicle)
			status.DistanceAlongTrip = actualDistance

			if scheduleDeviation != 0 && len(stopTimes) > 0 {
				scheduledDistance := api.calculateEffectiveDistanceAlongTrip(
					ctx, actualDistance, scheduleDeviation, currentTime, serviceDate,
					stopTimes, shapePoints, cumulativeDistances,
				)
				status.ScheduledDistanceAlongTrip = scheduledDistance
			}
		}
	}

	blockTripSequence := api.calculateBlockTripSequence(ctx, tripID, serviceDate)
	if blockTripSequence > 0 {
		status.BlockTripSequence = blockTripSequence
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
		shapePoints = shapeRowsToPoints(shapeRows)
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

func (api *RestAPI) fillStopsFromSchedule(ctx context.Context, status *models.TripStatusForTripDetails, tripID string, currentTime time.Time, serviceDate time.Time, agencyID string) {
	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if err != nil {
		slog.Warn("fillStopsFromSchedule: failed to get stop times",
			slog.String("trip_id", tripID),
			slog.String("error", err.Error()))
		return
	}
	if len(stopTimes) == 0 {
		return
	}

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	for i, st := range stopTimes {
		arrivalTime := utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime)
		predictedArrival := arrivalTime + int64(status.ScheduleDeviation)

		if predictedArrival > currentSeconds {
			if i > 0 && status.ClosestStop == "" {
				status.ClosestStop = utils.FormCombinedID(agencyID, stopTimes[i-1].StopID)
				closestArrival := utils.EffectiveStopTimeSeconds(stopTimes[i-1].ArrivalTime, stopTimes[i-1].DepartureTime)
				status.ClosestStopTimeOffset = int(closestArrival + int64(status.ScheduleDeviation) - currentSeconds)
			}
			if status.NextStop == "" {
				status.NextStop = utils.FormCombinedID(agencyID, st.StopID)
				status.NextStopTimeOffset = int(predictedArrival - currentSeconds)
			}
			return
		}
	}

	if len(stopTimes) > 0 && status.ClosestStop == "" {
		lastStop := stopTimes[len(stopTimes)-1]
		status.ClosestStop = utils.FormCombinedID(agencyID, lastStop.StopID)
		arrivalTime := utils.EffectiveStopTimeSeconds(lastStop.ArrivalTime, lastStop.DepartureTime)
		status.ClosestStopTimeOffset = int(arrivalTime + int64(status.ScheduleDeviation) - currentSeconds)
	}
}

func findClosestStopByTimeWithDelays(currentTime time.Time, serviceDate time.Time, stopTimes []*gtfsdb.StopTime, stopDelays map[string]StopDelayInfo) (stopID string, offset int) {
	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	var minTimeDiff int64 = math.MaxInt64
	var closestStopTimeSeconds int64

	for _, st := range stopTimes {
		// NOTE: Intentionally prefers DepartureTime over ArrivalTime, unlike
		// EffectiveStopTimeSeconds which prefers arrival. When per-stop delays
		// are available (from GTFS-RT StopTimeUpdates), departure delays are the
		// more relevant metric for predicting when the vehicle leaves a stop.
		var stopTimeSeconds int64
		if st.DepartureTime > 0 {
			stopTimeSeconds = utils.NanosToSeconds(st.DepartureTime)
		} else if st.ArrivalTime > 0 {
			stopTimeSeconds = utils.NanosToSeconds(st.ArrivalTime)
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

func findNextStopByTimeWithDelays(currentTime time.Time, serviceDate time.Time, stopTimes []*gtfsdb.StopTime, stopDelays map[string]StopDelayInfo) (stopID string, offset int) {
	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	var minTimeDiff int64 = math.MaxInt64
	var nextStopTimeSeconds int64

	for _, st := range stopTimes {
		// NOTE: Intentionally prefers DepartureTime over ArrivalTime, unlike
		// EffectiveStopTimeSeconds which prefers arrival. See comment in
		// findClosestStopByTimeWithDelays for rationale.
		var stopTimeSeconds int64
		if st.DepartureTime > 0 {
			stopTimeSeconds = utils.NanosToSeconds(st.DepartureTime)
		} else if st.ArrivalTime > 0 {
			stopTimeSeconds = utils.NanosToSeconds(st.ArrivalTime)
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
	if len(shape) < 2 {
		return 0
	}

	cumulativeDistances := preCalculateCumulativeDistances(shape)

	var minDistance = math.Inf(1)
	var closestSegmentIndex int
	var projectionRatio float64

	for i := 0; i < len(shape)-1; i++ {
		distance, ratio := distanceToLineSegment(
			lat, lon,
			shape[i].Latitude, shape[i].Longitude,
			shape[i+1].Latitude, shape[i+1].Longitude,
		)

		if distance < minDistance {
			minDistance = distance
			closestSegmentIndex = i
			projectionRatio = ratio
		}
	}

	var segmentLength float64
	if closestSegmentIndex < len(shape)-1 {
		segmentLength = utils.Distance(
			shape[closestSegmentIndex].Latitude, shape[closestSegmentIndex].Longitude,
			shape[closestSegmentIndex+1].Latitude, shape[closestSegmentIndex+1].Longitude,
		)
	}

	return interpolateDistance(cumulativeDistances, segmentLength, closestSegmentIndex, projectionRatio)
}

func getDistanceAlongShapeInRange(lat, lon float64, shape []gtfs.ShapePoint, minDistTraveled, maxDistTraveled float64) float64 {
	if len(shape) < 2 {
		return 0
	}

	cumulativeDistances := preCalculateCumulativeDistances(shape)
	useRange := maxDistTraveled > minDistTraveled

	var minDistance = math.Inf(1)
	var closestSegmentIndex int
	var projectionRatio float64
	var foundInRange = false

	for i := 0; i < len(shape)-1; i++ {
		// check if this segment is within or overlaps the range
		if useRange {
			segmentStart := cumulativeDistances[i]
			segmentEnd := cumulativeDistances[i+1]

			if segmentEnd < minDistTraveled-models.RangeSearchBufferMeters || segmentStart > maxDistTraveled+models.RangeSearchBufferMeters {
				continue
			}
		}

		distance, ratio := distanceToLineSegment(
			lat, lon,
			shape[i].Latitude, shape[i].Longitude,
			shape[i+1].Latitude, shape[i+1].Longitude,
		)

		if distance < minDistance {
			minDistance = distance
			closestSegmentIndex = i
			projectionRatio = ratio
			foundInRange = true
		}
	}

	// Fallback to full shape search if nothing found in range (GPS drift edge case)
	if useRange && !foundInRange {
		return getDistanceAlongShape(lat, lon, shape)
	}

	var segmentLength float64
	if closestSegmentIndex < len(shape)-1 {
		segmentLength = utils.Distance(
			shape[closestSegmentIndex].Latitude, shape[closestSegmentIndex].Longitude,
			shape[closestSegmentIndex+1].Latitude, shape[closestSegmentIndex+1].Longitude,
		)
	}

	return interpolateDistance(cumulativeDistances, segmentLength, closestSegmentIndex, projectionRatio)
}

// calculateBlockTripSequence calculates the index of a trip within its block's ordered trip sequence
// for trips that are active on the given service date.
// Uses GetTripsByBlockIDOrdered to perform a single SQL JOIN instead of N+1 queries.
func (api *RestAPI) calculateBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) int {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		slog.Warn("calculateBlockTripSequence: failed to get trip",
			slog.String("trip_id", tripID),
			slog.String("error", err.Error()))
		return 0
	}

	if !trip.BlockID.Valid {
		return 0
	}

	formattedDate := serviceDate.Format("20060102")
	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		slog.Warn("calculateBlockTripSequence: failed to get active service IDs",
			slog.String("trip_id", tripID),
			slog.String("date", formattedDate),
			slog.String("error", err.Error()))
		return 0
	}
	if len(activeServiceIDs) == 0 {
		return 0
	}

	orderedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: activeServiceIDs,
	})
	if err != nil {
		slog.Warn("calculateBlockTripSequence: failed to get ordered block trips",
			slog.String("trip_id", tripID),
			slog.String("block_id", trip.BlockID.String),
			slog.String("error", err.Error()))
		return 0
	}

	for i, t := range orderedTrips {
		if t.ID == tripID {
			return i
		}
	}
	return 0
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

	var segmentLength float64
	if closestSegmentIndex < len(shapePoints)-1 {
		segmentLength = utils.Distance(
			shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
			shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
		)
	}
	return interpolateDistance(cumulativeDistances, segmentLength, closestSegmentIndex, projectionRatio)
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

// projectOntoSegment is the shared implementation for projecting a point onto a line segment.
// Returns the distance from point to the closest point on the segment, the projection ratio t ∈ [0,1],
func projectOntoSegment(px, py, x1, y1, x2, y2 float64) (distance, ratio float64, projLat, projLon float64) {
	dx := x2 - x1
	dy := y2 - y1

	if dx == 0 && dy == 0 {
		// Line segment is a point
		return utils.Distance(px, py, x1, y1), 0, x1, y1
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

	return utils.Distance(px, py, closestX, closestY), t, closestX, closestY
}

// distanceToLineSegment returns the distance from a point to the closest point on a line segment
// and the projection ratio t ∈ [0,1].
func distanceToLineSegment(px, py, x1, y1, x2, y2 float64) (distance, ratio float64) {
	d, r, _, _ := projectOntoSegment(px, py, x1, y1, x2, y2)
	return d, r
}

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (api *RestAPI) GetSituationIDsForTrip(ctx context.Context, tripID string) []string {
	var routeID string
	var agencyID string

	if api.GtfsManager.GtfsDB != nil {
		trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
		if err == nil {
			routeID = trip.RouteID
			route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
			if err == nil {
				agencyID = route.AgencyID
			} else if !errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("Failed to fetch route for alerts; degrading to trip+route matching only",
					slog.String("trip_id", tripID),
					slog.String("route_id", routeID),
					slog.Any("error", err),
				)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			api.Logger.Warn("Failed to fetch trip for alerts; degrading to trip matching only",
				slog.String("trip_id", tripID),
				slog.Any("error", err),
			)
		}
	}

	alerts := api.GtfsManager.GetAlertsByIDs(tripID, routeID, agencyID)

	situationIDs := []string{}
	for _, alert := range alerts {
		if alert.ID == "" {
			continue
		}
		if agencyID != "" {
			situationIDs = append(situationIDs, utils.FormCombinedID(agencyID, alert.ID))
		} else {
			situationIDs = append(situationIDs, alert.ID)
		}
	}

	return situationIDs
}

func (api *RestAPI) calculateOffsetForStop(
	stopID string,
	stopTimes []*gtfsdb.StopTime,
	currentTime time.Time,
	serviceDate time.Time,
	scheduleDeviation int,
) int {
	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	for _, st := range stopTimes {
		if st.StopID == stopID {
			stopTimeSeconds := utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime)
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
	serviceDate time.Time,
	scheduleDeviation int,
) (stopID string, offset int) {
	if len(stopTimes) == 0 {
		return "", 0
	}

	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	for i, st := range stopTimes {
		if st.StopID == currentStopID {
			if i+1 < len(stopTimes) {
				nextSt := stopTimes[i+1]
				stopTimeSeconds := utils.EffectiveStopTimeSeconds(nextSt.ArrivalTime, nextSt.DepartureTime)
				predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
				return nextSt.StopID, int(predictedArrival - currentTimeSeconds)
			}
			break
		}
	}

	return "", 0
}

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
				ArrivalTime:         int(utils.NanosToSeconds(stopTime.ArrivalTime)),
				DepartureTime:       int(utils.NanosToSeconds(stopTime.DepartureTime)),
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
				ArrivalTime:         int(utils.NanosToSeconds(stopTime.ArrivalTime)),
				DepartureTime:       int(utils.NanosToSeconds(stopTime.DepartureTime)),
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
			var segmentLength float64
			if closestSegmentIndex < len(shapePoints)-1 {
				segmentLength = utils.Distance(
					shapePoints[closestSegmentIndex].Latitude, shapePoints[closestSegmentIndex].Longitude,
					shapePoints[closestSegmentIndex+1].Latitude, shapePoints[closestSegmentIndex+1].Longitude,
				)
			}
			distanceAlongTrip = interpolateDistance(cumulativeDistances, segmentLength, closestSegmentIndex, projectionRatio)
		}

		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         int(utils.NanosToSeconds(stopTime.ArrivalTime)),
			DepartureTime:       int(utils.NanosToSeconds(stopTime.DepartureTime)),
			StopHeadsign:        utils.NullStringOrEmpty(stopTime.StopHeadsign),
			DistanceAlongTrip:   distanceAlongTrip,
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
}

func (api *RestAPI) findStopsByScheduleDeviation(
	stopTimes []*gtfsdb.StopTime,
	currentTime time.Time,
	serviceDate time.Time,
	scheduleDeviation int,
) (closestStopID string, closestOffset int, nextStopID string, nextOffset int) {
	if len(stopTimes) == 0 {
		return "", 0, "", 0
	}

	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	effectiveScheduleTime := currentTimeSeconds - int64(scheduleDeviation)

	var closestStop *gtfsdb.StopTime
	var closestTimeDiff int64 = math.MaxInt64
	var closestIndex int

	for i, st := range stopTimes {
		stopTime := utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime)

		timeDiff := stopTime - effectiveScheduleTime
		if timeDiff < 0 {
			timeDiff = -timeDiff
		}

		if timeDiff < closestTimeDiff {
			closestTimeDiff = timeDiff
			closestStop = st
			closestIndex = i
		}
	}

	if closestStop == nil {
		return "", 0, "", 0
	}

	closestStopID = closestStop.StopID

	closestStopTime := utils.EffectiveStopTimeSeconds(closestStop.ArrivalTime, closestStop.DepartureTime)
	predictedClosestArrival := closestStopTime + int64(scheduleDeviation)
	closestOffset = int(predictedClosestArrival - currentTimeSeconds)

	if closestIndex+1 < len(stopTimes) {
		nextSt := stopTimes[closestIndex+1]
		nextStopID = nextSt.StopID

		nextStopTime := utils.EffectiveStopTimeSeconds(nextSt.ArrivalTime, nextSt.DepartureTime)
		predictedNextArrival := nextStopTime + int64(scheduleDeviation)
		nextOffset = int(predictedNextArrival - currentTimeSeconds)
	}

	return closestStopID, closestOffset, nextStopID, nextOffset
}

func (api *RestAPI) findClosestStopBySequence(
	stopTimes []*gtfsdb.StopTime,
	currentStopSequence uint32,
	currentTime time.Time,
	serviceDate time.Time,
	scheduleDeviation int,
) (stopID string, offset int) {
	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	for _, st := range stopTimes {
		if uint32(st.StopSequence) == currentStopSequence {
			stopTimeSeconds := utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime)
			predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
			return st.StopID, int(predictedArrival - currentTimeSeconds)
		}
	}

	return "", 0
}

func (api *RestAPI) findNextStopBySequence(
	ctx context.Context,
	stopTimes []*gtfsdb.StopTime,
	currentStopSequence uint32,
	currentTime time.Time,
	serviceDate time.Time,
	scheduleDeviation int,
	vehicle *gtfs.Vehicle,
	tripID string,
) (stopID string, offset int) {
	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

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
				stopTimeSeconds := utils.EffectiveStopTimeSeconds(nextStop.ArrivalTime, nextStop.DepartureTime)
				predictedArrival := stopTimeSeconds + int64(scheduleDeviation)
				return nextStop.StopID, int(predictedArrival - currentTimeSeconds)
			}
		}
	}

	return "", 0
}

func (api *RestAPI) getFirstStopOfNextTripInBlock(ctx context.Context, currentTripID string, serviceDate time.Time) *gtfsdb.StopTime {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, currentTripID)
	if err != nil {
		slog.Warn("getFirstStopOfNextTripInBlock: failed to get trip",
			slog.String("trip_id", currentTripID),
			slog.String("error", err.Error()))
		return nil
	}
	if !trip.BlockID.Valid {
		return nil
	}

	orderedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		slog.Warn("getFirstStopOfNextTripInBlock: failed to get ordered block trips",
			slog.String("trip_id", currentTripID),
			slog.String("block_id", trip.BlockID.String),
			slog.String("error", err.Error()))
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
		if err != nil {
			slog.Warn("getFirstStopOfNextTripInBlock: failed to get stop times for next trip",
				slog.String("next_trip_id", nextTripID),
				slog.String("error", err.Error()))
			return nil
		}
		if len(nextTripStopTimes) > 0 {
			return &nextTripStopTimes[0]
		}
	}

	return nil
}

func (api *RestAPI) calculateEffectiveDistanceAlongTrip(
	ctx context.Context,
	actualDistance float64,
	scheduleDeviation int,
	currentTime time.Time,
	serviceDate time.Time,
	stopTimes []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
) float64 {
	if scheduleDeviation == 0 || len(stopTimes) == 0 {
		return actualDistance
	}

	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}
	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return actualDistance
	}
	stopByID := make(map[string]gtfsdb.Stop, len(stops))
	for _, s := range stops {
		stopByID[s.ID] = s
	}

	stopDistances := make([]float64, len(stopTimes))
	for i, st := range stopTimes {
		stop, ok := stopByID[st.StopID]
		if !ok {
			return actualDistance
		}
		stopDistances[i] = api.calculatePreciseDistanceAlongTripWithCoords(
			stop.Lat, stop.Lon, shapePoints, cumulativeDistances,
		)
	}

	currentTimeSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)
	effectiveScheduleTime := currentTimeSeconds - int64(scheduleDeviation)

	return interpolateDistanceAtScheduledTime(effectiveScheduleTime, stopTimes, stopDistances)
}

func interpolateDistanceAtScheduledTime(
	scheduledTime int64,
	stopTimes []gtfsdb.StopTime,
	cumulativeDistances []float64,
) float64 {
	if len(stopTimes) == 0 || len(cumulativeDistances) != len(stopTimes) {
		return 0
	}

	for i := 0; i < len(stopTimes)-1; i++ {
		fromStop := stopTimes[i]
		toStop := stopTimes[i+1]

		fromTime := utils.NanosToSeconds(fromStop.DepartureTime)
		toTime := utils.NanosToSeconds(toStop.ArrivalTime)

		if scheduledTime >= fromTime && scheduledTime <= toTime {
			if toTime == fromTime {
				return cumulativeDistances[i]
			}

			timeRatio := float64(scheduledTime-fromTime) / float64(toTime-fromTime)

			return cumulativeDistances[i] + timeRatio*(cumulativeDistances[i+1]-cumulativeDistances[i])
		}
	}

	if scheduledTime < utils.NanosToSeconds(stopTimes[0].ArrivalTime) {
		return 0
	}

	return cumulativeDistances[len(cumulativeDistances)-1]
}

func interpolateDistance(cumulativeDistances []float64, segmentLength float64, index int, projectionRatio float64) float64 {
	cumulativeDistance := cumulativeDistances[index]
	if index < len(cumulativeDistances)-1 {
		cumulativeDistance += segmentLength * projectionRatio
	}
	return cumulativeDistance
}
