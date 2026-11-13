package gtfs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTripUpdatesTestManager returns a manager whose configuration lists the
// given feed IDs in order.
func newTripUpdatesTestManager(feedIDs ...string) *Manager {
	manager := newTestManager()
	for _, feedID := range feedIDs {
		manager.config.RTFeeds = append(manager.config.RTFeeds, RTFeedConfig{ID: feedID, TripUpdatesURL: "x", Enabled: true})
	}
	return manager
}

// setFeedTrips stores a feed's trips with the agency of each trip ID and
// the feed's data time, then rebuilds the snapshot.
func setFeedTrips(manager *Manager, feedID string, dataTime time.Time, agencies map[string]string, trips ...gtfs.Trip) {
	manager.realTimeMutex.Lock()
	defer manager.realTimeMutex.Unlock()
	manager.storeFeedTrips(feedID, tripAttribution{kept: trips, agencies: agencies}, dataTime)
	manager.rebuildMergedRealtimeLocked()
}

func entityIDs(updates AgencyTripUpdates) []string {
	ids := make([]string, 0, len(updates.TripUpdates))
	for _, update := range updates.TripUpdates {
		ids = append(ids, update.EntityID)
	}
	return ids
}

func TestTripUpdateEntityID(t *testing.T) {
	serviceDate := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		trip gtfs.TripID
		want string
	}{
		{name: "trip only", trip: gtfs.TripID{ID: "T1"}, want: "T1"},
		{name: "start date", trip: gtfs.TripID{ID: "T1", HasStartDate: true, StartDate: serviceDate}, want: "T1_20261015"},
		{
			name: "start date and time",
			trip: gtfs.TripID{ID: "T1", HasStartDate: true, StartDate: serviceDate, HasStartTime: true, StartTime: 22*time.Hour + 30*time.Minute},
			want: "T1_20261015_22:30:00",
		},
		{
			name: "start time past midnight",
			trip: gtfs.TripID{ID: "T1", HasStartDate: true, StartDate: serviceDate, HasStartTime: true, StartTime: 25*time.Hour + 10*time.Minute},
			want: "T1_20261015_25:10:00",
		},
		{name: "start time without date", trip: gtfs.TripID{ID: "T1", HasStartTime: true, StartTime: 8 * time.Hour}, want: "T1_08:00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TripUpdateEntityID(tt.trip))
		})
	}
}

func TestTripUpdatesForAgency_EntitySet(t *testing.T) {
	dataTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	day1 := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	t.Run("sorted by entity ID", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		setFeedTrips(manager, "feed", dataTime, map[string]string{"B": "A", "A": "A"}, tripOnRoute("B", "R1"), tripOnRoute("A", "R1"))

		assert.Equal(t, []string{"A", "B"}, entityIDs(manager.TripUpdatesForAgency("A")))
	})

	t.Run("one trip on two service days is two entities", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		first, second := tripOnRoute("T1", "R1"), tripOnRoute("T1", "R1")
		first.ID.HasStartDate, first.ID.StartDate = true, day1
		second.ID.HasStartDate, second.ID.StartDate = true, day2
		setFeedTrips(manager, "feed", dataTime, map[string]string{"T1": "A"}, first, second)

		assert.Equal(t, []string{"T1_20261015", "T1_20261016"}, entityIDs(manager.TripUpdatesForAgency("A")))
	})

	t.Run("the same ID twice in one feed keeps the first", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		setFeedTrips(manager, "feed", dataTime, map[string]string{"T1": "A"}, tripOnRoute("T1", "R1"), tripOnRoute("T1", "R2"))

		updates := manager.TripUpdatesForAgency("A").TripUpdates
		require.Len(t, updates, 1)
		assert.Equal(t, "R1", updates[0].Trip.ID.RouteID)
	})

	t.Run("the same ID in two feeds keeps the feed listed first", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed-b", "feed-a")
		setFeedTrips(manager, "feed-a", dataTime, map[string]string{"T1": "A"}, tripOnRoute("T1", "from-a"))
		setFeedTrips(manager, "feed-b", dataTime, map[string]string{"T1": "A"}, tripOnRoute("T1", "from-b"))

		updates := manager.TripUpdatesForAgency("A").TripUpdates
		require.Len(t, updates, 1)
		assert.Equal(t, "from-b", updates[0].Trip.ID.RouteID)
	})

	t.Run("the same ID for two agencies is in both feeds", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed-a", "feed-b")
		setFeedTrips(manager, "feed-a", dataTime, map[string]string{"T1": "A"}, tripOnRoute("T1", "R1"))
		setFeedTrips(manager, "feed-b", dataTime, map[string]string{"T1": "B"}, tripOnRoute("T1", "R9"))

		assert.Equal(t, []string{"T1"}, entityIDs(manager.TripUpdatesForAgency("A")))
		assert.Equal(t, []string{"T1"}, entityIDs(manager.TripUpdatesForAgency("B")))
	})

	t.Run("trips that aren't exportable trip updates are left out", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		vehicleOnly := tripOnRoute("T2", "R1")
		vehicleOnly.IsEntityInMessage = false
		setFeedTrips(manager, "feed", dataTime, map[string]string{"T1": "A", "T2": "A", "": "A"},
			tripOnRoute("T1", "R1"), vehicleOnly, tripOnRoute("", "R1"), tripOnRoute("T3", "R1"))

		assert.Equal(t, []string{"T1"}, entityIDs(manager.TripUpdatesForAgency("A")),
			"T2 is made up from a vehicle entity, the third has no trip_id, T3 has no agency")
	})

	t.Run("unresolved trips of a filtered feed are filed under its first agency", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		manager.realTimeMutex.Lock()
		manager.storeFeedTrips("feed", tripAttribution{
			kept:         []gtfs.Trip{tripOnRoute("T1", "R1")},
			unattributed: []gtfs.Trip{tripOnRoute("T7", "X")},
			agencies:     map[string]string{"T1": "A", "T7": "A"},
		}, dataTime)
		manager.rebuildMergedRealtimeLocked()
		manager.realTimeMutex.Unlock()

		assert.Equal(t, []string{"T1", "T7"}, entityIDs(manager.TripUpdatesForAgency("A")))
	})
}

func TestTripUpdatesForAgency_Timestamps(t *testing.T) {
	older := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	newer := older.Add(time.Minute)

	t.Run("each trip update is dated by its feed, the header by the newest feed", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed-a", "feed-b")
		setFeedTrips(manager, "feed-a", older, map[string]string{"T1": "A"}, tripOnRoute("T1", "R1"))
		setFeedTrips(manager, "feed-b", newer, map[string]string{"T2": "A"}, tripOnRoute("T2", "R1"))

		updates := manager.TripUpdatesForAgency("A")
		require.Len(t, updates.TripUpdates, 2)
		assert.True(t, older.Equal(updates.TripUpdates[0].Timestamp))
		assert.True(t, newer.Equal(updates.TripUpdates[1].Timestamp))
		assert.True(t, newer.Equal(updates.Timestamp))
	})

	t.Run("an agency with no trip updates is dated at the snapshot", func(t *testing.T) {
		manager := newTripUpdatesTestManager("feed")
		setFeedTrips(manager, "feed", older, map[string]string{"T1": "A"}, tripOnRoute("T1", "R1"))

		updates := manager.TripUpdatesForAgency("nope")
		assert.Empty(t, updates.TripUpdates)
		assert.True(t, manager.mergedRealtime().builtAt.Equal(updates.Timestamp))
	})

	t.Run("a server without realtime feeds dates it now", func(t *testing.T) {
		before := time.Now()
		updates := newTestManager().TripUpdatesForAgency("A")
		assert.False(t, updates.Timestamp.Before(before))
	})
}

func TestAgencyTripUpdatesEncoded(t *testing.T) {
	dataTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	manager := newTripUpdatesTestManager("feed")
	setFeedTrips(manager, "feed", dataTime, map[string]string{"T1": "A"}, tripOnRoute("T1", "R1"))
	var encodes atomic.Int32
	encode := func(updates AgencyTripUpdates) ([]byte, error) {
		encodes.Add(1)
		return []byte(entityIDs(updates)[0]), nil
	}

	t.Run("encoded once per snapshot, even concurrently", func(t *testing.T) {
		var wg sync.WaitGroup
		bodies := make([][]byte, 8)
		for i := range bodies {
			wg.Add(1)
			go func() {
				defer wg.Done()
				body, err := manager.TripUpdatesForAgency("A").Encoded(encode)
				assert.NoError(t, err)
				bodies[i] = body
			}()
		}
		wg.Wait()

		assert.Equal(t, int32(1), encodes.Load())
		for _, body := range bodies {
			assert.Equal(t, []byte("T1"), body)
		}
	})

	t.Run("a new snapshot encodes again", func(t *testing.T) {
		setFeedTrips(manager, "feed", dataTime, map[string]string{"T2": "A"}, tripOnRoute("T2", "R1"))

		body, err := manager.TripUpdatesForAgency("A").Encoded(encode)
		require.NoError(t, err)
		assert.Equal(t, []byte("T2"), body)
		assert.Equal(t, int32(2), encodes.Load())
	})
}
