package gtfs

import (
	"context"
	"maps"
	"sort"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// MetricsSnapshot captures a point-in-time view of agency coverage, scheduled
// trip counts, and GTFS-RT matching health, keyed by agency ID.
//
// "Matched" means a real-time trip/stop ID resolves against the static
// schedule and, for trips, that its block has a currently active prediction
// window (see isCombinedRecordActive), mirroring Java's
// GtfsRealtimeTripLibrary#isTripActive gate. Record counting and
// matched/unmatched ID deduplication follow Java's semantics too: see
// countMatchedGroupsByAgency and computeFeedMetrics.
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
	// TimeSinceLastRealtimeUpdate is seconds since the most-stale feed covering
	// the agency last updated successfully. It is realtimeUpdateUnknown (-1)
	// when the agency is covered by a configured feed that has never
	// successfully updated (or was cleared as stale — see clearFeedData), and
	// 0 only when no configured feed covers the agency at all: a real 0 would
	// misread as "just updated" for a feed that's actually dead.
	TimeSinceLastRealtimeUpdate map[string]int64
}

// realtimeUpdateUnknown marks an agency that's covered by a configured
// real-time feed whose freshness can't currently be determined, as distinct
// from an agency with no covering feed at all (which reports 0).
const realtimeUpdateUnknown int64 = -1

// GetMetrics computes an aggregate health snapshot: currently-active trip
// counts per agency, plus GTFS-RT matching status (records received,
// matched/unmatched trip and stop IDs, and feed staleness) attributed to the
// agencies each feed covers. scheduleReferenceTime is the reference time
// used to decide which scheduled and real-time trips count as currently
// active; it does not affect real-time feed staleness, which is always
// measured against the real wall clock (see populateRealtimeMetrics).
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

	if err := manager.populateRealtimeMetrics(ctx, &snapshot, scheduleReferenceTime); err != nil {
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
		// to realtimeUpdateUnknown or 0 at the end of populateRealtimeMetrics,
		// depending on whether the agency has a covering feed at all.
	}
	return snapshot
}

// activeTripsByAgency counts, per agency, the blocks active at this exact
// instant, evaluated in each agency's own timezone. Despite the "scheduled"
// name, the upstream Java implementation reports currently-active blocks here
// (via BlockStatusServiceImpl#getActiveBlocksForAgency, queried with
// timeFrom == timeTo == now: a strict point-in-time check with no
// running-late/running-early tolerance, unlike trips-for-route). A block
// counts as active from its first trip's start to its last trip's end, so
// both trips in progress and layovers between trips count.
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
	// Wall-clock math matches GTFS seconds-since-midnight semantics:
	// time.Sub diverges by 3600s during the DST fall-back ambiguous hour
	// (see CalculateSecondsSinceServiceDate for the full explanation).
	h, m, s := localNow.Clock()
	sinceMidnight := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second

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
// given instant — between the block's first trip start and last trip end —
// among the services active on serviceDay.
func (manager *Manager) countActiveBlocksAt(ctx context.Context, agencyID string, serviceDay time.Time, at time.Duration) (int, error) {
	serviceIDs, err := manager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, serviceDay.Format("20060102"))
	if err != nil {
		return 0, err
	}
	if len(serviceIDs) == 0 {
		return 0, nil
	}

	activeBlockIDs, err := manager.GtfsDB.Queries.GetActiveBlockIDsForAgency(ctx, gtfsdb.GetActiveBlockIDsForAgencyParams{
		AgencyID:   agencyID,
		At:         at.Nanoseconds(),
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return 0, err
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

// snapshotRealtimeFeedState enumerates every feed the manager knows about.
// The union of all five per-feed maps is needed: any one alone can miss a
// vehicle-positions-only feed (no feedTrips entry) or a configured feed
// that has never fetched (only feedAgencyFilter entry).
func (manager *Manager) snapshotRealtimeFeedState() []realtimeFeedState {
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()

	feedIDs := make(map[string]bool, len(manager.feedTrips))
	for feedID := range manager.feedTrips {
		feedIDs[feedID] = true
	}
	for feedID := range manager.feedVehicles {
		feedIDs[feedID] = true
	}
	for feedID := range manager.feedAlerts {
		feedIDs[feedID] = true
	}
	for feedID := range manager.feedLastUpdate {
		feedIDs[feedID] = true
	}
	for feedID := range manager.feedAgencyFilter {
		feedIDs[feedID] = true
	}

	states := make([]realtimeFeedState, 0, len(feedIDs))
	for feedID := range feedIDs {
		lastUpdate, hasUpdate := manager.feedLastUpdate[feedID]
		states = append(states, realtimeFeedState{
			feedID:       feedID,
			trips:        manager.feedTrips[feedID],
			agencyFilter: manager.feedAgencyFilter[feedID],
			lastUpdate:   lastUpdate,
			hasUpdate:    hasUpdate,
		})
	}
	return states
}

// populateRealtimeMetrics computes matched/unmatched trip and stop counts for
// each feed and attributes them to the agencies that feed covers: its
// configured `agency-ids` filter if set, otherwise every static agency,
// matching GtfsRealtimeSource#start. Matched trip counts are the exception:
// they go to each matched trip's own agency, matching
// MetricsBeanServiceImpl#getValidRealtimeTripIds.
//
// Trip activity is judged against scheduleReferenceTime, but staleness is
// measured against the real wall clock: feedLastUpdate is always stamped
// with time.Now() when a feed refreshes (see realtime.go), independent of
// any test clock injection, so comparing it against an injected time would
// produce a meaningless delta whenever that time isn't close to the real one.
func (manager *Manager) populateRealtimeMetrics(ctx context.Context, snapshot *MetricsSnapshot, scheduleReferenceTime time.Time) error {
	wallClockNow := time.Now()

	allAgencies := make(map[string]bool, len(snapshot.AgencyIDs))
	for _, agencyID := range snapshot.AgencyIDs {
		allAgencies[agencyID] = true
	}

	unmatchedTripIDsByAgency := make(map[string]map[string]bool, len(snapshot.AgencyIDs))
	matchedStopIDsByAgency := make(map[string]map[string]bool, len(snapshot.AgencyIDs))
	unmatchedStopIDsByAgency := make(map[string]map[string]bool, len(snapshot.AgencyIDs))

	for _, feed := range manager.snapshotRealtimeFeedState() {
		metrics, err := manager.computeFeedMetrics(ctx, feed.trips, scheduleReferenceTime)
		if err != nil {
			return err
		}

		// A feed that has never successfully updated is the worst possible
		// staleness: use realtimeUpdateUnknown so it propagates correctly
		// through the max-staleness-wins comparison even when a sibling feed
		// covering the same agency is healthy.
		staleness := realtimeUpdateUnknown
		if feed.hasUpdate {
			staleness = int64(wallClockNow.Sub(feed.lastUpdate).Seconds())
		}

		coveredAgencies := feed.agencyFilter
		if len(coveredAgencies) == 0 {
			coveredAgencies = allAgencies
		}

		for agencyID := range coveredAgencies {
			if !allAgencies[agencyID] {
				// Skip agencies absent from static GTFS — a stale or
				// misspelled agency-ids entry must not create orphan keys.
				continue
			}
			snapshot.RealtimeRecordsTotal[agencyID] += metrics.recordsTotal
			updateStaleness(snapshot, agencyID, staleness)
			addToAgencySet(unmatchedTripIDsByAgency, agencyID, metrics.tripIDsUnmatched)
			addToAgencySet(matchedStopIDsByAgency, agencyID, metrics.stopIDsMatched)
			addToAgencySet(unmatchedStopIDsByAgency, agencyID, metrics.stopIDsUnmatched)
		}

		for agencyID, matched := range metrics.tripsMatchedByAgency {
			if allAgencies[agencyID] {
				snapshot.RealtimeTripCountsMatched[agencyID] += matched
			}
		}
	}

	for _, agencyID := range snapshot.AgencyIDs {
		if _, tracked := snapshot.TimeSinceLastRealtimeUpdate[agencyID]; !tracked {
			snapshot.TimeSinceLastRealtimeUpdate[agencyID] = 0
		}
		snapshot.RealtimeTripCountsUnmatched[agencyID] = len(unmatchedTripIDsByAgency[agencyID])
		snapshot.RealtimeTripIDsUnmatched[agencyID] = sortedKeys(unmatchedTripIDsByAgency[agencyID])
		snapshot.StopIDsMatchedCount[agencyID] = len(matchedStopIDsByAgency[agencyID])
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
	recordsTotal         int
	tripsMatchedByAgency map[string]int
	tripIDsUnmatched     []string
	stopIDsMatched       []string
	stopIDsUnmatched     []string
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
	metrics := feedMetrics{tripsMatchedByAgency: map[string]int{}}
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

	agencyByRouteID, err := manager.agencyIDsByRouteID(ctx, tripRouteByID)
	if err != nil {
		return feedMetrics{}, err
	}

	tripGroups := groupTripsByBlock(trips, tripBlockByID)
	metrics.recordsTotal = len(tripGroups)
	metrics.tripsMatchedByAgency = countMatchedGroupsByAgency(tripGroups, tripRouteByID, agencyByRouteID, now)

	classification := classifyTrips(trips, tripRouteByID, staticStopIDs)
	metrics.tripIDsUnmatched = sortedKeys(classification.unmatchedTripIDs)
	metrics.stopIDsMatched = sortedKeys(classification.matchedStopIDs)
	metrics.stopIDsUnmatched = sortedKeys(classification.unmatchedStopIDs)

	return metrics, nil
}

// staticTripLookups resolves a feed poll's trip IDs against the static
// schedule, returning each matched trip's route and (if present) block ID.
func (manager *Manager) staticTripLookups(ctx context.Context, trips []gtfs.Trip) (tripRouteByID, tripBlockByID map[string]string, err error) {
	staticTrips, err := utils.QueryInBatches(ctx, collectTripIDs(trips), manager.GtfsDB.Queries.GetTripsByIDs)
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
	staticStops, err := utils.QueryInBatches(ctx, collectStopIDs(trips), manager.GtfsDB.Queries.GetStopsByIDs)
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
// which trip IDs didn't resolve statically, and which referenced stop IDs
// did/didn't.
type tripClassification struct {
	unmatchedTripIDs map[string]bool
	matchedStopIDs   map[string]bool
	unmatchedStopIDs map[string]bool
}

func classifyTrips(trips []gtfs.Trip, tripRouteByID map[string]string, staticStopIDs map[string]bool) tripClassification {
	result := tripClassification{
		unmatchedTripIDs: make(map[string]bool),
		matchedStopIDs:   make(map[string]bool),
		unmatchedStopIDs: make(map[string]bool),
	}

	for _, trip := range trips {
		if _, matched := tripRouteByID[trip.ID.ID]; !matched {
			result.unmatchedTripIDs[trip.ID.ID] = true
		}
		classifyStopTimeUpdates(trip.StopTimeUpdates, staticStopIDs, &result)
	}

	return result
}

func classifyStopTimeUpdates(updates []gtfs.StopTimeUpdate, staticStopIDs map[string]bool, result *tripClassification) {
	for _, stopTimeUpdate := range updates {
		if stopTimeUpdate.StopID == nil {
			continue
		}
		if staticStopIDs[*stopTimeUpdate.StopID] {
			result.matchedStopIDs[*stopTimeUpdate.StopID] = true
		} else {
			result.unmatchedStopIDs[*stopTimeUpdate.StopID] = true
		}
	}
}

// countMatchedGroupsByAgency counts, per agency, the block-grouped records
// that resolve statically and are currently active; see
// isCombinedRecordActive for why a resolved but not-currently-active block
// counts toward neither matched nor unmatched. Each record goes to the
// agency of its first statically matched trip, the counterpart of Java
// splitting matched trip IDs by their agency prefix.
func countMatchedGroupsByAgency(tripGroups map[string][]gtfs.Trip, tripRouteByID, agencyByRouteID map[string]string, now time.Time) map[string]int {
	matchedByAgency := make(map[string]int)
	for _, group := range tripGroups {
		routeID, matched := firstStaticRouteID(group, tripRouteByID)
		if !matched || !isCombinedRecordActive(group, now) {
			continue
		}
		if agencyID, ok := agencyByRouteID[routeID]; ok {
			matchedByAgency[agencyID]++
		}
	}
	return matchedByAgency
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

func firstStaticRouteID(group []gtfs.Trip, tripRouteByID map[string]string) (string, bool) {
	for _, trip := range group {
		if routeID, matched := tripRouteByID[trip.ID.ID]; matched {
			return routeID, true
		}
	}
	return "", false
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
// both matched and unmatched.
//
// Any trip in the group being active is sufficient: picking a single
// "representative trip" by earliest first-stop prediction would select a
// just-finished trip over an active sibling leg, causing the block to be
// missed even when a genuine active leg exists in the same poll.
func isCombinedRecordActive(group []gtfs.Trip, now time.Time) bool {
	for _, trip := range group {
		if isTripActive(trip, now) {
			return true
		}
	}
	return false
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

// agencyIDsByRouteID resolves the owning agency of every static route
// referenced by a feed poll's matched trips.
func (manager *Manager) agencyIDsByRouteID(ctx context.Context, tripRouteByID map[string]string) (map[string]string, error) {
	routeIDs := make(map[string]bool, len(tripRouteByID))
	for _, routeID := range tripRouteByID {
		routeIDs[routeID] = true
	}
	if len(routeIDs) == 0 {
		return map[string]string{}, nil
	}

	routes, err := utils.QueryInBatches(ctx, sortedKeys(routeIDs), manager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		return nil, err
	}

	agencyByRouteID := make(map[string]string, len(routes))
	for _, route := range routes {
		agencyByRouteID[route.ID] = route.AgencyID
	}
	return agencyByRouteID, nil
}

// updateStaleness keeps the worst staleness value across feeds covering the
// same agency. staleness is either a non-negative seconds value (feed has
// updated) or realtimeUpdateUnknown (-1, feed has never updated);
// isStalerThan treats realtimeUpdateUnknown as worse than any non-negative
// value, so a never-updated sibling feed always wins over a healthy one.
func updateStaleness(snapshot *MetricsSnapshot, agencyID string, staleness int64) {
	existing, tracked := snapshot.TimeSinceLastRealtimeUpdate[agencyID]
	if !tracked || isStalerThan(staleness, existing) {
		snapshot.TimeSinceLastRealtimeUpdate[agencyID] = staleness
	}
}

// isStalerThan reports whether candidate represents a worse freshness state
// than existing. realtimeUpdateUnknown (-1) is treated as worse than any
// non-negative value; among non-negative values, larger (older) wins.
func isStalerThan(candidate, existing int64) bool {
	if candidate == realtimeUpdateUnknown {
		return existing != realtimeUpdateUnknown
	}
	if existing == realtimeUpdateUnknown {
		return false
	}
	return candidate > existing
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
