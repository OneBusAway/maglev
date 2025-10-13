package merge

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestReferenceMap_RecordAndRetrieve(t *testing.T) {
	refMap := NewReferenceMap()

	refMap.RecordReplacement("stop", "stop1", "stop2")
	refMap.RecordReplacement("route", "route1", "a-route1")

	newID, ok := refMap.GetReplacement("stop", "stop1")
	assert.True(t, ok)
	assert.Equal(t, "stop2", newID)

	newID, ok = refMap.GetReplacement("route", "route1")
	assert.True(t, ok)
	assert.Equal(t, "a-route1", newID)

	_, ok = refMap.GetReplacement("stop", "nonexistent")
	assert.False(t, ok)
}

func TestReferenceMap_ChainedReplacements(t *testing.T) {
	// If A→B and B→C, looking up A should return C
	refMap := NewReferenceMap()

	refMap.RecordReplacement("stop", "A", "B")
	refMap.RecordReplacement("stop", "B", "C")

	// Should follow the chain
	newID, ok := refMap.GetReplacement("stop", "A")
	assert.True(t, ok)
	assert.Equal(t, "C", newID, "Should resolve chained replacements A→B→C to C")
}

func TestReferenceMap_MultipleChains(t *testing.T) {
	// Test multiple independent replacement chains
	refMap := NewReferenceMap()

	// Chain 1: A→B→C
	refMap.RecordReplacement("stop", "A", "B")
	refMap.RecordReplacement("stop", "B", "C")

	// Chain 2: X→Y
	refMap.RecordReplacement("stop", "X", "Y")

	// Chain 3: Different entity type
	refMap.RecordReplacement("route", "R1", "R2")

	// Verify chains
	newID, ok := refMap.GetReplacement("stop", "A")
	assert.True(t, ok)
	assert.Equal(t, "C", newID)

	newID, ok = refMap.GetReplacement("stop", "X")
	assert.True(t, ok)
	assert.Equal(t, "Y", newID)

	newID, ok = refMap.GetReplacement("route", "R1")
	assert.True(t, ok)
	assert.Equal(t, "R2", newID)
}

func TestReferenceMap_NoReplacement(t *testing.T) {
	refMap := NewReferenceMap()

	// Looking up non-existent replacement should return false
	_, ok := refMap.GetReplacement("stop", "stop1")
	assert.False(t, ok)
}

func TestReferenceMap_SameIDReplacement(t *testing.T) {
	// Edge case: replacing with same ID (shouldn't break)
	refMap := NewReferenceMap()

	refMap.RecordReplacement("stop", "stop1", "stop1")

	newID, ok := refMap.GetReplacement("stop", "stop1")
	assert.True(t, ok)
	assert.Equal(t, "stop1", newID)
}

func TestUpdateStopReferences_StopTimes(t *testing.T) {
	// Setup: Feed with stop times referencing old stop IDs
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}

	feed := &gtfs.Static{
		Stops: []gtfs.Stop{
			{Id: "stop2"}, // Merged stop (kept)
		},
		Trips: []gtfs.ScheduledTrip{
			{
				ID: "trip1",
				StopTimes: []gtfs.ScheduledStopTime{
					{Stop: stop1}, // References old ID
					{Stop: stop2}, // Already correct
				},
			},
		},
	}

	refMap := NewReferenceMap()
	refMap.RecordReplacement("stop", "stop1", "stop2")

	updater := NewReferenceUpdater(refMap)
	updater.UpdateStopReferences(feed)

	// Verify all stop references updated
	assert.Equal(t, "stop2", feed.Trips[0].StopTimes[0].Stop.Id)
	assert.Equal(t, "stop2", feed.Trips[0].StopTimes[1].Stop.Id)
}

func TestUpdateStopReferences_Transfers(t *testing.T) {
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop3 := &gtfs.Stop{Id: "stop3"}

	feed := &gtfs.Static{
		Transfers: []gtfs.Transfer{
			{
				From: stop1,
				To:   stop3,
			},
		},
	}

	refMap := NewReferenceMap()
	refMap.RecordReplacement("stop", "stop1", "stop2")
	refMap.RecordReplacement("stop", "stop3", "stop4")

	updater := NewReferenceUpdater(refMap)
	updater.UpdateStopReferences(feed)

	assert.Equal(t, "stop2", feed.Transfers[0].From.Id)
	assert.Equal(t, "stop4", feed.Transfers[0].To.Id)
}

func TestUpdateStopReferences_ParentStation(t *testing.T) {
	parent := &gtfs.Stop{Id: "station1"}

	feed := &gtfs.Static{
		Stops: []gtfs.Stop{
			{Id: "platform1", Parent: parent},
		},
	}

	refMap := NewReferenceMap()
	refMap.RecordReplacement("stop", "station1", "station2")

	updater := NewReferenceUpdater(refMap)
	updater.UpdateStopReferences(feed)

	assert.Equal(t, "station2", feed.Stops[0].Parent.Id)
}

func TestUpdateStopReferences_NilReferences(t *testing.T) {
	// Edge case: nil references shouldn't crash
	feed := &gtfs.Static{
		Stops: []gtfs.Stop{
			{Id: "stop1", Parent: nil}, // No parent
		},
		Trips: []gtfs.ScheduledTrip{
			{
				ID:        "trip1",
				StopTimes: []gtfs.ScheduledStopTime{}, // No stop times
			},
		},
		Transfers: []gtfs.Transfer{}, // No transfers
	}

	refMap := NewReferenceMap()
	refMap.RecordReplacement("stop", "stop1", "stop2")

	updater := NewReferenceUpdater(refMap)
	// Should not panic
	updater.UpdateStopReferences(feed)

	// Verify nothing broke
	assert.Equal(t, "stop1", feed.Stops[0].Id)
}

func TestUpdateTripReferences_UpdatesStopTimeTripReferences(t *testing.T) {
	// Test that StopTime.Trip references are updated when trips are renamed
	refMap := NewReferenceMap()
	refMap.RecordReplacement("trip", "trip1", "a-trip1")

	updater := NewReferenceUpdater(refMap)

	trip := gtfs.ScheduledTrip{
		ID: "a-trip1",
		StopTimes: []gtfs.ScheduledStopTime{
			{
				Trip: &gtfs.ScheduledTrip{ID: "trip1"}, // Old trip reference
			},
			{
				Trip: &gtfs.ScheduledTrip{ID: "trip1"}, // Old trip reference
			},
		},
	}

	feed := &gtfs.Static{
		Trips: []gtfs.ScheduledTrip{trip},
	}

	updater.UpdateTripReferences(feed)

	// Verify trip references in stop times were updated
	assert.Equal(t, "a-trip1", feed.Trips[0].StopTimes[0].Trip.ID, "First stop time trip reference should be updated")
	assert.Equal(t, "a-trip1", feed.Trips[0].StopTimes[1].Trip.ID, "Second stop time trip reference should be updated")
}
