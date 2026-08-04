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

	// I'm confused by adding 24 hours to get the previous day here, but that's the existing behavior.
	prevDaySinceMidnight := currentSinceMidnight + 24*time.Hour
	windows = append(windows, serviceDayWindow{
		serviceIDs:    prevServiceIDs,
		sinceMidnight: prevDaySinceMidnight,
		rangeStart:    prevDaySinceMidnight - runningLate,
		rangeEnd:      prevDaySinceMidnight + runningEarly,
	})

	return windows, nil
}

// blockCandidatesForRoute returns the candidate block IDs and null-block trip
// IDs whose schedule overlaps the query windows, scoped to the given route.
// An error from the current-day (windows[0]) index/block queries is returned
// to the caller as fatal; all other failures (previous-day queries, layover
// blocks, null-block trips) are logged and skipped so partial data still
// produces a result, matching the pre-extraction handler's behavior.
func (api *RestAPI) blockCandidatesForRoute(ctx context.Context, routeID string, windows []serviceDayWindow) (map[string]bool, []string, error) {
	allLinkedBlocks := make(map[string]bool)

	for i, w := range windows {
		indexIDs, err := api.GtfsManager.GtfsDB.Queries.GetBlockTripIndexIDsForRoute(ctx, gtfsdb.GetBlockTripIndexIDsForRouteParams{
			RouteID:    routeID,
			ServiceIds: w.serviceIDs,
		})
		if err != nil {
			if i == 0 {
				return nil, nil, err
			}
			api.Logger.Warn("trips-for-route: failed to fetch previous-day block index IDs", "error", err)
			continue
		}
		if len(indexIDs) == 0 {
			continue
		}

		blocks, err := api.GtfsManager.GtfsDB.Queries.GetBlocksForBlockTripIndexIDs(ctx, gtfsdb.GetBlocksForBlockTripIndexIDsParams{
			FromTime:   sql.NullInt64{Int64: w.rangeStart.Nanoseconds(), Valid: true},
			ToTime:     sql.NullInt64{Int64: w.rangeEnd.Nanoseconds(), Valid: true},
			IndexIds:   indexIDs,
			ServiceIds: w.serviceIDs,
		})
		if err != nil {
			if i == 0 {
				return nil, nil, err
			}
			api.Logger.Warn("trips-for-route: failed to fetch previous-day blocks", "error", err)
			continue
		}
		for _, b := range blocks {
			if b.Valid {
				allLinkedBlocks[b.String] = true
			}
		}
	}

	layoverBlocks, err := api.GtfsManager.GtfsDB.Queries.GetActiveLayoverBlockIDsForRoute(ctx, gtfsdb.GetActiveLayoverBlockIDsForRouteParams{
		RouteID:        routeID,
		ServiceIds:     windows[0].serviceIDs,
		TimeRangeStart: windows[0].rangeStart.Nanoseconds(),
		TimeRangeEnd:   windows[0].rangeEnd.Nanoseconds(),
	})
	if err != nil {
		api.Logger.Warn("trips-for-route: failed to fetch layover blocks", "route_id", routeID, "error", err)
	} else {
		for _, blockID := range layoverBlocks {
			allLinkedBlocks[blockID] = true
		}
	}

	var nullBlockTrips []string
	for i, w := range windows {
		trips, err := api.GtfsManager.GtfsDB.Queries.GetActiveTripsWithNullBlockForRoute(ctx, gtfsdb.GetActiveTripsWithNullBlockForRouteParams{
			RouteID:        routeID,
			ServiceIds:     w.serviceIDs,
			TimeRangeStart: sql.NullInt64{Int64: w.rangeStart.Nanoseconds(), Valid: true},
			TimeRangeEnd:   sql.NullInt64{Int64: w.rangeEnd.Nanoseconds(), Valid: true},
		})
		if err != nil {
			if i == 0 {
				api.Logger.Warn("trips-for-route: failed to fetch null-block trips", "route_id", routeID, "error", err)
			} else {
				api.Logger.Warn("trips-for-route: failed to fetch previous-day null-block trips", "error", err)
			}
			continue
		}
		nullBlockTrips = append(nullBlockTrips, trips...)
	}

	return allLinkedBlocks, nullBlockTrips, nil
}

// resolveActiveTrips returns the trip IDs actually running at each window's
// query time: one active trip per candidate block (the earliest trip in the
// block whose stop times contain that window's query time), plus the given
// null-block trip IDs. If ctx is canceled mid-loop, the trips accumulated so
// far are returned; callers should check ctx.Err() afterward.
func (api *RestAPI) resolveActiveTrips(ctx context.Context, blockIDs map[string]bool, nullBlockTripIDs []string, windows []serviceDayWindow) []string {
	var activeTrips []string

	for blockID := range blockIDs {
		if ctx.Err() != nil {
			return activeTrips
		}

		blockIDNullStr := nulls.String(blockID)

		for _, w := range windows {
			tripsInBlock, err := api.GtfsManager.GtfsDB.Queries.GetTripsInBlock(ctx, gtfsdb.GetTripsInBlockParams{
				BlockID:    blockIDNullStr,
				ServiceIds: w.serviceIDs,
			})
			if err != nil {
				api.Logger.Warn("failed to fetch trips in block", "block_id", blockID, "error", err)
				continue
			}
			if len(tripsInBlock) == 0 {
				continue
			}

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

			activeTrips = append(activeTrips, activeTrip)
			break
		}
	}

	activeTrips = append(activeTrips, nullBlockTripIDs...)
	return activeTrips
}
