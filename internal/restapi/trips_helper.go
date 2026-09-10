package restapi

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripStatusExtras carries what BuildTripStatus resolved on the way to the
// status, so a caller needing the same values does not query for them again.
//
// snapshot may be nil (no live vehicle, no in-range block, etc.); callers
// should handle that case.
type tripStatusExtras struct {
	snapshot   *scheduledBlockSnapshot
	situations []situationRef
}

// BuildTripStatus builds a TripStatus for the given trip.
//
// Pass a non-nil vehicle when it is already known (e.g. DUPLICATED trips, or when
// the caller has already looked up the vehicle). Pass nil to have the function look
// up the vehicle automatically via GetVehicleForTrip.
//
// tripID is used for DB lookups (stop times, shapes, block sequence). For DUPLICATED
// trips whose synthetic ActiveTripID has no DB entry, set tripID to the base/static
// trip ID so the correct schedule data is used.
//
// It also returns the intermediate results it built along the way. Callers
// needing per-stop block metrics (distanceFromStop, numberOfStopsAway) or the
// trip's situations should reuse those instead of resolving them a second time —
// the amplification matters for the plural arrivals-and-departures endpoint
// which is called per-arrival-row across wide time windows.
//
// freqMap supplies batch-fetched frequency rows, avoiding a per-call query.
func (api *RestAPI) BuildTripStatus(
	ctx context.Context,
	agencyID, tripID string,
	vehicle *gtfs.Vehicle,
	serviceDate time.Time,
	currentTime time.Time,
	freqMap map[string][]gtfsdb.Frequency,
) (*models.TripStatus, *tripStatusExtras, error) {
	if vehicle == nil {
		vehicle = api.GtfsManager.GetVehicleForTrip(ctx, tripID)
	}
	// Normalize serviceDate to midnight for the response, consistent across all endpoints.
	sdMidnight := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(),
		0, 0, 0, 0, serviceDate.Location())
	status := models.NewTripStatus()
	status.ActiveTripID = utils.FormCombinedID(agencyID, tripID)
	status.ServiceDate = models.NewModelTime(sdMidnight)
	extras := &tripStatusExtras{situations: api.situationRefsForTrip(ctx, tripID)}
	status.SituationIDs = situationIDsFromRefs(extras.situations)
	// OccupancyCapacity and OccupancyCount default to 0 when no data is available.

	// Computed up front (independent of vehicle/stop-time/shape data below) so
	// it is still set even when the CANCELED early-return below skips the rest
	// of this function.
	blockTripSequence := api.calculateBlockTripSequence(ctx, tripID, serviceDate)
	if blockTripSequence > 0 {
		status.BlockTripSequence = blockTripSequence
	}

	if vehicle != nil {
		if vehicle.ID != nil {
			// The OBA spec requires the combined {agencyId}_{vehicleId} form
			// here — see the vehicleId field on
			// arrival-and-departure-for-stop, arrivals-and-departures-for-stop,
			// and trip-details. Downstream consumers (trip_details_handler,
			// trip_for_vehicle_handler) parse it back with
			// utils.ExtractAgencyIDAndCodeID and 404 on the un-prefixed form.
			status.VehicleID = utils.FormCombinedID(agencyID, vehicle.ID.ID)
		}
		if vehicle.OccupancyStatus != nil {
			status.OccupancyStatus = vehicle.OccupancyStatus.String()
		}
		// NOTE: GTFS-RT OccupancyPercentage (0-100%) has no direct equivalent in the
		// OBA TripStatus schema. The Java OBA server populates occupancyCapacity from
		// agency-provided vehicle capacity data, not from GTFS-RT percentages.
		// We intentionally leave OccupancyCapacity at its zero value (0) here, as GTFS-RT OccupancyPercentage has no direct mapping to OBA's capacity-based model.
		// See: TripStatusBeanServiceImpl.java in onebusaway-transit-data-federation.
	}
	api.BuildVehicleStatus(ctx, vehicle, tripID, agencyID, status, currentTime)

	// Frequencies are reported even for CANCELED trips, so resolve the DB
	// trip ID before the early-return.
	_, activeTripRawID, err := utils.ExtractAgencyIDAndCodeID(status.ActiveTripID)
	if err != nil {
		return status, extras, err
	}

	// Determine which trip ID to use for DB lookups (stop times, shapes, etc.).
	// Usually activeTripRawID (which may differ from tripID when a vehicle is
	// reassigned to a different trip in the same block). For DUPLICATED trips,
	// activeTripRawID may be a synthetic ID not in the DB, so fall back to tripID.
	dbTripID := activeTripRawID
	if activeTripRawID != tripID {
		if _, lookupErr := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, activeTripRawID); lookupErr != nil {
			if !errors.Is(lookupErr, sql.ErrNoRows) {
				slog.Warn("BuildTripStatus: failed to resolve active trip ID, falling back to trip ID",
					slog.String("active_trip_id", activeTripRawID),
					slog.String("trip_id", tripID),
					slog.String("error", lookupErr.Error()))
			}
			dbTripID = tripID
		}
	}

	// Reuse the batch-fetched frequencies when supplied; single-entry handlers
	// fall back to a per-trip query.
	frequency, freqErr := api.frequencyForEntry(ctx, freqMap, dbTripID, serviceDate, currentTime)
	if freqErr != nil {
		return status, extras, freqErr
	}
	status.Frequency = frequency

	// CANCELED trips are no longer running there is no active position or schedule
	// to report. Return immediately with the cancellation status and skip all stop-time
	// and shape calculations, which are meaningless for a trip that is not operating.
	// Predicted is true because the cancellation itself is real-time information.
	if status.Status == "CANCELED" {
		status.Predicted = vehicle != nil && !defaultStaleDetector.Check(vehicle, currentTime)
		status.Scheduled = !status.Predicted
		return status, extras, nil
	}

	// Mirror Java's applyTripUpdatesToRecord: iterate all block trips in
	// start-time order; last trip-level delay wins (Java overwrites
	// best.scheduleDeviation unconditionally each time a trip-level delay is
	// seen). This is what makes the published `scheduleDeviation` match Java's.
	//
	// blockShiftTripIDsSortedByStartTime applies the same
	// keepShiftContainingTrip split that the snapshot path uses. Without it,
	// feeds that reuse one block_id across an entire day (many physical
	// buses under one block) leak the last shift's delay into the first
	// shift's scheduleDeviation, and status.ScheduleDeviation flows onward
	// into effectiveTime / stop-offset math for every arrival on this trip.
	blockTripIDs := api.blockShiftTripIDsSortedByStartTime(ctx, dbTripID, serviceDate)
	scheduleDeviation, hasRealtimeTripUpdate := api.GetScheduleDeviationForBlock(ctx, blockTripIDs, serviceDate, currentTime)

	if hasRealtimeTripUpdate {
		status.ScheduleDeviation = scheduleDeviation
	}

	hasVehicleRealtimeData := vehicle != nil && !defaultStaleDetector.Check(vehicle, currentTime)
	status.SetPredicted(hasVehicleRealtimeData || hasRealtimeTripUpdate)

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, dbTripID)
	if err != nil {
		slog.Warn("buildTripStatusCore: failed to get stop times",
			slog.String("trip_id", dbTripID),
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
			stopDelays := api.GetStopDelaysFromTripUpdates(dbTripID)
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
		api.fillStopsFromSchedule(ctx, status, dbTripID, currentTime, serviceDate, agencyID, stopTimes)
	}

	shapeRows, shapeErr := api.GtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, dbTripID)
	if shapeErr != nil {
		slog.Warn("buildTripStatusCore: failed to get shape points",
			slog.String("trip_id", dbTripID),
			slog.String("error", shapeErr.Error()))
	}
	// Snap is built from BLOCK trip data (loadBlockTripData + emitBlockStops
	// with per-block-trip haversine fallback), so it does not require the
	// target trip's own shape. Computing it outside the target-shape gate
	// means shapeless feeds still get ActiveTripID, ScheduledDistanceAlong
	// Trip, and — most importantly — the snap that callers reuse for
	// metricsForStop, which drives numberOfStopsAway / distanceFromStop.
	//
	// Apply the same schedule-deviation shift the live-vehicle branch
	// below uses. Trip updates without vehicle positions are common;
	// without this shift the response would self-contradict — publishing
	// scheduleDeviation while placing the bus at its on-schedule position.
	// When hasRealtimeTripUpdate is false, scheduleDeviation is 0 and the
	// shift is a no-op.
	var snap *scheduledBlockSnapshot
	if len(stopTimes) > 0 {
		effectiveTime := currentTime.Add(-time.Duration(scheduleDeviation) * time.Second)
		snap = api.computeScheduledBlockSnapshot(ctx, dbTripID, effectiveTime, serviceDate)
	}

	// Snap-derived status fields — independent of target's shape.
	//
	// Snapshot.InRange guard mirrors Java's null-BlockLocation semantics
	// (ScheduledBlockLocationServiceImpl.java:241-244). When currentTime
	// falls outside the shift's [firstStop, lastStop] range, Java returns
	// null and the arrivals bean leaves tripStatus position fields at
	// their defaults.
	if snap != nil && snap.ActiveTripID != "" && snap.InRange {
		status.ActiveTripID = utils.FormCombinedID(agencyID, snap.ActiveTripID)
		status.ScheduledDistanceAlongTrip = snap.ActiveTripScheduledDistance
		if snap.ActiveTripTotalDistance > 0 {
			status.TotalDistanceAlongTrip = snap.ActiveTripTotalDistance
		}
		// Per the OBA spec, distanceAlongTrip is a distinct field from
		// scheduledDistanceAlongTrip: the former is where the vehicle
		// ACTUALLY is (from live GPS), the latter is where the schedule
		// expects it to be. Java derives it from blockLocation.
		// getDistanceAlongBlock() minus activeBlockTrip.getDistanceAlong
		// Block() -- a live-position value pulled from the RT record.
		//
		// The correct implementation requires block-cumulative shape
		// projection (concat every block-trip's polyline; project vehicle
		// GPS onto the full assembly; subtract active-trip block offset).
		// See summary/distance-along-shape-library-port.md -- that's the
		// tracked followup.
		//
		// Until then, we produce a best-effort value in two tiers:
		//
		// 1) When snap's distance-picked active trip matches the vehicle's
		//    RT-declared trip, snap.ActiveTripShape IS the vehicle's shape
		//    -- projecting the vehicle's GPS onto it gives the true live
		//    position within that trip. This is the correct GPS-derived
		//    value that distinguishes distanceAlongTrip from scheduled
		//    DistanceAlongTrip.
		//
		// 2) When they don't match, projecting onto ActiveTripShape can
		//    produce catastrophic errors (a loop-route vehicle 21km along
		//    the block matches near a trip-start segment on a different
		//    trip's shape, yielding ~11m). Fall back to the schedule
		//    value -- collapses the two fields into one number but is
		//    never wildly wrong. Both are gated on the shift check to
		//    match Java's per-BlockInstance semantics.
		if vehicle != nil && vehicle.Trip != nil && snap.ShiftTripIDs != nil {
			_, sameShift := snap.ShiftTripIDs[vehicle.Trip.ID.ID]
			_, arrivalInShift := snap.ShiftTripIDs[tripID]
			if sameShift && arrivalInShift {
				gpsAssigned := false
				// A stale vehicle's GPS position is stale by definition;
				// treating it as live would mint a distanceAlongTrip from a
				// position the vehicle occupied minutes/hours ago. Gate the
				// GPS projection on freshness so stale (or unavailable) RT
				// data falls through to snap.ActiveTripScheduledDistance --
				// matches the freshness gate on status.Predicted (see the
				// CANCELED branch above) and hasVehicleRealtimeData below.
				if vehicle.Position != nil &&
					vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil &&
					snap.ActiveTripID == vehicle.Trip.ID.ID &&
					!defaultStaleDetector.Check(vehicle, currentTime) {
					gpsPos := models.Location{
						Lat: float64(*vehicle.Position.Latitude),
						Lon: float64(*vehicle.Position.Longitude),
					}
					if dist, ok := projectVehicleDistanceOnShape(
						snap.ActiveTripShape,
						snap.ActiveTripCumulativeDistances,
						gpsPos,
					); ok {
						status.DistanceAlongTrip = dist
						status.LastKnownDistanceAlongTrip = dist
						gpsAssigned = true
					}
				}
				if !gpsAssigned {
					status.DistanceAlongTrip = snap.ActiveTripScheduledDistance
				}
			}
		}
	}

	// Target-shape-dependent work: TotalDistanceAlongTrip default,
	// live-vehicle position projection onto target's shape, orientation
	// inference, and within-target interpolation fallback.
	if shapeErr == nil && len(shapeRows) > 1 {
		shapePoints := shapeRowsToPoints(shapeRows)
		cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
		// Snap's ActiveTripTotalDistance (set above when available) takes
		// precedence; only fall back to target's shape total when snap
		// didn't provide one.
		if status.TotalDistanceAlongTrip == 0 {
			status.TotalDistanceAlongTrip = cumulativeDistances[len(cumulativeDistances)-1]
		}

		if vehicle != nil && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
			if status.LastKnownLocation != nil {
				actualPosition := *status.LastKnownLocation

				if projected := projectPositionWithShapePoints(shapePoints, actualPosition); projected != nil {
					status.Position = *projected
				}

				// If the feed does not provide a bearing, infer orientation from the
				// heading of the closest shape segment at the vehicle's position.
				if vehicle.Position.Bearing == nil {
					if inferred := inferOrientationFromShape(actualPosition.Lat, actualPosition.Lon, shapePoints); inferred >= 0 {
						status.Orientation = inferred
						status.LastKnownOrientation = inferred
					}
				}
			}
		} else if snap != nil && snap.ActiveTripID != "" && snap.InRange {
			// No live vehicle: place the bus at the interpolated position
			// on the block's currently-active trip.
			if pos, orient := positionAndOrientationAtDistance(
				snap.ActiveTripShape,
				snap.ActiveTripCumulativeDistances,
				snap.ActiveTripScheduledDistance,
			); pos != nil {
				status.Position = *pos
				if orient >= 0 {
					status.Orientation = orient
				}
			}
		} else if len(stopTimes) > 0 {
			// currentTime is outside the shift's schedule — fall back to
			// within-target interpolation so position / orientation are not
			// (0, 0). scheduledDistanceAlongTrip stays at its default; Java
			// leaves it unset in this case too.
			api.applyScheduledTripPositionToStatus(
				ctx, status, stopTimes, shapePoints, cumulativeDistances, currentTime, serviceDate,
			)
		}
	}

	extras.snapshot = snap
	return status, extras, nil
}

// frequencyForEntry returns the frequency row whose window contains
// currentTime (earliest when none matches), from freqMap or a per-trip
// fallback query. Empty results yield nil; DB errors propagate.
func (api *RestAPI) frequencyForEntry(ctx context.Context, freqMap map[string][]gtfsdb.Frequency, tripID string, serviceDate, currentTime time.Time) (*models.Frequency, error) {
	freqs, ok := freqMap[tripID]
	if !ok {
		rows, err := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrip(ctx, tripID)
		if err != nil {
			return nil, err
		}
		freqs = rows
		if freqMap != nil {
			freqMap[tripID] = rows
		}
	}
	if len(freqs) == 0 {
		return nil, nil
	}
	converted := models.NewFrequencyFromDB(*selectFrequency(freqs, serviceDate, currentTime), serviceDate)
	return &converted, nil
}

// selectFrequency returns the row whose [start_time, end_time) window
// contains effectiveTime, falling back to freqs[0].
func selectFrequency(freqs []gtfsdb.Frequency, serviceDate, effectiveTime time.Time) *gtfsdb.Frequency {
	midnight := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(),
		0, 0, 0, 0, serviceDate.Location())
	for i := range freqs {
		start := midnight.Add(time.Duration(freqs[i].StartTime))
		end := midnight.Add(time.Duration(freqs[i].EndTime))
		if !effectiveTime.Before(start) && effectiveTime.Before(end) {
			return &freqs[i]
		}
	}
	return &freqs[0]
}

// fetchFrequenciesForTrips batch-fetches frequency rows for tripIDs, batching
// under SQLite's bind-variable limit. Seeds the map only after the query
// succeeds; errors propagate.
func (api *RestAPI) fetchFrequenciesForTrips(ctx context.Context, tripIDs []string) (map[string][]gtfsdb.Frequency, error) {
	freqMap := make(map[string][]gtfsdb.Frequency, len(tripIDs))
	allFreqs, err := queryInBatches(ctx, tripIDs, api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrips)
	if err != nil {
		return nil, err
	}
	for _, tripID := range tripIDs {
		freqMap[tripID] = nil
	}
	for _, f := range allFreqs {
		freqMap[f.TripID] = append(freqMap[f.TripID], f)
	}
	return freqMap, nil
}

// BuildTripSchedule returns the trip's schedule (stop times, block
// neighbors, frequency) resolved around serviceDate in the agency's timezone.
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

	// Populate frequency data for the schedule sub-object.
	var scheduleFrequency *models.Frequency
	freqRows, freqErr := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrip(ctx, trip.ID)
	if freqErr != nil {
		return nil, freqErr
	}
	if len(freqRows) > 0 {
		converted := models.NewFrequencyFromDB(*selectFrequency(freqRows, serviceDate, serviceDate), serviceDate)
		scheduleFrequency = &converted
	}

	return &models.Schedule{
		StopTimes:      stopTimesVals,
		TimeZone:       loc.String(),
		Frequency:      scheduleFrequency,
		NextTripID:     nextTripID,
		PreviousTripID: previousTripID,
	}, nil
}

func (api *RestAPI) GetNextAndPreviousTripIDs(ctx context.Context, trip *gtfsdb.Trip, agencyID string, serviceDate time.Time) (nextTripID string, previousTripID string, stopTimes []gtfsdb.StopTime, err error) {
	if !trip.BlockID.Valid {
		stopTimes, stopErr := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if stopErr != nil {
			return "", "", nil, stopErr
		}
		return "", "", stopTimes, nil
	}

	navResult, err := api.GtfsManager.GtfsDB.Queries.GetNextAndPreviousTripsInBlock(ctx, gtfsdb.GetNextAndPreviousTripsInBlockParams{
		TripID:     trip.ID,
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			// Real DB error (timeout, connection failure, etc.) — propagate it.
			return "", "", nil, err
		}
		// ErrNoRows: trip not in block for this service date — no prev/next, but
		// still fetch stop times so callers get the schedule for the trip itself.
		stopTimes, stopErr := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if stopErr != nil {
			return "", "", nil, stopErr
		}
		return "", "", stopTimes, nil
	}

	// LAG/LEAD return interface{} (nullable); type-switch to handle both string and []byte
	switch prev := navResult.PrevTripID.(type) {
	case string:
		if prev != "" {
			previousTripID = utils.FormCombinedID(agencyID, prev)
		}
	case []byte:
		if len(prev) > 0 {
			previousTripID = utils.FormCombinedID(agencyID, string(prev))
		}
	case nil:
		// Expected for the first trip in the block.
	default:
		slog.Warn("GetNextAndPreviousTripIDs: unexpected type for prev_trip_id",
			slog.String("type", fmt.Sprintf("%T", prev)),
			slog.String("trip_id", trip.ID))
	}

	switch next := navResult.NextTripID.(type) {
	case string:
		if next != "" {
			nextTripID = utils.FormCombinedID(agencyID, next)
		}
	case []byte:
		if len(next) > 0 {
			nextTripID = utils.FormCombinedID(agencyID, string(next))
		}
	case nil:
		// Expected for the last trip in the block.
	default:
		slog.Warn("GetNextAndPreviousTripIDs: unexpected type for next_trip_id",
			slog.String("type", fmt.Sprintf("%T", next)),
			slog.String("trip_id", trip.ID))
	}

	stopTimes, err = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	if err != nil {
		return nextTripID, previousTripID, nil, err
	}

	return nextTripID, previousTripID, stopTimes, nil
}

func (api *RestAPI) fillStopsFromSchedule(ctx context.Context, status *models.TripStatus, tripID string, currentTime time.Time, serviceDate time.Time, agencyID string, preloaded []gtfsdb.StopTime) {
	var stopTimes []gtfsdb.StopTime
	if len(preloaded) > 0 {
		stopTimes = preloaded
	} else {
		var err error
		stopTimes, err = api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err != nil {
			slog.Warn("fillStopsFromSchedule: failed to get stop times",
				slog.String("trip_id", tripID),
				slog.String("error", err.Error()))
			return
		}
	}
	if len(stopTimes) == 0 {
		return
	}

	currentSeconds := utils.CalculateSecondsSinceServiceDate(currentTime, serviceDate)

	// Dereference schedule deviation safely, default to 0 if not set
	schedDev := int64(status.ScheduleDeviation)

	for i, st := range stopTimes {
		arrivalTime := utils.EffectiveStopTimeSeconds(st.ArrivalTime, st.DepartureTime)
		predictedArrival := arrivalTime + schedDev

		if predictedArrival > currentSeconds {
			if i > 0 && status.ClosestStop == "" {
				status.ClosestStop = utils.FormCombinedID(agencyID, stopTimes[i-1].StopID)
				closestArrival := utils.EffectiveStopTimeSeconds(stopTimes[i-1].ArrivalTime, stopTimes[i-1].DepartureTime)
				status.ClosestStopTimeOffset = int(closestArrival + schedDev - currentSeconds)
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
		status.ClosestStopTimeOffset = int(arrivalTime + schedDev - currentSeconds)
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
// Returns 0 when the sequence is unavailable, for callers that treat 0 as "no data".
func (api *RestAPI) calculateBlockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) int {
	seq, ok := api.blockTripSequence(ctx, tripID, serviceDate)
	if !ok {
		return 0
	}
	return seq
}

// blockTripSequence returns the zero-based index of a trip within its block's
// ordered sequence for the given service date, and whether it was resolved.
// Uses GetBlockTripSequence with ROW_NUMBER() window function instead of fetching all trips and looping.
func (api *RestAPI) blockTripSequence(ctx context.Context, tripID string, serviceDate time.Time) (int, bool) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("blockTripSequence: failed to get trip",
				slog.String("trip_id", tripID),
				slog.String("error", err.Error()))
		}
		return 0, false
	}

	if !trip.BlockID.Valid {
		return 0, false
	}

	formattedDate := serviceDate.Format("20060102")
	activeServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		slog.Warn("blockTripSequence: failed to get active service IDs",
			slog.String("trip_id", tripID),
			slog.String("date", formattedDate),
			slog.String("error", err.Error()))
		return 0, false
	}
	if len(activeServiceIDs) == 0 {
		return 0, false
	}

	// Use optimized query with ROW_NUMBER() window function
	seq, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripSequence(ctx, gtfsdb.GetBlockTripSequenceParams{
		TripID:     tripID,
		BlockID:    trip.BlockID,
		ServiceIds: activeServiceIDs,
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("blockTripSequence: failed to get block trip sequence",
				slog.String("trip_id", tripID),
				slog.String("block_id", trip.BlockID.String),
				slog.String("error", err.Error()))
		}
		return 0, false
	}

	return int(seq), true
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

// situationRefsForTrip resolves the alerts affecting a trip and pairs each with
// the situation ID that refers to it, so a caller needing both the entry IDs and
// the situation references gets them from one lookup.
func (api *RestAPI) situationRefsForTrip(ctx context.Context, tripID string) []situationRef {
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

	return situationRefsFromAlerts(api.GtfsManager.GetAlertsByIDs(tripID, routeID, agencyID), agencyID)
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

// calculateBatchStopDistances builds a per-stop models.StopTime slice for the
// trip response, filling DistanceAlongTrip via projectStopsInSequence — the
// same sequence-aware projection the block-level path uses. Sharing that
// projection keeps distanceAlongTrip in trip-details stopTimes consistent
// with distanceFromStop in arrivals responses (they used to drift because
// each path had its own copy of the projection loop, and only one of the
// copies had the loop-route cursor advance).
func (api *RestAPI) calculateBatchStopDistances(
	timeStops []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	stopCoords map[string]struct{ lat, lon float64 },
	agencyID string,
) []models.StopTime {
	stopTimesList := make([]models.StopTime, 0, len(timeStops))

	// Pre-compute the shape's cumulative distances once so projectStopsInSequence
	// can operate on them without re-walking the polyline. When the shape is
	// missing / too short, projectStopsInSequence returns zeros — the response
	// still emits every stop with distanceAlongTrip=0 (same as before).
	var cumulativeDistances []float64
	var distances []float64
	if len(shapePoints) >= 2 {
		cumulativeDistances = preCalculateCumulativeDistances(shapePoints)
	}
	if len(cumulativeDistances) == len(shapePoints) {
		stopByID := stopByIDFromCoords(timeStops, stopCoords)
		distances = projectStopsInSequence(timeStops, stopByID, shapePoints, cumulativeDistances)
	} else {
		distances = make([]float64, len(timeStops))
	}

	for i, stopTime := range timeStops {
		stopTimesList = append(stopTimesList, models.StopTime{
			StopID:              utils.FormCombinedID(agencyID, stopTime.StopID),
			ArrivalTime:         models.NewModelDuration(time.Duration(stopTime.ArrivalTime)),
			DepartureTime:       models.NewModelDuration(time.Duration(stopTime.DepartureTime)),
			StopHeadsign:        nulls.StringOrEmpty(stopTime.StopHeadsign),
			DistanceAlongTrip:   distances[i],
			HistoricalOccupancy: "",
		})
	}
	return stopTimesList
}

// stopByIDFromCoords bridges calculateBatchStopDistances's stopCoords map
// (its callers pass an untyped {lat, lon} pair) to the gtfsdb.Stop shape
// projectStopsInSequence expects. Only the stops referenced in timeStops
// are added, and any stop whose coords are missing is left out — matching
// projectStopsInSequence's own "unknown stop → distance 0" branch.
func stopByIDFromCoords(
	timeStops []gtfsdb.StopTime,
	stopCoords map[string]struct{ lat, lon float64 },
) map[string]gtfsdb.Stop {
	out := make(map[string]gtfsdb.Stop, len(stopCoords))
	for _, st := range timeStops {
		if _, done := out[st.StopID]; done {
			continue
		}
		coords, ok := stopCoords[st.StopID]
		if !ok {
			continue
		}
		out[st.StopID] = gtfsdb.Stop{ID: st.StopID, Lat: coords.lat, Lon: coords.lon}
	}
	return out
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
					nextStop = api.getFirstStopOfNextTripInBlock(ctx, tripID)
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

// getFirstStopOfNextTripInBlock uses LEAD() window function to find the next trip
// in the block and directly fetches its first stop in a single SQL query.
func (api *RestAPI) getFirstStopOfNextTripInBlock(ctx context.Context, currentTripID string) *gtfsdb.StopTime {
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

	// Use optimized query with LEAD() window function
	stopTime, err := api.GtfsManager.GtfsDB.Queries.GetFirstStopOfNextTripInBlock(ctx, gtfsdb.GetFirstStopOfNextTripInBlockParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
		TripID:     currentTripID,
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("getFirstStopOfNextTripInBlock: query failed",
				slog.String("trip_id", currentTripID),
				slog.String("block_id", trip.BlockID.String),
				slog.String("error", err.Error()))
		}
		return nil
	}

	return &stopTime
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

// inferOrientationFromShape computes the OBA orientation (degrees, 0=East, 90=North)
// for a vehicle at (lat, lon) by finding the closest shape segment and returning its heading.
// Returns -1 if the shape has fewer than 2 points.
func inferOrientationFromShape(lat, lon float64, shape []gtfs.ShapePoint) float64 {
	if len(shape) < 2 {
		return -1
	}

	var minDist = math.Inf(1)
	bestIdx := 0

	for i := 0; i < len(shape)-1; i++ {
		d, _ := distanceToLineSegment(lat, lon,
			shape[i].Latitude, shape[i].Longitude,
			shape[i+1].Latitude, shape[i+1].Longitude,
		)
		if d < minDist {
			minDist = d
			bestIdx = i
		}
	}

	return segmentOrientation(shape[bestIdx], shape[bestIdx+1])
}

// segmentOrientation returns the OBA orientation (degrees; 0=East, 90=North) of
// the from→to segment.
func segmentOrientation(from, to gtfs.ShapePoint) float64 {
	dLat := to.Latitude - from.Latitude
	cosLat := math.Cos(from.Latitude * math.Pi / 180)
	dLon := (to.Longitude - from.Longitude) * cosLat
	deg := math.Atan2(dLat, dLon) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg
}

type directionGroup struct {
	GroupID     string
	DirectionID sql.NullInt64
	Trips       []gtfsdb.Trip
}

func groupTripsByDirection(trips []gtfsdb.Trip) []directionGroup {
	byDirID := make(map[int64][]gtfsdb.Trip)
	for _, trip := range trips {
		byDirID[trip.DirectionID.Int64] = append(byDirID[trip.DirectionID.Int64], trip)
	}

	dirIDs := make([]int64, 0, len(byDirID))
	for dirID := range byDirID {
		dirIDs = append(dirIDs, dirID)
	}
	slices.Sort(dirIDs)

	groups := make([]directionGroup, 0, len(dirIDs))
	for _, dirID := range dirIDs {
		tripsInGroup := byDirID[dirID]
		slices.SortFunc(tripsInGroup, func(a, b gtfsdb.Trip) int {
			return cmp.Compare(a.ID, b.ID)
		})
		groups = append(groups, directionGroup{
			GroupID:     strconv.FormatInt(dirID, 10),
			DirectionID: tripsInGroup[0].DirectionID,
			Trips:       tripsInGroup,
		})
	}
	return groups
}

// Match Java OBA: a trip counts as running now if it started up to
// runningLateWindow ago or starts within runningEarlyWindow. Trip selection and
// service-date resolution must use the same window, or a trip can be selected
// and then classified as not running.
//
// Java OBA makes these configurable; maglev hard-codes its defaults, tracked in
// https://github.com/OneBusAway/maglev/issues/800
// source:https://groups.google.com/g/onebusaway-developers/c/j-G-1UyfbXI/m/J-Su3BArKW0J
const (
	runningLate  = 30 * time.Minute // runningLateWindow
	runningEarly = 10 * time.Minute // runningEarlyWindow
)

// serviceDateResolver picks the service date a trip instance belongs to at one
// moment in time. GTFS stop times may run past 24:00:00, so a trip still
// running just after midnight belongs to the previous day's service; reporting
// the query day would put serviceDate and the trip's stop-time offsets a day
// apart.
type serviceDateResolver struct {
	queryDayMidnight    time.Time
	sinceMidnightNs     int64
	queryDayServices    map[string]struct{}
	previousDayServices map[string]struct{}
}

// serviceIDsByDay carries the service IDs active on the query day and the day
// before it, for callers that already loaded them.
type serviceIDsByDay struct {
	QueryDay    []string
	PreviousDay []string
}

// newServiceDateResolverFor builds a resolver from the service IDs the caller
// already fetched for the query day and the day before it.
func newServiceDateResolverFor(queryDayMidnight, currentTime time.Time, services serviceIDsByDay) *serviceDateResolver {
	return &serviceDateResolver{
		queryDayMidnight:    queryDayMidnight,
		sinceMidnightNs:     wallClockSinceMidnightNs(currentTime),
		queryDayServices:    serviceIDSet(services.QueryDay),
		previousDayServices: serviceIDSet(services.PreviousDay),
	}
}

func serviceIDSet(serviceIDs []string) map[string]struct{} {
	if len(serviceIDs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		set[serviceID] = struct{}{}
	}
	return set
}

// serviceIDsForDays fetches the service IDs active on queryDayMidnight and the
// day before it, for a caller that wants both days' raw IDs — e.g. to also run
// a second query with the same service-ID list, the way the block lookup in
// trips-for-location does. A lookup failure is not fatal — that day comes back
// empty.
func (api *RestAPI) serviceIDsForDays(ctx context.Context, queryDayMidnight time.Time) (serviceIDsByDay, error) {
	queryDay, err := api.activeServiceIDsForDate(ctx, queryDayMidnight)
	if err != nil {
		return serviceIDsByDay{}, err
	}
	prevDay, err := api.activeServiceIDsForDate(ctx, queryDayMidnight.AddDate(0, 0, -1))
	if err != nil {
		return serviceIDsByDay{}, err
	}
	return serviceIDsByDay{
		QueryDay:    queryDay,
		PreviousDay: prevDay,
	}, nil
}

func (api *RestAPI) activeServiceIDsForDate(ctx context.Context, day time.Time) ([]string, error) {
	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, day.Format("20060102"))
	if err != nil {
		return nil, err
	}
	return serviceIDs, nil
}

// Resolve returns midnight of the service date trip belongs to.
func (r *serviceDateResolver) Resolve(trip gtfsdb.Trip) time.Time {
	if r.runsOn(r.queryDayServices, trip, r.sinceMidnightNs) {
		return r.queryDayMidnight
	}
	if r.runsOn(r.previousDayServices, trip, r.sinceMidnightNs+int64(24*time.Hour)) {
		return r.queryDayMidnight.AddDate(0, 0, -1)
	}
	return r.queryDayMidnight
}

// runsOn reports whether trip's service is active in services and its scheduled
// span overlaps the running window [sinceMidnightNs-runningLate,
// sinceMidnightNs+runningEarly], measured from that service day's midnight.
//
// Overlap rather than containment: requiring the span to contain sinceMidnightNs
// would classify a just-ended previous-day trip as not running and report it
// against the wrong service date — putting its position and schedule deviation
// a day out. The window matches trips-for-route's own selection window; other
// callers may select trips by a different window and still need this overlap
// check to resolve their service date correctly.
func (r *serviceDateResolver) runsOn(services map[string]struct{}, trip gtfsdb.Trip, sinceMidnightNs int64) bool {
	if _, active := services[trip.ServiceID]; !active {
		return false
	}
	if !trip.MinArrivalTime.Valid || !trip.MaxDepartureTime.Valid {
		return false
	}
	startsBeforeWindowEnds := trip.MinArrivalTime.Int64 <= sinceMidnightNs+int64(runningEarly)
	endsAfterWindowStarts := trip.MaxDepartureTime.Int64 >= sinceMidnightNs-int64(runningLate)
	return startsBeforeWindowEnds && endsAfterWindowStarts
}

// serviceDay pairs the services active on one day with the time-since-midnight
// offset a trip's scheduled span is measured against for that day.
type serviceDay struct {
	serviceIDs      []string
	sinceMidnightNs int64
}

// ServiceDays returns the query day and the day before it. A trip belonging to
// the previous service day is matched against the query moment offset by +24h,
// since GTFS expresses its stop times relative to its own service date.
func (r *serviceDateResolver) ServiceDays() []serviceDay {
	return []serviceDay{
		{serviceIDs: serviceIDSlice(r.queryDayServices), sinceMidnightNs: r.sinceMidnightNs},
		{serviceIDs: serviceIDSlice(r.previousDayServices), sinceMidnightNs: r.sinceMidnightNs + int64(24*time.Hour)},
	}
}

func serviceIDSlice(services map[string]struct{}) []string {
	ids := make([]string, 0, len(services))
	for id := range services {
		ids = append(ids, id)
	}
	return ids
}

// scheduledPositionAtTime interpolates where a trip is along its shape at
// currentTime using its schedule alone. This is the no-real-time-vehicle
// equivalent of a GTFS-RT position; it returns nil when the trip has no usable
// shape or stop times, or when stopsByID is missing any stop the trip serves.
//
// Failing closed on a missing stop matters here specifically: this result
// feeds a bounds-membership test deciding whether a trip is included in the
// response. projectStopsInSequence defaults a missing stop's distance to 0,
// which would place the trip at the start of its shape and risk a false
// positive rather than simply a wrong displayed position.
func (api *RestAPI) scheduledPositionAtTime(
	stopTimes []gtfsdb.StopTime,
	shapePoints []gtfs.ShapePoint,
	stopsByID map[string]gtfsdb.Stop,
	currentTime time.Time,
	serviceDate time.Time,
) *models.Location {
	if len(stopTimes) == 0 || len(shapePoints) < 2 {
		return nil
	}
	for _, st := range stopTimes {
		if _, ok := stopsByID[st.StopID]; !ok {
			return nil
		}
	}

	cumulativeDistances := preCalculateCumulativeDistances(shapePoints)
	position, _, _ := scheduledTripPosition(
		stopTimes, stopsByID, shapePoints, cumulativeDistances, currentTime, serviceDate)
	return position
}
