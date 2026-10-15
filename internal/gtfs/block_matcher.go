package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// BlockMatch is the block instance the legacy matcher resolved for a
// realtime trip. It is Maglev-local export metadata.
type BlockMatch struct {
	TripID   string
	RouteID  string
	AgencyID string // owner of the matched trip's route, i.e. the block's agency
	// BlockID is the static block, or the trip ID for a trip without one.
	BlockID     string
	ServiceDate time.Time     // midnight of the chosen candidate date, agency zone
	TripStart   time.Duration // matched trip's first static departure
	// FrequencyBased marks a trip listed in frequencies.txt, whose realtime
	// start_time names the running instance and TripStart cannot replace.
	FrequencyBased bool
}

const (
	blockMatchCacheTTL     = 30 * time.Minute
	blockMatchEarlyCutHour = 4
	blockMatchLateCutHour  = 21
)

type blockMatchEntry struct {
	match      *BlockMatch // nil records an unresolved trip
	insertedAt time.Time
}

// blockMatcher ports the legacy OneBusAway BlockFinder used to pick the
// service instance of an ordinary scheduled trip. It deliberately keeps the
// legacy quirks the export spec accepts: fixed date windows, first-match
// selection, ignored start_date/start_time hints, and a per-source cache
// keyed only by trip ID. Unlike legacy, the windows use the agency's
// timezone, not the server's.
type blockMatcher struct {
	queries *gtfsdb.Queries
	now     func() time.Time

	mu     sync.Mutex
	caches map[string]map[string]blockMatchEntry // feed ID -> trip ID -> entry
	// generation counts Clear calls, so a match resolved from static data
	// replaced during the match is not cached.
	generation uint64
}

func newBlockMatcher(queries *gtfsdb.Queries, now func() time.Time) *blockMatcher {
	return &blockMatcher{
		queries: queries,
		now:     now,
		caches:  make(map[string]map[string]blockMatchEntry),
	}
}

// blockMatchLookups memoizes the static lookups shared by every trip one
// refresh matches: trip rows, active services per date, agency zones and
// the frequency-based trips, each loaded on first use.
type blockMatchLookups struct {
	trips            staticTripRows
	serviceIDs       serviceIDsByDate
	agencyZones      map[string]*time.Location
	frequencyTripIDs map[string]struct{}
}

func newBlockMatchLookups() *blockMatchLookups {
	return &blockMatchLookups{
		trips:       staticTripRows{},
		serviceIDs:  serviceIDsByDate{},
		agencyZones: make(map[string]*time.Location),
	}
}

// blockMatchRequest is one trip to match and its legacy matching reference.
type blockMatchRequest struct {
	tripID    string
	reference time.Time
}

// Match returns the block of each requested trip as seen by feedID's
// source. Results cached within the last 30 minutes are reused, and the
// other trips are resolved together, so the query count does not grow with
// the number of trips. A nil match means the trip is unresolved. When a
// query fails, Match returns the cached matches with the error and caches
// nothing new.
func (m *blockMatcher) Match(ctx context.Context, feedID string, requests []blockMatchRequest, lookups *blockMatchLookups) (map[string]*BlockMatch, error) {
	matches, pending, generation := m.cachedMatches(feedID, requests)
	if len(pending) == 0 {
		return matches, nil
	}
	resolved, err := m.resolve(ctx, pending, lookups)
	if err != nil {
		return matches, err
	}
	m.store(feedID, resolved, generation)
	maps.Copy(matches, resolved)
	return matches, nil
}

// Clear drops every cached association; static GTFS replacement calls it.
func (m *blockMatcher) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caches = make(map[string]map[string]blockMatchEntry)
	m.generation++
}

// cachedMatches splits requests into live cached matches and the requests
// still to resolve, and returns the cache generation to pass to store with
// their results.
func (m *blockMatcher) cachedMatches(feedID string, requests []blockMatchRequest) (map[string]*BlockMatch, []blockMatchRequest, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matches := make(map[string]*BlockMatch, len(requests))
	var pending []blockMatchRequest
	for _, request := range requests {
		entry, ok := m.caches[feedID][request.tripID]
		if !ok || m.now().Sub(entry.insertedAt) >= blockMatchCacheTTL {
			pending = append(pending, request)
			continue
		}
		matches[request.tripID] = entry.match
	}
	return matches, pending, m.generation
}

// store caches resolved unless the cache was cleared after generation was
// read, since the matches may then come from replaced static data.
func (m *blockMatcher) store(feedID string, resolved map[string]*BlockMatch, generation uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation {
		return
	}
	if m.caches[feedID] == nil {
		m.caches[feedID] = make(map[string]blockMatchEntry)
	}
	insertedAt := m.now()
	for tripID, match := range resolved {
		m.caches[feedID][tripID] = blockMatchEntry{match: match, insertedAt: insertedAt}
	}
}

// resolve matches the pending trips, reading their trips, routes and first
// departures in batches.
func (m *blockMatcher) resolve(ctx context.Context, pending []blockMatchRequest, lookups *blockMatchLookups) (map[string]*BlockMatch, error) {
	tripIDs := make([]string, 0, len(pending))
	for _, request := range pending {
		tripIDs = append(tripIDs, request.tripID)
	}
	if err := lookups.trips.load(ctx, m.queries, tripIDs); err != nil {
		return nil, err
	}
	routes, err := m.tripRoutes(ctx, tripIDs, lookups.trips)
	if err != nil {
		return nil, err
	}
	departures, err := m.firstDepartures(ctx, tripIDs)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]*BlockMatch, len(pending))
	for _, request := range pending {
		match, err := m.resolveTrip(ctx, request, routes, departures, lookups)
		if err != nil {
			return nil, err
		}
		resolved[request.tripID] = match
	}
	return resolved, nil
}

func (m *blockMatcher) resolveTrip(ctx context.Context, request blockMatchRequest, routes map[string]gtfsdb.Route, departures map[string]time.Duration, lookups *blockMatchLookups) (*BlockMatch, error) {
	trip, ok := lookups.trips.get(request.tripID)
	if !ok {
		return nil, nil
	}
	route, ok := routes[trip.RouteID]
	if !ok {
		return nil, nil
	}
	agencyZone, err := m.agencyLocation(ctx, route.AgencyID, lookups)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tripStart, ok := departures[trip.ID]
	if !ok {
		return nil, nil
	}
	serviceDate, found, err := m.firstActiveServiceDate(ctx, trip, request.reference.In(agencyZone), lookups)
	if err != nil || !found {
		return nil, err
	}
	frequencyBased, err := m.isFrequencyTrip(ctx, trip.ID, lookups)
	if err != nil {
		return nil, err
	}
	return &BlockMatch{
		TripID:         trip.ID,
		RouteID:        trip.RouteID,
		AgencyID:       route.AgencyID,
		BlockID:        tripBlockKey(trip),
		ServiceDate:    serviceDate,
		TripStart:      tripStart,
		FrequencyBased: frequencyBased,
	}, nil
}

// tripRoutes reads the routes of the loaded trips, keyed by route ID.
func (m *blockMatcher) tripRoutes(ctx context.Context, tripIDs []string, trips staticTripRows) (map[string]gtfsdb.Route, error) {
	routeIDs := make(map[string]struct{})
	for _, tripID := range tripIDs {
		if trip, ok := trips.get(tripID); ok {
			routeIDs[trip.RouteID] = struct{}{}
		}
	}
	rows, err := utils.QueryInBatches(ctx, slices.Sorted(maps.Keys(routeIDs)), m.queries.GetRoutesByIDs)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]gtfsdb.Route, len(rows))
	for _, route := range rows {
		routes[route.ID] = route
	}
	return routes, nil
}

func (m *blockMatcher) isFrequencyTrip(ctx context.Context, tripID string, lookups *blockMatchLookups) (bool, error) {
	if lookups.frequencyTripIDs == nil {
		tripIDs, err := m.queries.GetFrequencyTripIDs(ctx)
		if err != nil {
			return false, err
		}
		lookups.frequencyTripIDs = make(map[string]struct{}, len(tripIDs))
		for _, id := range tripIDs {
			lookups.frequencyTripIDs[id] = struct{}{}
		}
	}
	_, ok := lookups.frequencyTripIDs[tripID]
	return ok, nil
}

// agencyLocation is the zone candidate service dates are read in. Using the
// agency's timezone instead of the server's (as legacy did) keeps matching
// correct on servers that run in UTC or serve several timezones.
func (m *blockMatcher) agencyLocation(ctx context.Context, agencyID string, lookups *blockMatchLookups) (*time.Location, error) {
	if location, ok := lookups.agencyZones[agencyID]; ok {
		return location, nil
	}
	agency, err := m.queries.GetAgency(ctx, agencyID)
	if err != nil {
		return nil, fmt.Errorf("agency %q: %w", agencyID, err)
	}
	location, err := time.LoadLocation(agency.Timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone for agency %q: %w", agencyID, err)
	}
	lookups.agencyZones[agencyID] = location
	return location, nil
}

// candidateServiceDates returns the legacy fixed window of service dates to
// try, in order, for a reference time already in the agency's zone.
func candidateServiceDates(reference time.Time) []time.Time {
	today := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, reference.Location())
	switch hour := reference.Hour(); {
	case hour < blockMatchEarlyCutHour:
		return []time.Time{today.AddDate(0, 0, -1), today}
	case hour >= blockMatchLateCutHour:
		return []time.Time{today, today.AddDate(0, 0, 1)}
	default:
		return []time.Time{today}
	}
}

// firstActiveServiceDate returns the first candidate date on which trip's
// service runs. Legacy looked for an active block instance containing the
// trip, which is the same as the trip's own service being active, and
// accepted the first date whose adjusted block start was positive. For an
// ordinary trip that start reduces to the trip's own first departure, so
// only a block starting at 00:00:00 failed it, as a side effect of a "> 0"
// check meant for a -1 error value; Maglev accepts that block.
func (m *blockMatcher) firstActiveServiceDate(ctx context.Context, trip gtfsdb.Trip, reference time.Time, lookups *blockMatchLookups) (time.Time, bool, error) {
	for _, serviceDate := range candidateServiceDates(reference) {
		serviceIDs, err := lookups.serviceIDs.forDate(ctx, m.queries, serviceDate)
		if err != nil {
			return time.Time{}, false, err
		}
		if slices.Contains(serviceIDs, trip.ServiceID) {
			return serviceDate, true, nil
		}
	}
	return time.Time{}, false, nil
}

// firstDepartures returns each trip's first static departure; trips
// without stop times are left out.
func (m *blockMatcher) firstDepartures(ctx context.Context, tripIDs []string) (map[string]time.Duration, error) {
	rows, err := utils.QueryInBatches(ctx, tripIDs, m.queries.GetFirstDeparturesForTripIDs)
	if err != nil {
		return nil, err
	}
	departures := make(map[string]time.Duration, len(rows))
	for _, row := range rows {
		departures[row.TripID] = time.Duration(row.DepartureTime)
	}
	return departures, nil
}
