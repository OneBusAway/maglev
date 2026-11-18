package gtfs

import (
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
)

// vehiclePositionsMaxAge is how old a vehicle's last report may be and still
// be exported. Java's vehicle-positions-for-agency uses the same window.
const vehiclePositionsMaxAge = 600 * time.Second

// AgencyVehiclePositions is one agency's vehicles for the GTFS-RT vehicle
// positions export, read from one snapshot so both fields agree.
type AgencyVehiclePositions struct {
	// Vehicles have a vehicle ID and a position, reported within
	// vehiclePositionsMaxAge of the snapshot, one per vehicle ID, sorted by ID.
	Vehicles []gtfs.Vehicle
	// Timestamp is the newest data time of the feeds that contributed a
	// vehicle, or the snapshot's build time when none did.
	Timestamp time.Time
}

// VehiclePositionsForAgency returns the current snapshot's vehicle positions
// for an agency. The vehicles are shared with every other reader and must
// not be modified.
func (manager *Manager) VehiclePositionsForAgency(agencyID string) AgencyVehiclePositions {
	merged := manager.mergedRealtime()
	if positions, ok := merged.vehiclePositionsByAgency[agencyID]; ok {
		return positions
	}
	builtAt := merged.builtAt
	// No snapshot is built on a server without realtime feeds.
	if builtAt.IsZero() {
		builtAt = time.Now()
	}
	return AgencyVehiclePositions{Timestamp: builtAt}
}

// exportCandidate is a vehicle with what the export needs to choose between
// two reports of the same vehicle and to date the feed.
type exportCandidate struct {
	vehicle           gtfs.Vehicle
	reportedAt        time.Time
	feedRank          int
	dataTime          time.Time
	exceedsFutureSkew bool
}

// buildVehiclePositionsByAgency assembles every agency's export view from
// the per-feed vehicles and their ingestion-time agencies. feedIDs must be
// sorted. Caller must hold realTimeMutex.
func (manager *Manager) buildVehiclePositionsByAgency(feedIDs []string, builtAt time.Time) map[string]AgencyVehiclePositions {
	feedRanks := manager.feedConfigRanks(feedIDs)
	preferredByAgency := make(map[string]map[string]exportCandidate)
	for _, feedID := range feedIDs {
		for _, vehicle := range manager.feedVehicles[feedID] {
			if !hasExportablePosition(vehicle) {
				continue
			}
			key := newVehicleKey(vehicle.ID)
			agencyID, attributed := manager.feedVehicleAgency[feedID][key]
			lastSeen := manager.feedVehicleLastSeen[feedID][key]
			reportedAt := vehicleReportTime(vehicle, lastSeen)
			if !attributed || !isFreshVehicleReport(reportedAt, builtAt) {
				continue
			}
			// An unrelated snapshot rebuild should not change the skew
			// classification of a report already received.
			skewReference := lastSeen
			if skewReference.IsZero() {
				skewReference = builtAt
			}
			candidate := exportCandidate{
				vehicle:           vehicle,
				reportedAt:        reportedAt,
				feedRank:          feedRanks[feedID],
				dataTime:          manager.feedVehicleDataTime[feedID],
				exceedsFutureSkew: reportedAt.Sub(skewReference) > maxVehicleFeedFutureSkew,
			}
			keepPreferredCandidate(preferredByAgency, agencyID, candidate)
		}
	}

	positionsByAgency := make(map[string]AgencyVehiclePositions, len(preferredByAgency))
	for agencyID, candidates := range preferredByAgency {
		positionsByAgency[agencyID] = assembleAgencyVehiclePositions(candidates, builtAt)
	}
	return positionsByAgency
}

// feedConfigRanks orders feeds for the exports' duplicate tie-breaks: the
// configured feed order first, then feeds missing from the configuration
// (such as test feeds) in feed ID order.
func (manager *Manager) feedConfigRanks(sortedFeedIDs []string) map[string]int {
	ranks := make(map[string]int, len(sortedFeedIDs))
	configuredFeeds := manager.config.enabledFeeds()
	for i, feed := range configuredFeeds {
		ranks[feed.ID] = i
	}
	for i, feedID := range sortedFeedIDs {
		if _, configured := ranks[feedID]; !configured {
			ranks[feedID] = len(configuredFeeds) + i
		}
	}
	return ranks
}

// feedIDsInConfigOrder returns the feeds in their feedConfigRanks order.
func (manager *Manager) feedIDsInConfigOrder(sortedFeedIDs []string) []string {
	feedRanks := manager.feedConfigRanks(sortedFeedIDs)
	ordered := slices.Clone(sortedFeedIDs)
	slices.SortFunc(ordered, func(a, b string) int {
		return feedRanks[a] - feedRanks[b]
	})
	return ordered
}

func hasExportablePosition(vehicle gtfs.Vehicle) bool {
	hasVehicleID := vehicle.ID != nil && vehicle.ID.ID != ""
	hasLocation := vehicle.Position != nil &&
		vehicle.Position.Latitude != nil &&
		vehicle.Position.Longitude != nil
	return hasVehicleID && hasLocation
}

// vehicleReportTime is the vehicle's own timestamp, else the time Maglev
// last saw it in a feed.
func vehicleReportTime(vehicle gtfs.Vehicle, lastSeen time.Time) time.Time {
	if vehicle.Timestamp != nil {
		return *vehicle.Timestamp
	}
	return lastSeen
}

func isFreshVehicleReport(reportedAt, builtAt time.Time) bool {
	return !reportedAt.IsZero() && builtAt.Sub(reportedAt) < vehiclePositionsMaxAge
}

func keepPreferredCandidate(preferredByAgency map[string]map[string]exportCandidate, agencyID string, candidate exportCandidate) {
	candidates, ok := preferredByAgency[agencyID]
	if !ok {
		candidates = make(map[string]exportCandidate)
		preferredByAgency[agencyID] = candidates
	}
	vehicleID := candidate.vehicle.ID.ID
	current, exists := candidates[vehicleID]
	if !exists || candidate.preferredTo(current) {
		candidates[vehicleID] = candidate
	}
}

// preferredTo applies the skew limit only when choosing between reports.
// When both reports fall on the same side of the limit, the newest report
// wins, with ties going to the earlier configured feed.
func (candidate exportCandidate) preferredTo(current exportCandidate) bool {
	if candidate.exceedsFutureSkew != current.exceedsFutureSkew {
		return !candidate.exceedsFutureSkew
	}
	return candidate.reportedAt.After(current.reportedAt) ||
		(candidate.reportedAt.Equal(current.reportedAt) && candidate.feedRank < current.feedRank)
}

func assembleAgencyVehiclePositions(candidates map[string]exportCandidate, builtAt time.Time) AgencyVehiclePositions {
	positions := AgencyVehiclePositions{Vehicles: make([]gtfs.Vehicle, 0, len(candidates))}
	for _, candidate := range candidates {
		positions.Vehicles = append(positions.Vehicles, candidate.vehicle)
		if candidate.dataTime.After(positions.Timestamp) {
			positions.Timestamp = candidate.dataTime
		}
	}
	slices.SortFunc(positions.Vehicles, func(a, b gtfs.Vehicle) int {
		return strings.Compare(a.ID.ID, b.ID.ID)
	})
	if positions.Timestamp.IsZero() {
		positions.Timestamp = builtAt
	}
	return positions
}
