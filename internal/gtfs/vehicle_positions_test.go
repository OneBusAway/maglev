package gtfs

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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
	delete(manager.feedVehicleReportSeen, feedID)
	manager.recordVehicleReportsSeen(feedID, vehicles, nil, lastSeen)
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
		{"sole future-dated report", []gtfs.Vehicle{exportVehicle("V1", now.Add(24*time.Hour))}, now, []string{"V1"}},
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
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	older, newer := now.Add(-time.Minute), now
	future := now.Add(24 * time.Hour)
	skewLimit := now.Add(15 * time.Minute)

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
		{"a future report in the first feed loses", []string{"feed-a", "feed-b"}, future, newer, 2},
		{"a future report in the second feed loses", []string{"feed-a", "feed-b"}, newer, future, 1},
		{"a report at the skew limit can win", []string{"feed-a", "feed-b"}, newer, skewLimit, 2},
		{"a report past the skew limit loses", []string{"feed-a", "feed-b"}, newer, skewLimit.Add(time.Second), 1},
		{"last seen is used for a report without a timestamp", []string{"feed-a", "feed-b"}, future, time.Time{}, 2},
		{"newer report wins when both are beyond the skew limit", []string{"feed-a", "feed-b"}, future, future.Add(time.Minute), 2},
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

			positions := manager.buildVehiclePositionsByAgency([]string{"feed-a", "feed-b"}, now)["agency-A"]
			require.Len(t, positions.Vehicles, 1)
			assert.InDelta(t, tt.wantFeedLat, *positions.Vehicles[0].Position.Latitude, 0.001)
		})
	}
}

func TestVehiclePositionsForAgency_DuplicateVehicleAfterRebuild(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	manager := newTestManager()
	current := exportVehicle("V1", now)
	*current.Position.Latitude = 1
	future := exportVehicle("V1", now.Add(15*time.Minute+time.Second))
	*future.Position.Latitude = 2
	futureFeedTime := now.Add(2 * time.Minute)
	seedAgencyFeed(manager, "feed-a", "agency-A", now, now, current)
	seedAgencyFeed(manager, "feed-b", "agency-A", futureFeedTime, now, future)

	for _, tt := range []struct {
		name      string
		builtAt   time.Time
		wantLat   float32
		wantStamp time.Time
	}{
		{"first export", now, 1, now},
		{"rebuild from the same reports", now.Add(time.Second), 1, now},
		{"plausible report ages out", now.Add(10 * time.Minute), 2, futureFeedTime},
	} {
		t.Run(tt.name, func(t *testing.T) {
			positions := manager.buildVehiclePositionsByAgency([]string{"feed-a", "feed-b"}, tt.builtAt)["agency-A"]
			require.Len(t, positions.Vehicles, 1)
			assert.InDelta(t, tt.wantLat, *positions.Vehicles[0].Position.Latitude, 0.001)
			assert.Equal(t, tt.wantStamp, positions.Timestamp)
		})
	}
}

func TestVehiclePositionsForAgency_DuplicateAfterPoll(t *testing.T) {
	for _, kind := range []string{"repeated", "older correction"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			observedAt := now.Add(-2 * time.Minute)
			futureAt := now.Add(14 * time.Minute)
			manager := newTestManager()
			current := exportVehicle("V1", now)
			*current.Position.Latitude = 1
			future := exportVehicle("V1", futureAt)
			*future.Position.Latitude = 2
			seedAgencyFeed(manager, "feed-a", "agency-A", now, now, current)
			seedAgencyFeed(manager, "feed-b", "agency-A", now, observedAt, future)
			firstSeen := manager.feedVehicleReportSeen["feed-b"][vehicleKey{id: "V1"}]
			rebuild(manager)
			require.InDelta(t, 1, *manager.VehiclePositionsForAgency("agency-A").Vehicles[0].Position.Latitude, 0.001)

			incomingAt := futureAt
			if kind == "older correction" {
				incomingAt = now
			}
			var payloadMu sync.Mutex
			payload := encodeVehicleFeed(now, []*gtfsrt.VehiclePosition{{
				Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("V1")},
				Position:  &gtfsrt.Position{Latitude: proto.Float32(2), Longitude: proto.Float32(-122.3)},
				Timestamp: proto.Uint64(uint64(incomingAt.Unix())),
			}})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				payloadMu.Lock()
				defer payloadMu.Unlock()
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)
			require.True(t, manager.updateFeedRealtime(t.Context(), RTFeedConfig{
				ID: "feed-b", VehiclePositionsURL: server.URL, AgencyIDs: []string{"agency-A"},
			}))
			positions := manager.VehiclePositionsForAgency("agency-A")
			require.Len(t, positions.Vehicles, 1)
			assert.InDelta(t, 1, *positions.Vehicles[0].Position.Latitude, 0.001)
			assert.Equal(t, now, positions.Timestamp)
			assert.Equal(t, firstSeen, manager.feedVehicleReportSeen["feed-b"][vehicleKey{id: "V1"}])
			assert.True(t, manager.feedVehicleLastSeen["feed-b"][vehicleKey{id: "V1"}].After(observedAt))

			newerAt := futureAt.Add(time.Second)
			payloadMu.Lock()
			payload = encodeVehicleFeed(now.Add(time.Second), []*gtfsrt.VehiclePosition{{
				Vehicle:   &gtfsrt.VehicleDescriptor{Id: proto.String("V1")},
				Position:  &gtfsrt.Position{Latitude: proto.Float32(2), Longitude: proto.Float32(-122.3)},
				Timestamp: proto.Uint64(uint64(newerAt.Unix())),
			}})
			payloadMu.Unlock()
			require.True(t, manager.updateFeedRealtime(t.Context(), RTFeedConfig{
				ID: "feed-b", VehiclePositionsURL: server.URL, AgencyIDs: []string{"agency-A"},
			}))
			assert.True(t, manager.feedVehicleReportSeen["feed-b"][vehicleKey{id: "V1"}].After(firstSeen))
			positions = manager.VehiclePositionsForAgency("agency-A")
			require.Len(t, positions.Vehicles, 1)
			assert.Equal(t, float32(2), *positions.Vehicles[0].Position.Latitude)
		})
	}
}

func TestVehiclePositionsForAgency_UnknownReceiptTime(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	manager := newTestManager()
	seedAgencyFeed(manager, "feed-a", "agency-A", now, time.Time{}, exportVehicle("V1", now))
	future := exportVehicle("V1", now.Add(15*time.Minute+time.Second))
	*future.Position.Latitude = 2
	seedAgencyFeed(manager, "feed-b", "agency-A", now, time.Time{}, future)
	for _, builtAt := range []time.Time{now, now.Add(time.Second)} {
		positions := manager.buildVehiclePositionsByAgency([]string{"feed-a", "feed-b"}, builtAt)["agency-A"]
		require.Len(t, positions.Vehicles, 1)
		assert.Equal(t, float32(2), *positions.Vehicles[0].Position.Latitude)
	}
}

func TestVehiclePositionsForAgency_TimestampTransitions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	futureAt := now.Add(14 * time.Minute)
	for _, tt := range []struct {
		name     string
		previous time.Time
		incoming time.Time
	}{
		{"timestamp appeared", time.Time{}, futureAt},
		{"timestamp disappeared", futureAt, time.Time{}},
		{"timestamp still absent", time.Time{}, time.Time{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := newTestManager()
			current := exportVehicle("V1", now.Add(-time.Minute))
			*current.Position.Latitude = 1
			seedAgencyFeed(manager, "feed-a", "agency-A", now, now, current)
			seedAgencyFeed(manager, "feed-b", "agency-A", now, now.Add(-2*time.Minute), exportVehicle("V1", tt.previous))
			position := &gtfsrt.VehiclePosition{
				Vehicle:  &gtfsrt.VehicleDescriptor{Id: proto.String("V1")},
				Position: &gtfsrt.Position{Latitude: proto.Float32(2), Longitude: proto.Float32(-122.3)},
			}
			if !tt.incoming.IsZero() {
				position.Timestamp = proto.Uint64(uint64(tt.incoming.Unix()))
			}
			payload := encodeVehicleFeed(now, []*gtfsrt.VehiclePosition{position})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)
			require.True(t, manager.updateFeedRealtime(t.Context(), RTFeedConfig{
				ID: "feed-b", VehiclePositionsURL: server.URL, AgencyIDs: []string{"agency-A"},
			}))
			positions := manager.VehiclePositionsForAgency("agency-A")
			require.Len(t, positions.Vehicles, 1)
			assert.Equal(t, float32(2), *positions.Vehicles[0].Position.Latitude)
			assert.Equal(t, manager.feedVehicleLastSeen["feed-b"][vehicleKey{id: "V1"}],
				manager.feedVehicleReportSeen["feed-b"][vehicleKey{id: "V1"}])
		})
	}
}

func TestVehiclePositionsForAgency_PublishedDuplicateSelection(t *testing.T) {
	now := time.Now()
	manager := newTestManager()
	current := exportVehicle("V1", now)
	*current.Position.Latitude = 1
	future := exportVehicle("V1", now.Add(24*time.Hour))
	*future.Position.Latitude = 2
	futureFeedTime := now.Add(-time.Minute)
	seedAgencyFeed(manager, "feed-a", "agency-A", now, now, current)
	seedAgencyFeed(manager, "feed-b", "agency-A", futureFeedTime, now, future)
	for _, step := range []struct {
		name      string
		age       time.Duration
		wantLat   float32
		wantStamp time.Time
	}{
		{"first publish", 0, 1, now},
		{"same reports republished", 0, 1, now},
		{"plausible report expired", 11 * time.Minute, 2, futureFeedTime},
	} {
		t.Run(step.name, func(t *testing.T) {
			measuredAt := now.Add(-step.age)
			current.Timestamp = &measuredAt
			seedAgencyFeed(manager, "feed-a", "agency-A", now, now, current)
			rebuild(manager)
			positions := manager.VehiclePositionsForAgency("agency-A")
			require.Len(t, positions.Vehicles, 1)
			assert.Equal(t, step.wantLat, *positions.Vehicles[0].Position.Latitude)
			assert.Equal(t, step.wantStamp, positions.Timestamp)
		})
	}
}

func TestVehicleReportSeenCleanup(t *testing.T) {
	for _, action := range []string{"clear feed", "expire vehicle", "expire on successful poll"} {
		t.Run(action, func(t *testing.T) {
			now := time.Now()
			manager := newTestManager()
			seedAgencyFeed(manager, "feed", "agency", now, now.Add(-20*time.Minute), exportVehicle("V1", now))
			switch action {
			case "clear feed":
				manager.clearFeedData("feed")
			case "expire vehicle":
				manager.cleanupExpiredVehicles("feed")
			case "expire on successful poll":
				payload := encodeVehicleFeed(now, nil)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write(payload)
				}))
				t.Cleanup(server.Close)
				require.True(t, manager.updateFeedRealtime(t.Context(), RTFeedConfig{
					ID: "feed", VehiclePositionsURL: server.URL, AgencyIDs: []string{"agency"},
				}))
			}
			assert.Empty(t, manager.feedVehicleReportSeen["feed"])
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
