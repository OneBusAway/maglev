package gtfs

import (
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportVehicle builds a vehicle with a position and the given report time.
// A zero reportedAt leaves the timestamp unset.
func exportVehicle(vehicleID string, reportedAt time.Time) gtfs.Vehicle {
	latitude, longitude := float32(47.6), float32(-122.3)
	vehicle := gtfs.Vehicle{
		ID:       &gtfs.VehicleID{ID: vehicleID},
		Position: &gtfs.Position{Latitude: &latitude, Longitude: &longitude},
	}
	if !reportedAt.IsZero() {
		vehicle.Timestamp = &reportedAt
	}
	return vehicle
}

// seedAgencyFeed stores vehicles for a feed, all attributed to agencyID and
// last seen at lastSeen, with the feed's data time set to dataTime. The
// caller rebuilds the snapshot.
func seedAgencyFeed(manager *Manager, feedID, agencyID string, dataTime, lastSeen time.Time, vehicles ...gtfs.Vehicle) {
	manager.feedVehicles[feedID] = vehicles
	if manager.feedVehicleAgency == nil {
		manager.feedVehicleAgency = make(map[string]map[vehicleKey]string)
	}
	if manager.feedVehicleDataTime == nil {
		manager.feedVehicleDataTime = make(map[string]time.Time)
	}
	agencies := make(map[vehicleKey]string, len(vehicles))
	seen := make(map[vehicleKey]time.Time, len(vehicles))
	for _, vehicle := range vehicles {
		agencies[newVehicleKey(vehicle.ID)] = agencyID
		seen[newVehicleKey(vehicle.ID)] = lastSeen
	}
	manager.feedVehicleAgency[feedID] = agencies
	manager.feedVehicleLastSeen[feedID] = seen
	manager.feedVehicleDataTime[feedID] = dataTime
}

func rebuild(manager *Manager) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()
	manager.rebuildMergedRealtimeLocked()
}

func exportedIDs(positions AgencyVehiclePositions) []string {
	var ids []string
	for _, vehicle := range positions.Vehicles {
		ids = append(ids, vehicle.ID.ID)
	}
	return ids
}

func TestIsFreshVehicleReport(t *testing.T) {
	builtAt := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		reportedAt time.Time
		want       bool
	}{
		{"599 s old", builtAt.Add(-599 * time.Second), true},
		{"600 s old", builtAt.Add(-600 * time.Second), false},
		{"in the future", builtAt.Add(time.Minute), true},
		{"never reported", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isFreshVehicleReport(tt.reportedAt, builtAt))
		})
	}
}

func TestVehiclePositionsForAgency_Selection(t *testing.T) {
	now := time.Now()
	labelOnly := exportVehicle("", now)
	labelOnly.ID = &gtfs.VehicleID{Label: "bus-7"}
	noLongitude := exportVehicle("V5", now)
	noLongitude.Position.Longitude = nil

	tests := []struct {
		name     string
		vehicles []gtfs.Vehicle
		lastSeen time.Time
		want     []string
	}{
		{"fresh timestamp", []gtfs.Vehicle{exportVehicle("V1", now.Add(-5*time.Minute))}, now, []string{"V1"}},
		{"stale timestamp", []gtfs.Vehicle{exportVehicle("V1", now.Add(-11*time.Minute))}, now, nil},
		{"no timestamp, seen recently", []gtfs.Vehicle{exportVehicle("V1", time.Time{})}, now.Add(-10 * time.Second), []string{"V1"}},
		{"no timestamp, seen long ago", []gtfs.Vehicle{exportVehicle("V1", time.Time{})}, now.Add(-12 * time.Minute), nil},
		{"no position", []gtfs.Vehicle{noLongitude}, now, nil},
		{"no vehicle ID", []gtfs.Vehicle{labelOnly}, now, nil},
		{"sorted by ID", []gtfs.Vehicle{exportVehicle("V2", now), exportVehicle("V10", now), exportVehicle("V1", now)}, now, []string{"V1", "V10", "V2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManager()
			seedAgencyFeed(manager, "feed-a", "agency-A", now, tt.lastSeen, tt.vehicles...)
			rebuild(manager)

			assert.Equal(t, tt.want, exportedIDs(manager.VehiclePositionsForAgency("agency-A")))
		})
	}
}

func TestVehiclePositionsForAgency_DuplicateVehicle(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	older, newer := now.Add(-time.Minute), now

	tests := []struct {
		name        string
		feedOrder   []string
		feedATime   time.Time
		feedBTime   time.Time
		wantFeedLat float32
	}{
		{"newer report wins", []string{"feed-a", "feed-b"}, older, newer, 2},
		{"tie goes to the first configured feed", []string{"feed-b", "feed-a"}, newer, newer, 2},
		{"no timestamps, first configured feed", []string{"feed-a", "feed-b"}, time.Time{}, time.Time{}, 1},
		{"an older report from a later feed loses", []string{"feed-a", "feed-b"}, newer, older, 1},
		{"configured feeds rank before unconfigured ones", []string{"feed-b"}, newer, newer, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManager()
			for _, feedID := range tt.feedOrder {
				manager.config.RTFeeds = append(manager.config.RTFeeds, RTFeedConfig{ID: feedID, VehiclePositionsURL: "x", Enabled: true})
			}
			fromA := exportVehicle("V1", tt.feedATime)
			*fromA.Position.Latitude = 1
			fromB := exportVehicle("V1", tt.feedBTime)
			*fromB.Position.Latitude = 2
			seedAgencyFeed(manager, "feed-a", "agency-A", now, now, fromA)
			seedAgencyFeed(manager, "feed-b", "agency-A", now, now, fromB)
			rebuild(manager)

			positions := manager.VehiclePositionsForAgency("agency-A")
			require.Len(t, positions.Vehicles, 1)
			assert.InDelta(t, tt.wantFeedLat, *positions.Vehicles[0].Position.Latitude, 0.001)
		})
	}
}

func TestVehiclePositionsForAgency_Timestamp(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	t.Run("newest contributing feed", func(t *testing.T) {
		manager := newTestManager()
		seedAgencyFeed(manager, "feed-a", "agency-A", now.Add(-time.Minute), now, exportVehicle("V1", now))
		seedAgencyFeed(manager, "feed-b", "agency-A", now.Add(-30*time.Second), now, exportVehicle("V2", now))
		rebuild(manager)

		assert.Equal(t, now.Add(-30*time.Second), manager.VehiclePositionsForAgency("agency-A").Timestamp)
	})

	t.Run("a feed that contributes nothing is ignored", func(t *testing.T) {
		manager := newTestManager()
		seedAgencyFeed(manager, "feed-a", "agency-A", now.Add(-time.Minute), now, exportVehicle("V1", now))
		seedAgencyFeed(manager, "feed-b", "agency-A", now, now, exportVehicle("V2", now.Add(-time.Hour)))
		rebuild(manager)

		assert.Equal(t, now.Add(-time.Minute), manager.VehiclePositionsForAgency("agency-A").Timestamp)
	})

	t.Run("no fresh vehicles uses the build time", func(t *testing.T) {
		manager := newTestManager()
		seedAgencyFeed(manager, "feed-a", "agency-A", now.Add(-time.Hour), now, exportVehicle("V1", now.Add(-time.Hour)))
		before := time.Now()
		rebuild(manager)

		positions := manager.VehiclePositionsForAgency("agency-A")
		assert.Empty(t, positions.Vehicles)
		assert.False(t, positions.Timestamp.Before(before))
		assert.Equal(t, positions.Timestamp, manager.VehiclePositionsForAgency("nope").Timestamp)
	})

	t.Run("before the first snapshot", func(t *testing.T) {
		manager := newTestManager()
		before := time.Now()

		positions := manager.VehiclePositionsForAgency("agency-A")
		assert.Empty(t, positions.Vehicles)
		assert.False(t, positions.Timestamp.Before(before), "a server without realtime feeds still sends a current header time")
	})

	t.Run("a cloned snapshot keeps its build time", func(t *testing.T) {
		manager := newTestManager()
		rebuild(manager)
		builtAt := manager.VehiclePositionsForAgency("agency-A").Timestamp
		manager.MockAddVehicle("V9", "T9", "R9")

		assert.Equal(t, builtAt, manager.VehiclePositionsForAgency("agency-A").Timestamp)
	})

	t.Run("a feed without a data time uses the build time", func(t *testing.T) {
		manager := newTestManager()
		seedAgencyFeed(manager, "feed-a", "agency-A", time.Time{}, now, exportVehicle("V1", now))
		before := time.Now()
		rebuild(manager)

		assert.False(t, manager.VehiclePositionsForAgency("agency-A").Timestamp.Before(before))
	})
}

func TestVehicleDataTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	t.Run("feed header time", func(t *testing.T) {
		manager := newTestManager()
		manager.updateFeedRealtime(t.Context(), serveVehicleFeed(t, now.Add(-time.Minute), nil))

		assert.WithinDuration(t, now.Add(-time.Minute), manager.feedVehicleDataTime["test-feed"], 0)
	})

	t.Run("cleared with the feed", func(t *testing.T) {
		manager := newTestManager()
		manager.updateFeedRealtime(t.Context(), serveVehicleFeed(t, now, nil))
		manager.clearFeedData("test-feed")

		assert.NotContains(t, manager.feedVehicleDataTime, "test-feed")
	})
}

func TestRecordVehicleDataTime_NoHeaderTimeUsesFetchTime(t *testing.T) {
	manager := newTestManager()
	before := time.Now()

	manager.recordVehicleDataTime("feed-a", time.Time{})

	assert.False(t, manager.feedVehicleDataTime["feed-a"].Before(before))
}

func TestRecordVehicleDataTime_KeepsRealHeaderTime(t *testing.T) {
	manager := newTestManager()
	headerTime := time.Date(2025, 6, 8, 21, 10, 0, 0, time.UTC)

	manager.recordVehicleDataTime("feed-a", headerTime)

	assert.True(t, manager.feedVehicleDataTime["feed-a"].Equal(headerTime))
}
