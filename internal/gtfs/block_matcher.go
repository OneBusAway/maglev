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
}

func newBlockMatcher(queries *gtfsdb.Queries, now func() time.Time) *blockMatcher {
	return &blockMatcher{
		queries: queries,
		now:     now,
		caches:  make(map[string]map[string]blockMatchEntry),
	}
}

// Match returns the block for tripID as seen by feedID's source, reusing a
// cached result for 30 minutes after it was first stored. A nil match means
// the trip is unresolved; an error means a query failed and nothing was
// cached.
func (m *blockMatcher) Match(ctx context.Context, feedID, tripID string, reference time.Time) (*BlockMatch, error) {
	if entry, ok := m.cached(feedID, tripID); ok {
		return entry.match, nil
	}
	match, err := m.resolve(ctx, tripID, reference)
	if err != nil {
		return nil, err
	}
	m.store(feedID, tripID, match)
	return match, nil
}

// Clear drops every cached association; static GTFS replacement calls it.
func (m *blockMatcher) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.caches = make(map[string]map[string]blockMatchEntry)
}

func (m *blockMatcher) cached(feedID, tripID string) (blockMatchEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.caches[feedID][tripID]
	if !ok || m.now().Sub(entry.insertedAt) >= blockMatchCacheTTL {
		return blockMatchEntry{}, false
	}
	return entry, true
}

func (m *blockMatcher) store(feedID, tripID string, match *BlockMatch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.caches[feedID] == nil {
		m.caches[feedID] = make(map[string]blockMatchEntry)
	}
	m.caches[feedID][tripID] = blockMatchEntry{match: match, insertedAt: m.now()}
}

func (m *blockMatcher) resolve(ctx context.Context, tripID string, reference time.Time) (*BlockMatch, error) {
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
	agencyZone, err := m.agencyLocation(ctx, route.AgencyID)
	if err != nil {
		return nil, err
	}
	for _, serviceDate := range candidateServiceDates(reference.In(agencyZone)) {
		match, err := m.matchOnDate(ctx, trip, route.AgencyID, serviceDate)
		if err != nil || match != nil {
			return match, err
		}
	}
	return nil, nil
}

// agencyLocation is the zone candidate service dates are read in. Using the
// agency's timezone instead of the server's (as legacy did) keeps matching
// correct on servers that run in UTC or serve several timezones.
func (m *blockMatcher) agencyLocation(ctx context.Context, agencyID string) (*time.Location, error) {
	agency, err := m.queries.GetAgency(ctx, agencyID)
	if err != nil {
		return nil, fmt.Errorf("agency %q: %w", agencyID, err)
	}
	location, err := time.LoadLocation(agency.Timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone for agency %q: %w", agencyID, err)
	}
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

// matchOnDate checks one candidate date. The legacy adjusted block start is
// the block's first departure plus the trip start minus the matching
// block-trip's departure; for an ordinary trip both of those are the trip's
// own first static departure, so the start reduces to the block's first
// departure. Zero or negative never qualifies.
func (m *blockMatcher) matchOnDate(ctx context.Context, trip gtfsdb.Trip, agencyID string, serviceDate time.Time) (*BlockMatch, error) {
	serviceIDs, err := m.queries.GetActiveServiceIDsForDate(ctx, serviceDate.Format("20060102"))
	if err != nil {
		return nil, err
	}
	blockID, blockTripIDs, err := m.activeBlockTrips(ctx, trip, serviceIDs)
	if err != nil || !slices.Contains(blockTripIDs, trip.ID) {
		return nil, err
	}
	departures, err := m.firstDepartures(ctx, blockTripIDs)
	if err != nil {
		return nil, err
	}
	tripStart, ok := departures[trip.ID]
	if !ok || earliest(departures) <= 0 {
		return nil, nil
	}
	return &BlockMatch{
		TripID:      trip.ID,
		RouteID:     trip.RouteID,
		AgencyID:    agencyID,
		BlockID:     blockID,
		ServiceDate: serviceDate,
		TripStart:   tripStart,
	}, nil
}

// activeBlockTrips lists the trips of trip's block that run on a date with
// the given active services. A trip without a block is its own block.
func (m *blockMatcher) activeBlockTrips(ctx context.Context, trip gtfsdb.Trip, serviceIDs []string) (string, []string, error) {
	if !trip.BlockID.Valid || trip.BlockID.String == "" {
		if slices.Contains(serviceIDs, trip.ServiceID) {
			return trip.ID, []string{trip.ID}, nil
		}
		return trip.ID, nil, nil
	}
	if len(serviceIDs) == 0 {
		return trip.BlockID.String, nil, nil
	}
	rows, err := m.queries.GetTripsByBlockIDs(ctx, gtfsdb.GetTripsByBlockIDsParams{
		BlockIds:   []sql.NullString{trip.BlockID},
		ServiceIds: serviceIDs,
	})
	if err != nil {
		return "", nil, err
	}
	tripIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		tripIDs = append(tripIDs, row.ID)
	}
	return trip.BlockID.String, tripIDs, nil
}

func (m *blockMatcher) firstDepartures(ctx context.Context, tripIDs []string) (map[string]time.Duration, error) {
	rows, err := m.queries.GetFirstDeparturesForTripIDs(ctx, tripIDs)
	if err != nil {
		return nil, err
	}
	departures := make(map[string]time.Duration, len(rows))
	for _, row := range rows {
		departures[row.TripID] = time.Duration(row.DepartureTime)
	}
	return departures, nil
}

func earliest(departures map[string]time.Duration) time.Duration {
	first := time.Duration(-1)
	for _, departure := range departures {
		if first < 0 || departure < first {
			first = departure
		}
	}
	return first
}
