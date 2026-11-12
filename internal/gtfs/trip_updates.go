package gtfs

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// ServiceDateLayout is the GTFS YYYYMMDD service date format.
const ServiceDateLayout = "20060102"

// AgencyTripUpdates is one agency's trip updates for the GTFS-RT trip
// updates export, read from one snapshot so its fields agree.
type AgencyTripUpdates struct {
	// TripUpdates have a trip_id, one per entity ID, sorted by entity ID.
	TripUpdates []TripUpdate
	// Timestamp is the newest data time of the feeds that contributed a trip
	// update, or the snapshot's build time when none did.
	Timestamp time.Time
	encoded   *encodedTripUpdates
}

// TripUpdate is an ingested trip update with its export entity ID.
type TripUpdate struct {
	EntityID string
	Trip     gtfs.Trip
	// Timestamp is when the trip's source feed produced its data.
	Timestamp time.Time
}

// encodedTripUpdates holds one serialization of an agency's trip updates,
// shared by every reader of the same snapshot.
type encodedTripUpdates struct {
	once sync.Once
	body []byte
	err  error
}

// TripUpdatesForAgency returns the current snapshot's trip updates for an
// agency. The trip updates are shared with every other reader and must not
// be modified.
func (manager *Manager) TripUpdatesForAgency(agencyID string) AgencyTripUpdates {
	merged := manager.mergedRealtime()
	if updates, ok := merged.tripUpdatesByAgency[agencyID]; ok {
		return updates
	}
	builtAt := merged.builtAt
	// No snapshot is built on a server without realtime feeds.
	if builtAt.IsZero() {
		builtAt = time.Now()
	}
	return AgencyTripUpdates{Timestamp: builtAt}
}

// Encoded returns encode's serialization of these trip updates. Copies read
// from the same snapshot share one result, so encode runs at most once per
// snapshot and agency. The bytes must not be modified.
func (updates AgencyTripUpdates) Encoded(encode func(AgencyTripUpdates) ([]byte, error)) ([]byte, error) {
	if updates.encoded == nil {
		return encode(updates)
	}
	updates.encoded.once.Do(func() {
		updates.encoded.body, updates.encoded.err = encode(updates)
	})
	return updates.encoded.body, updates.encoded.err
}

// TripUpdateEntityID is a trip update's export entity ID: the trip ID, then
// the start date and the start time when set, so it changes only with the
// trip's identity.
func TripUpdateEntityID(trip gtfs.TripID) string {
	parts := []string{trip.ID}
	if trip.HasStartDate {
		parts = append(parts, trip.StartDate.Format(ServiceDateLayout))
	}
	if trip.HasStartTime {
		parts = append(parts, FormatServiceTime(trip.StartTime))
	}
	return strings.Join(parts, "_")
}

// FormatServiceTime renders a duration since the service day's start as
// GTFS HH:MM:SS, allowing hours past 24.
func FormatServiceTime(sinceServiceDayStart time.Duration) string {
	seconds := int64(sinceServiceDayStart / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
}

// buildTripUpdatesByAgency assembles every agency's trip updates from the
// per-feed trips and their ingestion-time agencies. Feeds are read in
// configuration order, so when two feeds publish the same entity ID for an
// agency the one listed first wins, as does the first copy within one feed.
// feedIDs must be sorted. Caller must hold realTimeMutex.
func (manager *Manager) buildTripUpdatesByAgency(feedIDs []string, builtAt time.Time) map[string]AgencyTripUpdates {
	collector := tripUpdateCollector{
		updatesByAgency: make(map[string][]TripUpdate),
		seen:            make(map[agencyEntityKey]bool),
	}
	for _, feedID := range manager.feedIDsInConfigOrder(feedIDs) {
		agencies := manager.feedTripAgency[feedID]
		dataTime := manager.feedTripDataTime[feedID]
		for _, trip := range slices.Concat(manager.feedTrips[feedID], manager.feedUnattributedTrips[feedID]) {
			collector.add(trip, agencies[trip.ID.ID], dataTime)
		}
	}

	updatesByAgency := make(map[string]AgencyTripUpdates, len(collector.updatesByAgency))
	for agencyID, updates := range collector.updatesByAgency {
		updatesByAgency[agencyID] = assembleAgencyTripUpdates(updates, builtAt)
	}
	return updatesByAgency
}

// tripUpdateCollector files trip updates by agency, keeping the first one
// with each entity ID.
type tripUpdateCollector struct {
	updatesByAgency map[string][]TripUpdate
	seen            map[agencyEntityKey]bool
}

// add files a trip under its agency. Trips go-gtfs made up from vehicle
// entities aren't trip updates, and trips without a trip_id or an agency
// can't be exported.
func (collector *tripUpdateCollector) add(trip gtfs.Trip, agencyID string, dataTime time.Time) {
	if !trip.IsEntityInMessage || trip.ID.ID == "" || agencyID == "" {
		return
	}
	key := agencyEntityKey{agencyID: agencyID, entityID: TripUpdateEntityID(trip.ID)}
	if collector.seen[key] {
		return
	}
	collector.seen[key] = true
	collector.updatesByAgency[agencyID] = append(collector.updatesByAgency[agencyID], TripUpdate{
		EntityID:  key.entityID,
		Trip:      trip,
		Timestamp: dataTime,
	})
}

func assembleAgencyTripUpdates(updates []TripUpdate, builtAt time.Time) AgencyTripUpdates {
	slices.SortFunc(updates, func(a, b TripUpdate) int {
		return strings.Compare(a.EntityID, b.EntityID)
	})
	agencyUpdates := AgencyTripUpdates{TripUpdates: updates, encoded: &encodedTripUpdates{}}
	for _, update := range updates {
		if update.Timestamp.After(agencyUpdates.Timestamp) {
			agencyUpdates.Timestamp = update.Timestamp
		}
	}
	if agencyUpdates.Timestamp.IsZero() {
		agencyUpdates.Timestamp = builtAt
	}
	return agencyUpdates
}
