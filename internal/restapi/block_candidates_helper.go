package restapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

// Match Java OBA: look back 30 min (catch late vehicles) and ahead 10 min (catch early vehicles).
// TODO: We should add config for runningLateWindow and runningEarlyWindow like Java OBA
// source:https://groups.google.com/g/onebusaway-developers/c/j-G-1UyfbXI/m/J-Su3BArKW0J
const (
	runningLate  = 30 * time.Minute // runningLateWindow
	runningEarly = 10 * time.Minute // runningEarlyWindow
)

// serviceDayWindow is one service day's active service IDs together with the
// query window, expressed as durations since that service day's midnight.
type serviceDayWindow struct {
	serviceIDs    []string
	sinceMidnight time.Duration
	rangeStart    time.Duration
	rangeEnd      time.Duration
	// date is midnight of this service day, in the query's location.
	date time.Time
}

// serviceDayWindows returns the query window for the current service day and,
// when present, the previous service day. GTFS allows departure times past
// 24:00:00 (e.g., 25:30:00 = 1:30 AM next day), so trips belonging to
// yesterday's service can still be running now. The current-day window is
// always windows[0]; a previous-day window, when present, is windows[1].
func (api *RestAPI) serviceDayWindows(ctx context.Context, currentTime time.Time) ([]serviceDayWindow, error) {
	formattedDate := currentTime.Format("20060102")
	serviceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
	if err != nil {
		return nil, err
	}

	serviceDayMidnight := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, currentTime.Location())
	currentSinceMidnight := max(currentTime.Sub(serviceDayMidnight), 0)

	windows := []serviceDayWindow{{
		serviceIDs:    serviceIDs,
		sinceMidnight: currentSinceMidnight,
		rangeStart:    currentSinceMidnight - runningLate,
		rangeEnd:      currentSinceMidnight + runningEarly,
		date:          serviceDayMidnight,
	}}

	prevDay := currentTime.AddDate(0, 0, -1)
	prevFormattedDate := prevDay.Format("20060102")
	prevServiceIDs, err := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, prevFormattedDate)
	if err != nil {
		api.Logger.Warn("failed to fetch previous-day service IDs", "date", prevFormattedDate, "error", err)
		return windows, nil
	}
	if len(prevServiceIDs) == 0 {
		return windows, nil
	}

	// GTFS allows stop_times past 24:00:00 (e.g. 25:30:00 = 1:30 AM next day),
	// so a trip belonging to yesterday's service can still be running now.
	// Expressing "now" as yesterday's-midnight-plus-24h-and-change lets the
	// same query-window math used for today apply unchanged to yesterday's
	// service day.
	prevDaySinceMidnight := currentSinceMidnight + 24*time.Hour
	windows = append(windows, serviceDayWindow{
		serviceIDs:    prevServiceIDs,
		sinceMidnight: prevDaySinceMidnight,
		rangeStart:    prevDaySinceMidnight - runningLate,
		rangeEnd:      prevDaySinceMidnight + runningEarly,
		date:          serviceDayMidnight.AddDate(0, 0, -1),
	})

	return windows, nil
}

// blockCandidateQueries supplies the scope-specific (route or stop) lookups
// that blockCandidates needs. Everything else about candidate selection —
// the service-day window loop, the layover lookup, the null-block loop, and
// the error-handling semantics — is scope-agnostic and lives in
// blockCandidates itself.
type blockCandidateQueries struct {
	// logScope names the caller in warning logs, e.g. "trips-for-route".
	logScope   string
	indexIDs   func(ctx context.Context, serviceIDs []string) ([]int64, error)
	layover    func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error)
	nullBlocks func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error)
}

// blockCandidates returns the candidate block IDs, and the null-block trip
// IDs mapped to the index (into windows) of the service day whose query
// found them, whose schedule overlaps the query windows, as scoped by q.
func (api *RestAPI) blockCandidates(ctx context.Context, q blockCandidateQueries, windows []serviceDayWindow) (map[string]bool, map[string]int, error) {
	allLinkedBlocks, err := api.resolveLinkedBlocks(ctx, q, windows)
	if err != nil {
		return nil, nil, err
	}

	api.addLayoverBlocks(ctx, q, windows[0], allLinkedBlocks)

	nullBlockTripWindows := api.resolveNullBlockTripWindows(ctx, q, windows)

	return allLinkedBlocks, nullBlockTripWindows, nil
}

// resolveLinkedBlocks finds the block IDs whose block_trip_index overlaps
// the query windows. An error from the current-day (windows[0]) query is
// returned to the caller as fatal; a previous-day failure is logged and
// skipped so partial data still produces a result.
func (api *RestAPI) resolveLinkedBlocks(ctx context.Context, q blockCandidateQueries, windows []serviceDayWindow) (map[string]bool, error) {
	allLinkedBlocks := make(map[string]bool)

	for i, w := range windows {
		indexIDs, err := q.indexIDs(ctx, w.serviceIDs)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			api.Logger.Warn(q.logScope+": failed to fetch previous-day block index IDs", "error", err)
			continue
		}
		if len(indexIDs) == 0 {
			continue
		}

		if err := api.addBlocksForIndexIDs(ctx, indexIDs, w, allLinkedBlocks); err != nil {
			if i == 0 {
				return nil, err
			}
			api.Logger.Warn(q.logScope+": failed to fetch previous-day blocks", "error", err)
		}
	}

	return allLinkedBlocks, nil
}

// addBlocksForIndexIDs fetches the blocks active within w for the given
// block_trip_index IDs and adds them to allLinkedBlocks.
func (api *RestAPI) addBlocksForIndexIDs(ctx context.Context, indexIDs []int64, w serviceDayWindow, allLinkedBlocks map[string]bool) error {
	blocks, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
		FromTime:   sql.NullInt64{Int64: w.rangeStart.Nanoseconds(), Valid: true},
		ToTime:     sql.NullInt64{Int64: w.rangeEnd.Nanoseconds(), Valid: true},
		IndexIds:   indexIDs,
		ServiceIds: w.serviceIDs,
	})
	if err != nil {
		return err
	}
	for _, b := range blocks {
		if b.Valid {
			allLinkedBlocks[b.String] = true
		}
	}
	return nil
}

// addLayoverBlocks adds block IDs whose layover overlaps w to allLinkedBlocks.
// A failure here is logged and skipped, not fatal.
func (api *RestAPI) addLayoverBlocks(ctx context.Context, q blockCandidateQueries, w serviceDayWindow, allLinkedBlocks map[string]bool) {
	layoverBlocks, err := q.layover(ctx, w.serviceIDs, w.rangeStart.Nanoseconds(), w.rangeEnd.Nanoseconds())
	if err != nil {
		api.Logger.Warn(q.logScope+": failed to fetch layover blocks", "error", err)
		return
	}
	for _, blockID := range layoverBlocks {
		allLinkedBlocks[blockID] = true
	}
}

// resolveNullBlockTripWindows finds null-block trips active within each
// window, mapped to the index of the window that found them. A failure on
// any window is logged and skipped, not fatal.
func (api *RestAPI) resolveNullBlockTripWindows(ctx context.Context, q blockCandidateQueries, windows []serviceDayWindow) map[string]int {
	nullBlockTripWindows := make(map[string]int)

	for i, w := range windows {
		trips, err := q.nullBlocks(ctx, w.serviceIDs, w.rangeStart.Nanoseconds(), w.rangeEnd.Nanoseconds())
		if err != nil {
			label := q.logScope + ": failed to fetch null-block trips"
			if i > 0 {
				label = q.logScope + ": failed to fetch previous-day null-block trips"
			}
			api.Logger.Warn(label, "error", err)
			continue
		}
		for _, tripID := range trips {
			nullBlockTripWindows[tripID] = i
		}
	}

	return nullBlockTripWindows
}

// blockCandidatesForRoute returns the candidate block IDs and null-block
// trip IDs (see blockCandidates) whose schedule overlaps the query windows,
// scoped to the given route.
func (api *RestAPI) blockCandidatesForRoute(ctx context.Context, routeID string, windows []serviceDayWindow) (map[string]bool, map[string]int, error) {
	return api.blockCandidates(ctx, blockCandidateQueries{
		logScope: "trips-for-route",
		indexIDs: func(ctx context.Context, serviceIDs []string) ([]int64, error) {
			return api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
				RouteID:    routeID,
				ServiceIds: serviceIDs,
			})
		},
		layover: func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error) {
			return api.GtfsManager.GtfsDB.Queries.GetActiveLayoverBlockIDsForRoute(ctx, gtfsdb.GetActiveLayoverBlockIDsForRouteParams{
				RouteID:        routeID,
				ServiceIds:     serviceIDs,
				TimeRangeStart: rangeStart,
				TimeRangeEnd:   rangeEnd,
			})
		},
		nullBlocks: func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error) {
			return api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
				RouteID:        routeID,
				ServiceIds:     serviceIDs,
				TimeRangeStart: sql.NullInt64{Int64: rangeStart, Valid: true},
				TimeRangeEnd:   sql.NullInt64{Int64: rangeEnd, Valid: true},
			})
		},
	}, windows)
}

// blockCandidatesForStops is the stop-scoped mirror of blockCandidatesForRoute:
// it returns the candidate block IDs and null-block trip IDs (see
// blockCandidates) whose schedule overlaps the query windows, scoped to
// trips serving any of the given stops.
func (api *RestAPI) blockCandidatesForStops(ctx context.Context, stopIDs []string, windows []serviceDayWindow) (map[string]bool, map[string]int, error) {
	return api.blockCandidates(ctx, blockCandidateQueries{
		logScope: "trips-for-location",
		indexIDs: func(ctx context.Context, serviceIDs []string) ([]int64, error) {
			return api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForStops(ctx, gtfsdb.GetBlockTripIndexIDsForStopsParams{
				StopIds:    stopIDs,
				ServiceIds: serviceIDs,
			})
		},
		layover: func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error) {
			return api.GtfsManager.GtfsDB.Queries.GetActiveLayoverBlockIDsForStops(ctx, gtfsdb.GetActiveLayoverBlockIDsForStopsParams{
				StopIds:        stopIDs,
				ServiceIds:     serviceIDs,
				TimeRangeStart: rangeStart,
				TimeRangeEnd:   rangeEnd,
			})
		},
		nullBlocks: func(ctx context.Context, serviceIDs []string, rangeStart, rangeEnd int64) ([]string, error) {
			return api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForStops(ctx, gtfsdb.GetActiveTripsWithNullBlockForStopsParams{
				StopIds:        stopIDs,
				ServiceIds:     serviceIDs,
				TimeRangeStart: sql.NullInt64{Int64: rangeStart, Valid: true},
				TimeRangeEnd:   sql.NullInt64{Int64: rangeEnd, Valid: true},
			})
		},
	}, windows)
}

// resolveActiveTrips returns the trip IDs actually running at each window's
// query time: one active trip per candidate block (the earliest trip in the
// block whose stop times contain that window's query time), plus the given
// null-block trip IDs. It also returns, for every returned trip ID, the
// calendar date (midnight) of the service day whose window produced it —
// windows[0]'s date for an ordinary match, windows[1]'s (yesterday's) date
// for a trip only found via the past-midnight lookback — so callers doing
// their own schedule-time math (e.g. extrapolating a position) use the
// correct service day rather than assuming "today". If ctx is canceled
// mid-loop, the trips accumulated so far are returned; callers should check
// ctx.Err() afterward.
func (api *RestAPI) resolveActiveTrips(ctx context.Context, blockIDs map[string]bool, nullBlockTripWindows map[string]int, windows []serviceDayWindow) ([]string, map[string]time.Time) {
	var activeTrips []string
	serviceDateByTrip := make(map[string]time.Time, len(blockIDs)+len(nullBlockTripWindows))

	for blockID := range blockIDs {
		if ctx.Err() != nil {
			return activeTrips, serviceDateByTrip
		}

		if tripID, serviceDate, ok := api.resolveActiveTripForBlock(ctx, blockID, windows); ok {
			activeTrips = append(activeTrips, tripID)
			serviceDateByTrip[tripID] = serviceDate
		}
	}

	for tripID, windowIdx := range nullBlockTripWindows {
		activeTrips = append(activeTrips, tripID)
		if windowIdx >= 0 && windowIdx < len(windows) {
			serviceDateByTrip[tripID] = windows[windowIdx].date
		}
	}

	return activeTrips, serviceDateByTrip
}

// resolveActiveTripForBlock returns the trip in blockID that is running at
// the query time of the earliest window that has one, trying windows in
// order (current day, then the past-midnight lookback).
func (api *RestAPI) resolveActiveTripForBlock(ctx context.Context, blockID string, windows []serviceDayWindow) (tripID string, serviceDate time.Time, ok bool) {
	blockIDNullStr := nulls.String(blockID)

	for _, w := range windows {
		// GetActiveTripInBlockAtTime already returns sql.ErrNoRows when the
		// block has no trip for this service day, so there's no need to
		// pre-check with a separate "does this block have any trips" query.
		activeTrip, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripInBlockAtTime(ctx, gtfsdb.GetActiveTripInBlockAtTimeParams{
			BlockID:     blockIDNullStr,
			ServiceIds:  w.serviceIDs,
			CurrentTime: sql.NullInt64{Int64: w.sinceMidnight.Nanoseconds(), Valid: true},
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			api.Logger.Warn("failed to get active trip in block", "block_id", blockID, "error", err)
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			// No trip in this block is currently running at the requested time.
			// Java OBA only returns blocks with a currently-running trip (see
			// BlockStatusServiceImpl.computeLocations which adds scheduled locations
			// only when isInService()). Skip rather than picking a "best candidate"
			// upcoming/past trip that isn't actually running.
			continue
		}

		return activeTrip, w.date, true
	}

	return "", time.Time{}, false
}
