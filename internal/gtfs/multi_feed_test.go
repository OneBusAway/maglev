package gtfs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiFeedDataMerging verifies that rebuildMergedRealtimeLocked correctly
// concatenates vehicles from distinct feeds and that per-feed lookup maps work.
// Feed A uses RABA vehicle positions; feed B uses Unitrans vehicle positions so
// that each feed contributes genuinely different vehicle IDs.
func TestMultiFeedDataMerging(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/feed-a/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/feed-b/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "unitrans-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	manager := newTestManager()

	feedA := RTFeedConfig{
		ID:                  "feed-a",
		VehiclePositionsURL: server.URL + "/feed-a/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}
	feedB := RTFeedConfig{
		ID:                  "feed-b",
		VehiclePositionsURL: server.URL + "/feed-b/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}

	ctx := t.Context()
	manager.updateFeedRealtime(ctx, feedA)
	manager.updateFeedRealtime(ctx, feedB)

	feedAVehicles := manager.feedVehicles["feed-a"]
	feedBVehicles := manager.feedVehicles["feed-b"]
	require.NotEmpty(t, feedAVehicles, "Feed A should have vehicles")
	require.NotEmpty(t, feedBVehicles, "Feed B should have vehicles")

	// Merged view must contain vehicles from both feeds.
	vehicles := manager.GetRealTimeVehicles()
	assert.Equal(t, len(feedAVehicles)+len(feedBVehicles), len(vehicles),
		"merged vehicles should equal sum of per-feed vehicles")

	// Vehicles from each feed must be independently reachable through the
	// lookup map, confirming data isolation rather than overwriting.
	manager.realTimeMutex.RLock()
	defer manager.realTimeMutex.RUnlock()
	for _, v := range feedAVehicles {
		if v.ID == nil || v.ID.ID == "" {
			continue
		}
		_, found := manager.realTimeVehicleLookupByVehicle[v.ID.ID]
		assert.True(t, found, "feed-A vehicle %q should be in the merged lookup", v.ID.ID)
	}
	for _, v := range feedBVehicles {
		if v.ID == nil || v.ID.ID == "" {
			continue
		}
		_, found := manager.realTimeVehicleLookupByVehicle[v.ID.ID]
		assert.True(t, found, "feed-B vehicle %q should be in the merged lookup", v.ID.ID)
	}
}

func TestStaleVehicleExpiry(t *testing.T) {
	realServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer realServer.Close()

	manager := newTestManager()
	feed := RTFeedConfig{
		ID:                  "expiry-test",
		VehiclePositionsURL: realServer.URL,
		RefreshInterval:     30,
		Enabled:             true,
	}

	ctx := t.Context()

	// First poll: seed vehicles from RABA feed via the production updateFeedRealtime path.
	manager.updateFeedRealtime(ctx, feed)
	initialVehicles := manager.GetRealTimeVehicles()
	require.NotEmpty(t, initialVehicles, "first poll should seed vehicles from RABA")
	initialCount := len(initialVehicles)

	// Wind last-seen back to 5 minutes ago so vehicles appear to have disappeared
	// but are still within the 15-min retention window.
	manager.realTimeMutex.Lock()
	for vid := range manager.feedVehicleLastSeen["expiry-test"] {
		manager.feedVehicleLastSeen["expiry-test"][vid] = time.Now().Add(-5 * time.Minute)
	}
	manager.realTimeMutex.Unlock()

	// Call cleanupExpiredVehicles directly to verify the 5-minute vehicles are preserved
	manager.realTimeMutex.Lock()
	manager.cleanupExpiredVehicles("expiry-test")
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	afterCleanup := manager.GetRealTimeVehicles()
	assert.Equal(t, initialCount, len(afterCleanup),
		"vehicles should be retained when last-seen is within 15-min window")

	// Wind last-seen back to 20 minutes ago (beyond the 15-min window).
	manager.realTimeMutex.Lock()
	for vid := range manager.feedVehicleLastSeen["expiry-test"] {
		manager.feedVehicleLastSeen["expiry-test"][vid] = time.Now().Add(-20 * time.Minute)
	}
	manager.realTimeMutex.Unlock()

	// Call cleanupExpiredVehicles again — stale vehicles beyond the 15-min window
	// should now be evicted.
	manager.realTimeMutex.Lock()
	manager.cleanupExpiredVehicles("expiry-test")
	manager.rebuildMergedRealtimeLocked()
	manager.realTimeMutex.Unlock()

	afterExpiry := manager.GetRealTimeVehicles()
	assert.Empty(t, afterExpiry,
		"vehicles should be expired when last-seen exceeds 15-min window")
}

// TestFeedIsolation verifies that updating one feed does not affect another
// feed's data in the merged view.
func TestFeedIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	manager := newTestManager()
	ctx := t.Context()

	// Load feed-B with real vehicle data
	feedB := RTFeedConfig{
		ID:                  "feed-b",
		VehiclePositionsURL: server.URL,
		RefreshInterval:     30,
		Enabled:             true,
	}
	manager.updateFeedRealtime(ctx, feedB)

	feedBCount := len(manager.feedVehicles["feed-b"])
	require.Positive(t, feedBCount, "Feed B should have vehicles loaded")

	// Now update feed-A with a failing URL (no vehicles)
	feedA := RTFeedConfig{
		ID:                  "feed-a",
		VehiclePositionsURL: "http://127.0.0.1:1/nonexistent",
		RefreshInterval:     30,
		Enabled:             true,
	}
	manager.updateFeedRealtime(ctx, feedA)

	// Feed-B data should be untouched in the merged view
	vehicles := manager.GetRealTimeVehicles()
	assert.Equal(t, feedBCount, len(vehicles),
		"Merged vehicles should still contain all feed-B vehicles after feed-A update failure")

	// Verify feed-B sub-map is unchanged
	assert.Equal(t, feedBCount, len(manager.feedVehicles["feed-b"]),
		"Feed-B sub-map should be unaffected by feed-A update")
}

// TestConcurrentFeedUpdates verifies data isolation when two feeds update
// simultaneously. Both goroutines race to write their per-feed sub-maps and
// trigger rebuildMergedRealtimeLocked; the final merged view must contain
// vehicles from both feeds with no data corruption.
func TestConcurrentFeedUpdates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/feed-a/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/feed-b/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "unitrans-vehicle-positions.pb"))
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	manager := newTestManager()
	feedA := RTFeedConfig{
		ID:                  "feed-a",
		VehiclePositionsURL: server.URL + "/feed-a/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}
	feedB := RTFeedConfig{
		ID:                  "feed-b",
		VehiclePositionsURL: server.URL + "/feed-b/vehicle-positions",
		RefreshInterval:     30,
		Enabled:             true,
	}

	ctx := t.Context()

	// Run several rounds of concurrent updates to increase the chance of
	// exposing races. The -race detector will catch any unsynchronised access.
	const rounds = 5
	for i := 0; i < rounds; i++ {
		done := make(chan struct{}, 2)
		go func() { manager.updateFeedRealtime(ctx, feedA); done <- struct{}{} }()
		go func() { manager.updateFeedRealtime(ctx, feedB); done <- struct{}{} }()
		<-done
		<-done
	}

	// After all goroutines have finished, both per-feed sub-maps must be
	// populated and the merged view must be non-empty. The -race detector
	// validates that no unsynchronised access occurred during the updates.
	assert.NotEmpty(t, manager.feedVehicles["feed-a"], "feed-a should have vehicles after concurrent updates")
	assert.NotEmpty(t, manager.feedVehicles["feed-b"], "feed-b should have vehicles after concurrent updates")
	assert.NotEmpty(t, manager.GetRealTimeVehicles(), "merged view should be non-empty after concurrent updates")
}
