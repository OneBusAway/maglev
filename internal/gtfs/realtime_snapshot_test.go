package gtfs

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshotTestManager() *Manager {
	return &Manager{
		feedTrips:            make(map[string][]gtfs.Trip),
		feedVehicles:         make(map[string][]gtfs.Vehicle),
		feedAlerts:           make(map[string][]gtfs.Alert),
		feedLastUpdate:       make(map[string]time.Time),
		feedAgencyFilter:     make(map[string]map[string]bool),
		feedVehicleLastSeen:  make(map[string]map[string]time.Time),
		feedVehicleTimestamp: make(map[string]uint64),
	}
}

func seedSnapshotFeed(manager *Manager, feedID string, n int) {
	trips := make([]gtfs.Trip, 0, n)
	vehicles := make([]gtfs.Vehicle, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-trip-%d", feedID, i)
		trips = append(trips, gtfs.Trip{ID: gtfs.TripID{ID: id}})
		now := time.Now()
		vehicles = append(vehicles, gtfs.Vehicle{
			ID:        &gtfs.VehicleID{ID: fmt.Sprintf("%s-veh-%d", feedID, i)},
			Timestamp: &now,
			Trip:      &gtfs.Trip{ID: gtfs.TripID{ID: id}},
		})
	}
	manager.feedTrips[feedID] = trips
	manager.feedVehicles[feedID] = vehicles
}

// A published snapshot must never be mutated. A reader that loaded one before a
// rebuild has to keep seeing exactly what it loaded, otherwise dropping the
// reader-side RLock would be unsafe.
func TestMergedSnapshotIsImmutableAfterPublish(t *testing.T) {
	manager := snapshotTestManager()
	seedSnapshotFeed(manager, "feed-a", 3)
	manager.rebuildMergedRealtimeLocked()

	before := manager.mergedRealtime()
	require.Len(t, before.trips, 3)
	firstTripID := before.trips[0].ID.ID
	lookupLen := len(before.tripLookup)

	seedSnapshotFeed(manager, "feed-b", 5)
	manager.rebuildMergedRealtimeLocked()

	after := manager.mergedRealtime()
	assert.Len(t, after.trips, 8, "rebuild should publish the merged view of both feeds")

	assert.NotSame(t, before, after, "rebuild must publish a new snapshot, not mutate in place")
	assert.Len(t, before.trips, 3, "the previously loaded snapshot must not have grown")
	assert.Equal(t, firstTripID, before.trips[0].ID.ID)
	assert.Len(t, before.tripLookup, lookupLen)
}

// A zero-value Manager is constructed directly by several tests, so loading
// before anything has been published must be safe rather than a nil map panic.
func TestMergedRealtimeBeforeFirstPublish(t *testing.T) {
	manager := &Manager{}

	assert.Empty(t, manager.GetRealTimeTrips())
	assert.Empty(t, manager.GetRealTimeVehicles())
	assert.Empty(t, manager.GetAllTripUpdates())
	assert.Empty(t, manager.GetDuplicatedVehiclesForRoute("route-1"))
	assert.Empty(t, manager.GetAlertsForStop("stop-1"))
	assert.Empty(t, manager.GetTripUpdatesForTrip("trip-1"))

	_, err := manager.GetVehicleByID("veh-1")
	assert.Error(t, err)
}

// Readers must keep returning a coherent snapshot while a rebuild is running.
// Run with -race: before this change the readers held RLock, so the race
// detector was satisfied by the lock rather than by the data being immutable.
func TestConcurrentReadsDuringRebuild(t *testing.T) {
	manager := snapshotTestManager()
	seedSnapshotFeed(manager, "feed-a", 200)
	manager.rebuildMergedRealtimeLocked()

	var stop atomic.Bool
	var wg sync.WaitGroup

	// require calls FailNow, which Go only allows on the test goroutine, so the
	// workers report the first mismatch back instead of asserting in place.
	mismatch := make(chan error, 1)
	report := func(err error) {
		select {
		case mismatch <- err:
		default:
		}
	}

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				merged := manager.mergedRealtime()
				// Every index in the lookup must be valid for the same
				// snapshot's slice. A torn read would break this.
				for id, idx := range merged.tripLookup {
					if idx >= len(merged.trips) {
						report(fmt.Errorf("tripLookup[%s] = %d, out of range for %d trips", id, idx, len(merged.trips)))
						return
					}
					if got := merged.trips[idx].ID.ID; got != id {
						report(fmt.Errorf("tripLookup[%s] points at trip %q", id, got))
						return
					}
				}
			}
		}()
	}

	for i := 0; i < 40; i++ {
		manager.realTimeMutex.Lock()
		seedSnapshotFeed(manager, fmt.Sprintf("feed-%d", i), 50)
		manager.rebuildMergedRealtimeLocked()
		manager.realTimeMutex.Unlock()
	}

	stop.Store(true)
	wg.Wait()

	select {
	case err := <-mismatch:
		require.NoError(t, err)
	default:
	}
}

// BenchmarkRealtimeReadDuringRebuild measures a reader while rebuilds run in the
// background, which is the contention the change targets.
func BenchmarkRealtimeReadDuringRebuild(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("entities=%d", n), func(b *testing.B) {
			manager := snapshotTestManager()
			seedSnapshotFeed(manager, "feed-a", n)
			manager.rebuildMergedRealtimeLocked()

			var stop atomic.Bool
			done := make(chan struct{})
			go func() {
				defer close(done)
				for !stop.Load() {
					manager.realTimeMutex.Lock()
					manager.rebuildMergedRealtimeLocked()
					manager.realTimeMutex.Unlock()
				}
			}()

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = manager.GetRealTimeVehicles()
			}
			b.StopTimer()

			stop.Store(true)
			<-done
		})
	}
}

// The exported getters hand back snapshot-owned data. Under the read-only
// contract that is safe while rebuilds run; this pins it at the public API
// rather than only at mergedRealtime. Run with -race.
func TestConcurrentGetterReadsDuringRebuild(t *testing.T) {
	manager := snapshotTestManager()
	seedSnapshotFeed(manager, "feed-a", 200)
	manager.rebuildMergedRealtimeLocked()

	var stop atomic.Bool
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for _, tr := range manager.GetRealTimeTrips() {
					_ = tr.ID.ID
				}
				for _, v := range manager.GetRealTimeVehicles() {
					_ = v.ID
				}
				_ = manager.GetAlertsByIDs("trip-1", "route-1", "agency-1")
				_ = manager.GetDuplicatedVehiclesForRoute("route-1")
			}
		}()
	}

	for i := 0; i < 40; i++ {
		manager.realTimeMutex.Lock()
		seedSnapshotFeed(manager, fmt.Sprintf("feed-%d", i), 50)
		manager.rebuildMergedRealtimeLocked()
		manager.realTimeMutex.Unlock()
	}

	stop.Store(true)
	wg.Wait()
}
