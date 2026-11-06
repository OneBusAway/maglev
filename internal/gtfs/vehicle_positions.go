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
	vehicle    gtfs.Vehicle
	reportedAt time.Time
	feedRank   int
	dataTime   time.Time
}

// buildVehiclePositionsByAgency assembles every agency's export view from
// the per-feed vehicles and their ingestion-time agencies. feedIDs must be
// sorted. Caller must hold realTimeMutex.
func (manager *Manager) buildVehiclePositionsByAgency(feedIDs []string, builtAt time.Time) map[string]AgencyVehiclePositions {
	feedRanks := manager.feedConfigRanks(feedIDs)
	newestByAgency := make(map[string]map[string]exportCandidate)
	for _, feedID := range feedIDs {
		for _, vehicle := range manager.feedVehicles[feedID] {
			if !hasExportablePosition(vehicle) {
				continue
			}
			key := newVehicleKey(vehicle.ID)
			agencyID, attributed := manager.feedVehicleAgency[feedID][key]
			reportedAt := vehicleReportTime(vehicle, manager.feedVehicleLastSeen[feedID][key])
			if !attributed || !isFreshVehicleReport(reportedAt, builtAt) {
				continue
			}
			candidate := exportCandidate{
				vehicle:    vehicle,
				reportedAt: reportedAt,
				feedRank:   feedRanks[feedID],
				dataTime:   manager.feedVehicleDataTime[feedID],
			}
			keepNewestCandidate(newestByAgency, agencyID, candidate)
		}
	}

	positionsByAgency := make(map[string]AgencyVehiclePositions, len(newestByAgency))
	for agencyID, candidates := range newestByAgency {
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

// keepNewestCandidate files the candidate under its agency and vehicle ID
// unless a newer report of the same vehicle is already there. On equal
// report times the earlier configured feed wins.
func keepNewestCandidate(newestByAgency map[string]map[string]exportCandidate, agencyID string, candidate exportCandidate) {
	candidates, ok := newestByAgency[agencyID]
	if !ok {
		candidates = make(map[string]exportCandidate)
		newestByAgency[agencyID] = candidates
	}
	vehicleID := candidate.vehicle.ID.ID
	current, exists := candidates[vehicleID]
	isPreferred := !exists ||
		candidate.reportedAt.After(current.reportedAt) ||
		(candidate.reportedAt.Equal(current.reportedAt) && candidate.feedRank < current.feedRank)
	if isPreferred {
		candidates[vehicleID] = candidate
	}
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
