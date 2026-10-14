package gtfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"maglev.onebusaway.org/gtfsdb"
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

// blockMatchLookups memoizes the static lookups that are the same for every
// trip one refresh matches: active services per date and agency zones.
type blockMatchLookups struct {
	serviceIDs  serviceIDsByDate
	agencyZones map[string]*time.Location
}

func newBlockMatchLookups() *blockMatchLookups {
	return &blockMatchLookups{serviceIDs: serviceIDsByDate{}, agencyZones: make(map[string]*time.Location)}
}

// Match returns the block for tripID as seen by feedID's source, reusing a
// cached result for 30 minutes after it was first stored. A nil match means
// the trip is unresolved; an error means a query failed and nothing was
// cached. lookups is shared by the trips matched in one refresh.
func (m *blockMatcher) Match(ctx context.Context, feedID, tripID string, reference time.Time, lookups *blockMatchLookups) (*BlockMatch, error) {
	entry, generation, ok := m.cached(feedID, tripID)
	if ok {
		return entry.match, nil
	}
	match, err := m.resolve(ctx, tripID, reference, lookups)
	if err != nil {
		return nil, err
	}
	m.store(feedID, tripID, match, generation)
	return match, nil
}

// Clear drops every cached association; static GTFS replacement calls it.
func (m *blockMatcher) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caches = make(map[string]map[string]blockMatchEntry)
	m.generation++
}

// cached returns the live entry for tripID, if any, and the cache
// generation to pass to store with a newly resolved match.
func (m *blockMatcher) cached(feedID, tripID string) (blockMatchEntry, uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.caches[feedID][tripID]
	if !ok || m.now().Sub(entry.insertedAt) >= blockMatchCacheTTL {
		return blockMatchEntry{}, m.generation, false
	}
	return entry, m.generation, true
}

// store caches match unless the cache was cleared after generation was
// read, since the match may then come from replaced static data.
func (m *blockMatcher) store(feedID, tripID string, match *BlockMatch, generation uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation {
		return
	}
	if m.caches[feedID] == nil {
		m.caches[feedID] = make(map[string]blockMatchEntry)
	}
	m.caches[feedID][tripID] = blockMatchEntry{match: match, insertedAt: m.now()}
}

func (m *blockMatcher) resolve(ctx context.Context, tripID string, reference time.Time, lookups *blockMatchLookups) (*BlockMatch, error) {
	trips, err := m.queries.GetTripsByIDs(ctx, []string{tripID})
	if err != nil || len(trips) == 0 {
		return nil, err
	}
	trip := trips[0]
	route, err := m.queries.GetRoute(ctx, trip.RouteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("route %q for trip %q: %w", trip.RouteID, trip.ID, err)
	}
	agencyZone, err := m.agencyLocation(ctx, route.AgencyID, lookups)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tripStart, hasDeparture, err := m.firstDeparture(ctx, trip.ID)
	if err != nil || !hasDeparture {
		return nil, err
	}
	serviceDate, found, err := m.firstActiveServiceDate(ctx, trip, reference.In(agencyZone), lookups)
	if err != nil || !found {
		return nil, err
	}
	return &BlockMatch{
		TripID:      trip.ID,
		RouteID:     trip.RouteID,
		AgencyID:    route.AgencyID,
		BlockID:     tripBlockKey(trip),
		ServiceDate: serviceDate,
		TripStart:   tripStart,
	}, nil
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

// firstDeparture returns the trip's first static departure, if it has stop
// times.
func (m *blockMatcher) firstDeparture(ctx context.Context, tripID string) (time.Duration, bool, error) {
	rows, err := m.queries.GetFirstDeparturesForTripIDs(ctx, []string{tripID})
	if err != nil || len(rows) == 0 {
		return 0, false, err
	}
	return time.Duration(rows[0].DepartureTime), true, nil
}
