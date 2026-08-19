package gtfs

import (
	"context"
	"database/sql"
	"maps"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

// MetricsSnapshot captures a point-in-time view of agency coverage, scheduled
// trip counts, and GTFS-RT matching health, keyed by agency ID.
//
// "Matched" means a real-time trip/stop ID resolves against the static
// schedule. This is an approximation of the upstream Java implementation,
// which instead matches a trip update to a static block using
// schedule-deviation heuristics as a side effect of its GTFS-RT ingestion
// engine — a much stricter check than static-ID existence, so
// RealtimeTripCountsMatched will typically be higher here than in Java for
// the same feed. Record counting and matched/unmatched ID deduplication,
// however, are intentionally kept in step with Java's semantics: see
// countCombinedRecords and computeFeedMetrics.
type MetricsSnapshot struct {
	AgencyIDs                   []string
	ScheduledTripsCount         map[string]int
	RealtimeRecordsTotal        map[string]int
	RealtimeTripCountsMatched   map[string]int
	RealtimeTripCountsUnmatched map[string]int
	RealtimeTripIDsUnmatched    map[string][]string
	StopIDsMatchedCount         map[string]int
	StopIDsUnmatchedCount       map[string]int
	StopIDsUnmatched            map[string][]string
	TimeSinceLastRealtimeUpdate map[string]int64
}

// GetMetrics computes an aggregate health snapshot: currently-active trip
// counts per agency, plus GTFS-RT matching status (records received,
// matched/unmatched trip and stop IDs, and feed staleness) attributed to the
// agencies each feed is configured to cover. scheduleReferenceTime is the
// reference time used to decide which trips count as currently active; it
// does not affect real-time feed staleness, which is always measured against
// the real wall clock (see populateRealtimeMetrics).
func (manager *Manager) GetMetrics(ctx context.Context, scheduleReferenceTime time.Time) (MetricsSnapshot, error) {
	agencies, err := manager.GtfsDB.Queries.ListAgencies(ctx)
	if err != nil {
		return MetricsSnapshot{}, err
	}

	agencyIDs := make([]string, 0, len(agencies))
	for _, agency := range agencies {
		agencyIDs = append(agencyIDs, agency.ID)
	}

	snapshot := newMetricsSnapshot(agencyIDs)

	scheduledTripsCount, err := manager.activeTripsByAgency(ctx, scheduleReferenceTime, agencies)
	if err != nil {
		return MetricsSnapshot{}, err
	}
	maps.Copy(snapshot.ScheduledTripsCount, scheduledTripsCount)

	if err := manager.populateRealtimeMetrics(ctx, &snapshot); err != nil {
		return MetricsSnapshot{}, err
	}

	return snapshot, nil
}

func newMetricsSnapshot(agencyIDs []string) MetricsSnapshot {
	snapshot := MetricsSnapshot{
		AgencyIDs:                   agencyIDs,
		ScheduledTripsCount:         make(map[string]int, len(agencyIDs)),
		RealtimeRecordsTotal:        make(map[string]int, len(agencyIDs)),
		RealtimeTripCountsMatched:   make(map[string]int, len(agencyIDs)),
		RealtimeTripCountsUnmatched: make(map[string]int, len(agencyIDs)),
		RealtimeTripIDsUnmatched:    make(map[string][]string, len(agencyIDs)),
		StopIDsMatchedCount:         make(map[string]int, len(agencyIDs)),
		StopIDsUnmatchedCount:       make(map[string]int, len(agencyIDs)),
		StopIDsUnmatched:            make(map[string][]string, len(agencyIDs)),
		TimeSinceLastRealtimeUpdate: make(map[string]int64, len(agencyIDs)),
	}
	for _, agencyID := range agencyIDs {
		snapshot.ScheduledTripsCount[agencyID] = 0
		snapshot.RealtimeRecordsTotal[agencyID] = 0
		snapshot.RealtimeTripCountsMatched[agencyID] = 0
		snapshot.RealtimeTripCountsUnmatched[agencyID] = 0
		snapshot.RealtimeTripIDsUnmatched[agencyID] = []string{}
		snapshot.StopIDsMatchedCount[agencyID] = 0
		snapshot.StopIDsUnmatchedCount[agencyID] = 0
		snapshot.StopIDsUnmatched[agencyID] = []string{}
		// TimeSinceLastRealtimeUpdate is intentionally left unset here: its
		// accumulation tracks the freshest feed per agency by checking whether
		// an entry exists yet, so a pre-seeded 0 would look like "already
		// fresh" and block any real value from ever being recorded. Backfilled
		// to 0 for agencies with no covering feed at the end of
		// populateRealtimeMetrics.
	}
	return snapshot
}

// activeTripsByAgency counts, per agency, the blocks active at this exact
// instant, evaluated in each agency's own timezone. Despite the "scheduled"
// name, the upstream Java implementation reports currently-active blocks here
// (via BlockStatusServiceImpl#getActiveBlocksForAgency, queried with
// timeFrom == timeTo == now: a strict point-in-time check with no
// running-late/running-early tolerance, unlike trips-for-route). A block
// counts as active both while a trip is in progress and while laying over
// between two of its trips, so this counts both.
func (manager *Manager) activeTripsByAgency(ctx context.Context, now time.Time, agencies []gtfsdb.Agency) (map[string]int, error) {
	counts := make(map[string]int, len(agencies))
	for _, agency := range agencies {
		count, err := manager.activeTripsForAgency(ctx, now, agency)
		if err != nil {
			return nil, err
		}
		counts[agency.ID] = count
	}
	return counts, nil
}

func (manager *Manager) activeTripsForAgency(ctx context.Context, now time.Time, agency gtfsdb.Agency) (int, error) {
	loc, err := time.LoadLocation(agency.Timezone)
	if err != nil {
		loc = time.UTC
	}
	localNow := now.In(loc)
	midnight := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	sinceMidnight := max(localNow.Sub(midnight), 0)

	today, err := manager.countActiveBlocksAt(ctx, agency.ID, localNow, sinceMidnight)
	if err != nil {
		return 0, err
	}

	// GTFS allows departure times past 24:00:00 for trips that started
	// yesterday but are still running (e.g. 25:30:00 = 1:30 AM). Check
	// yesterday's service against the same instant shifted +24h.
	yesterday, err := manager.countActiveBlocksAt(ctx, agency.ID, localNow.AddDate(0, 0, -1), sinceMidnight+24*time.Hour)
	if err != nil {
		return 0, err
	}

	return today + yesterday, nil
}

// countActiveBlocksAt counts the distinct blocks active for one agency at the
// given instant — a trip in progress, or a layover between two of the
// block's trips — among the services active on serviceDay.
func (manager *Manager) countActiveBlocksAt(ctx context.Context, agencyID string, serviceDay time.Time, at time.Duration) (int, error) {
	serviceIDs, err := manager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDay.Format("20060102"))
	if err != nil {
		return 0, err
	}
	if len(serviceIDs) == 0 {
		return 0, nil
	}

	tripBlockIDs, err := manager.GtfsDB.Queries.GetActiveTripBlockIDsForAgency(ctx, gtfsdb.GetActiveTripBlockIDsForAgencyParams{
		AgencyID:   agencyID,
		At:         sql.NullInt64{Int64: at.Nanoseconds(), Valid: true},
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return 0, err
	}

	layoverBlockIDs, err := manager.GtfsDB.Queries.GetActiveLayoverBlockIDsForAgency(ctx, gtfsdb.GetActiveLayoverBlockIDsForAgencyParams{
		AgencyID:   agencyID,
		At:         at.Nanoseconds(),
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return 0, err
	}

	activeBlockIDs := make(map[string]bool, len(tripBlockIDs)+len(layoverBlockIDs))
	for _, blockID := range tripBlockIDs {
		activeBlockIDs[blockID] = true
	}
	for _, blockID := range layoverBlockIDs {
		activeBlockIDs[blockID] = true
	}
	return len(activeBlockIDs), nil
}

// realtimeFeedState is a snapshot of a single feed's real-time trips, its
// configured agency filter, and its last successful update time, copied out
// from under realTimeMutex so subsequent DB lookups don't hold the lock.
type realtimeFeedState struct {
	feedID       string
	trips        []gtfs.Trip
	agencyFilter map[string]bool
	lastUpdate   time.Time
	hasUpdate    bool
}

func (manager *Manager) snapshotRealtimeFeedState() []realtimeFeedState {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	states := make([]realtimeFeedState, 0, len(manager.feedTrips))
	for feedID, trips := range manager.feedTrips {
		lastUpdate, hasUpdate := manager.feedLastUpdate[feedID]
		states = append(states, realtimeFeedState{
			feedID:       feedID,
			trips:        trips,
			agencyFilter: manager.feedAgencyFilter[feedID],
			lastUpdate:   lastUpdate,
			hasUpdate:    hasUpdate,
		})
	}
	return states
}

// populateRealtimeMetrics computes matched/unmatched trip and stop counts for
// each feed and attributes them to the agencies that feed covers: its
// configured `agency-ids` filter if set, otherwise the agencies actually
// resolved from the feed's matched trips.
//
// Staleness is measured against the real wall clock, not the `now` GetMetrics
// received: feedLastUpdate is always stamped with time.Now() when a feed
// refreshes (see realtime.go), independent of any test clock injection, so
// comparing it against an injected `now` would produce a meaningless delta
// whenever that `now` isn't close to the real time.
func (manager *Manager) populateRealtimeMetrics(ctx context.Context, snapshot *MetricsSnapshot) error {
	now := time.Now()

	unmatchedTripIDsByAgency := make(map[string]map[string]bool, len(snapshot.AgencyIDs))
	unmatchedStopIDsByAgency := make(map[string]map[string]bool, len(snapshot.AgencyIDs))

	for _, feed := range manager.snapshotRealtimeFeedState() {
		metrics, err := manager.computeFeedMetrics(ctx, feed.trips, now)
		if err != nil {
			return err
		}

		agencyIDs := feed.agencyFilter
		if len(agencyIDs) == 0 {
			agencyIDs = metrics.resolvedAgencyIDs
		}

		var staleness int64
		if feed.hasUpdate {
			staleness = int64(now.Sub(feed.lastUpdate).Seconds())
		}

		for agencyID := range agencyIDs {
			applyFeedMetrics(snapshot, agencyID, metrics, feed.hasUpdate, staleness)
			addToAgencySet(unmatchedTripIDsByAgency, agencyID, metrics.tripIDsUnmatched)
			addToAgencySet(unmatchedStopIDsByAgency, agencyID, metrics.stopIDsUnmatched)
		}
	}

	for _, agencyID := range snapshot.AgencyIDs {
		if _, tracked := snapshot.TimeSinceLastRealtimeUpdate[agencyID]; !tracked {
			snapshot.TimeSinceLastRealtimeUpdate[agencyID] = 0
		}
		snapshot.RealtimeTripCountsUnmatched[agencyID] = len(unmatchedTripIDsByAgency[agencyID])
		snapshot.RealtimeTripIDsUnmatched[agencyID] = sortedKeys(unmatchedTripIDsByAgency[agencyID])
		snapshot.StopIDsUnmatchedCount[agencyID] = len(unmatchedStopIDsByAgency[agencyID])
		snapshot.StopIDsUnmatched[agencyID] = sortedKeys(unmatchedStopIDsByAgency[agencyID])
	}

	return nil
}

// addToAgencySet merges ids into agencyID's set within sets, so an ID that's
// unmatched by more than one feed covering the same agency is only counted
// once in the final snapshot.
func addToAgencySet(sets map[string]map[string]bool, agencyID string, ids []string) {
	set, ok := sets[agencyID]
	if !ok {
		set = make(map[string]bool, len(ids))
		sets[agencyID] = set
	}
	for _, id := range ids {
		set[id] = true
	}
}

// feedMetrics is the matched/unmatched breakdown computed for a single feed.
type feedMetrics struct {
	recordsTotal      int
	tripsMatched      int
	tripsUnmatched    int
	tripIDsUnmatched  []string
	stopsMatched      int
	stopsUnmatched    int
	stopIDsUnmatched  []string
	resolvedAgencyIDs map[string]bool
}

// computeFeedMetrics cross-references a feed's real-time trips (and the stops
// referenced in their stop_time_updates) against the static schedule to
// determine what matched.
//
// recordsTotal and stop matching are deduplicated, mirroring the upstream
// Java implementation (GtfsRealtimeSource#handleUpdates groups trip updates
// by block before counting records — see groupTripsByBlock — and
// MonitoredResult tracks matched/unmatched stop IDs as Sets): a block with
// several trip updates in one poll (e.g. a current trip plus a look-ahead
// next trip) is one record, and a busy stop referenced by many trips is
// counted once, not once per trip that passes through it.
//
// Matched trip counting follows the same block grouping, gated by
// isCombinedRecordActive: see that function's comment for why a resolved
// but not-currently-active block counts toward neither matched nor
// unmatched, matching GtfsRealtimeTripLibrary#createVehicleLocationRecordForUpdate.
func (manager *Manager) computeFeedMetrics(ctx context.Context, trips []gtfs.Trip, now time.Time) (feedMetrics, error) {
	metrics := feedMetrics{resolvedAgencyIDs: map[string]bool{}}
	if len(trips) == 0 {
		return metrics, nil
	}

	tripRouteByID, tripBlockByID, err := manager.staticTripLookups(ctx, trips)
	if err != nil {
		return feedMetrics{}, err
	}

	staticStopIDs, err := manager.staticStopIDsForTrips(ctx, trips)
	if err != nil {
		return feedMetrics{}, err
	}

	tripGroups := groupTripsByBlock(trips, tripBlockByID)
	metrics.recordsTotal = len(tripGroups)
	metrics.tripsMatched = countMatchedGroups(tripGroups, tripRouteByID, now)

	classification := classifyTrips(trips, tripRouteByID, staticStopIDs)
	metrics.tripsUnmatched = len(classification.unmatchedTripIDs)
	metrics.tripIDsUnmatched = sortedKeys(classification.unmatchedTripIDs)
	metrics.stopsMatched = len(classification.matchedStopIDs)
	metrics.stopsUnmatched = len(classification.unmatchedStopIDs)
	metrics.stopIDsUnmatched = sortedKeys(classification.unmatchedStopIDs)

	resolvedAgencyIDs, err := manager.agencyIDsForRoutes(ctx, classification.routeIDs)
	if err != nil {
		return feedMetrics{}, err
	}
	metrics.resolvedAgencyIDs = resolvedAgencyIDs

	return metrics, nil
}

// staticTripLookups resolves a feed poll's trip IDs against the static
// schedule, returning each matched trip's route and (if present) block ID.
func (manager *Manager) staticTripLookups(ctx context.Context, trips []gtfs.Trip) (tripRouteByID, tripBlockByID map[string]string, err error) {
	staticTrips, err := manager.GtfsDB.Queries.GetTripsByIDs(ctx, collectTripIDs(trips))
	if err != nil {
		return nil, nil, err
	}

	tripRouteByID = make(map[string]string, len(staticTrips))
	tripBlockByID = make(map[string]string, len(staticTrips))
	for _, staticTrip := range staticTrips {
		tripRouteByID[staticTrip.ID] = staticTrip.RouteID
		if blockID := nulls.StringOrEmpty(staticTrip.BlockID); blockID != "" {
			tripBlockByID[staticTrip.ID] = blockID
		}
	}
	return tripRouteByID, tripBlockByID, nil
}

// staticStopIDsForTrips resolves the stop IDs referenced by a feed poll's
// stop_time_updates against the static schedule.
func (manager *Manager) staticStopIDsForTrips(ctx context.Context, trips []gtfs.Trip) (map[string]bool, error) {
	staticStops, err := manager.GtfsDB.Queries.GetStopsByIDs(ctx, collectStopIDs(trips))
	if err != nil {
		return nil, err
	}

	staticStopIDs := make(map[string]bool, len(staticStops))
	for _, staticStop := range staticStops {
		staticStopIDs[staticStop.ID] = true
	}
	return staticStopIDs, nil
}

// tripClassification is the per-trip breakdown computeFeedMetrics needs:
// which routes were referenced (for agency attribution), which trip IDs
// didn't resolve statically, and which referenced stop IDs did/didn't.
type tripClassification struct {
	routeIDs         map[string]bool
	unmatchedTripIDs map[string]bool
	matchedStopIDs   map[string]bool
	unmatchedStopIDs map[string]bool
}

func classifyTrips(trips []gtfs.Trip, tripRouteByID map[string]string, staticStopIDs map[string]bool) tripClassification {
	result := tripClassification{
		routeIDs:         make(map[string]bool, len(tripRouteByID)),
		unmatchedTripIDs: make(map[string]bool),
		matchedStopIDs:   make(map[string]bool),
		unmatchedStopIDs: make(map[string]bool),
	}

	for _, trip := range trips {
		if routeID, matched := tripRouteByID[trip.ID.ID]; matched {
			result.routeIDs[routeID] = true
		} else {
			result.unmatchedTripIDs[trip.ID.ID] = true
		}
		classifyStopTimeUpdates(trip.StopTimeUpdates, staticStopIDs, result.matchedStopIDs, result.unmatchedStopIDs)
	}

	return result
}

func classifyStopTimeUpdates(updates []gtfs.StopTimeUpdate, staticStopIDs, matchedStopIDs, unmatchedStopIDs map[string]bool) {
	for _, stopTimeUpdate := range updates {
		if stopTimeUpdate.StopID == nil {
			continue
		}
		if staticStopIDs[*stopTimeUpdate.StopID] {
			matchedStopIDs[*stopTimeUpdate.StopID] = true
		} else {
			unmatchedStopIDs[*stopTimeUpdate.StopID] = true
		}
	}
}

// countMatchedGroups counts the block-grouped records that resolve
// statically and are currently active; see isCombinedRecordActive for why
// a resolved but not-currently-active block counts toward neither matched
// nor unmatched.
func countMatchedGroups(tripGroups map[string][]gtfs.Trip, tripRouteByID map[string]string, now time.Time) int {
	matched := 0
	for _, group := range tripGroups {
		if groupHasStaticMatch(group, tripRouteByID) && isCombinedRecordActive(group, now) {
			matched++
		}
	}
	return matched
}

// groupTripsByBlock groups a feed poll's trip updates by their static block
// ID, falling back to the trip itself when it has no resolvable block:
// GtfsRealtimeTripLibrary groups GTFS-RT trip updates by BlockDescriptor
// (not by vehicle ID, and not one record per trip_update entity) — a
// vehicle's current trip and a vehicle-less look-ahead trip for its next
// leg share the same block and so collapse into a single record, regardless
// of which entities happen to carry a vehicle tag. Verified directly against
// a live feed: 217 trip_update entities resolved to exactly 72 distinct
// static block IDs, matching production Java's recordsTotal for that same
// poll exactly.
func groupTripsByBlock(trips []gtfs.Trip, tripBlockByID map[string]string) map[string][]gtfs.Trip {
	groups := make(map[string][]gtfs.Trip, len(trips))
	for _, trip := range trips {
		key := "trip:" + trip.ID.ID
		if blockID, hasBlock := tripBlockByID[trip.ID.ID]; hasBlock {
			key = "block:" + blockID
		}
		groups[key] = append(groups[key], trip)
	}
	return groups
}

func groupHasStaticMatch(group []gtfs.Trip, tripRouteByID map[string]string) bool {
	for _, trip := range group {
		if _, matched := tripRouteByID[trip.ID.ID]; matched {
			return true
		}
	}
	return false
}

// activeRecordLookahead is how far in the future a combined record's first
// predicted stop time can be while the record still counts as active,
// matching GtfsRealtimeTripLibrary#isTripActive's windowFuture.
const activeRecordLookahead = time.Hour

// isCombinedRecordActive reports whether a block's combined record is
// currently active, mirroring GtfsRealtimeTripLibrary#isTripActive: Java
// only adds a resolved record to matchedTripIds when its representative
// trip update's first predicted stop time is no more than an hour away and
// its last predicted stop time hasn't passed yet — a resolved but
// not-yet-started-soon or already-finished record is silently excluded from
// both matched and unmatched. The representative trip is the one with the
// earliest first-stop prediction in the group: Maglev's parsed trip data
// doesn't preserve GTFS-RT feed entity order the way Java's does, so this
// approximates "the currently active leg" when a block also has a
// vehicle-less look-ahead trip for its next leg. Verified directly against a
// live feed: this reproduced Java's matched count within 1 of 57.
func isCombinedRecordActive(group []gtfs.Trip, now time.Time) bool {
	trip, ok := representativeTrip(group)
	if !ok {
		return false
	}
	return isTripActive(trip, now)
}

func representativeTrip(group []gtfs.Trip) (gtfs.Trip, bool) {
	var best gtfs.Trip
	var bestPrediction time.Time
	found := false
	for _, trip := range group {
		if len(trip.StopTimeUpdates) == 0 {
			continue
		}
		prediction := firstPredictionTime(trip.StopTimeUpdates[0])
		if prediction == nil {
			continue
		}
		if !found || prediction.Before(bestPrediction) {
			best = trip
			bestPrediction = *prediction
			found = true
		}
	}
	return best, found
}

func isTripActive(trip gtfs.Trip, now time.Time) bool {
	if len(trip.StopTimeUpdates) == 0 {
		return false
	}
	first := firstPredictionTime(trip.StopTimeUpdates[0])
	last := lastPredictionTime(trip.StopTimeUpdates[len(trip.StopTimeUpdates)-1])
	if first == nil || last == nil {
		return false
	}
	return now.Add(activeRecordLookahead).After(*first) && last.After(now)
}

func firstPredictionTime(stopTimeUpdate gtfs.StopTimeUpdate) *time.Time {
	if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Time != nil {
		return stopTimeUpdate.Arrival.Time
	}
	if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Time != nil {
		return stopTimeUpdate.Departure.Time
	}
	return nil
}

func lastPredictionTime(stopTimeUpdate gtfs.StopTimeUpdate) *time.Time {
	if stopTimeUpdate.Departure != nil && stopTimeUpdate.Departure.Time != nil {
		return stopTimeUpdate.Departure.Time
	}
	if stopTimeUpdate.Arrival != nil && stopTimeUpdate.Arrival.Time != nil {
		return stopTimeUpdate.Arrival.Time
	}
	return nil
}

// sortedKeys returns the keys of a set as a sorted slice, so API responses
// have a deterministic order instead of Go's randomized map iteration order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// agencyIDsForRoutes resolves the set of agency IDs that own the given static
// route IDs. Used to attribute a feed's metrics to agencies when the feed has
// no explicit `agency-ids` configuration.
func (manager *Manager) agencyIDsForRoutes(ctx context.Context, routeIDs map[string]bool) (map[string]bool, error) {
	if len(routeIDs) == 0 {
		return map[string]bool{}, nil
	}

	ids := make([]string, 0, len(routeIDs))
	for routeID := range routeIDs {
		ids = append(ids, routeID)
	}

	routes, err := manager.GtfsDB.Queries.GetRoutesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	agencyIDs := make(map[string]bool, len(routes))
	for _, route := range routes {
		agencyIDs[route.AgencyID] = true
	}
	return agencyIDs, nil
}

// applyFeedMetrics accumulates a feed's per-agency totals into snapshot.
// Unmatched trip/stop IDs are handled separately by populateRealtimeMetrics
// (via addToAgencySet), since they need deduplicating across feeds that
// cover the same agency rather than summed directly.
func applyFeedMetrics(snapshot *MetricsSnapshot, agencyID string, metrics feedMetrics, hasUpdate bool, staleness int64) {
	snapshot.RealtimeRecordsTotal[agencyID] += metrics.recordsTotal
	snapshot.RealtimeTripCountsMatched[agencyID] += metrics.tripsMatched
	snapshot.StopIDsMatchedCount[agencyID] += metrics.stopsMatched

	if hasUpdate {
		existing, tracked := snapshot.TimeSinceLastRealtimeUpdate[agencyID]
		if !tracked || staleness < existing {
			snapshot.TimeSinceLastRealtimeUpdate[agencyID] = staleness
		}
	}
}

func collectTripIDs(trips []gtfs.Trip) []string {
	seen := make(map[string]bool, len(trips))
	ids := make([]string, 0, len(trips))
	for _, trip := range trips {
		if !seen[trip.ID.ID] {
			seen[trip.ID.ID] = true
			ids = append(ids, trip.ID.ID)
		}
	}
	return ids
}

func collectStopIDs(trips []gtfs.Trip) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, trip := range trips {
		for _, stopTimeUpdate := range trip.StopTimeUpdates {
			if stopTimeUpdate.StopID == nil || seen[*stopTimeUpdate.StopID] {
				continue
			}
			seen[*stopTimeUpdate.StopID] = true
			ids = append(ids, *stopTimeUpdate.StopID)
		}
	}
	return ids
}
