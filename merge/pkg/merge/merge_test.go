package merge

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/merge/pkg/merge/scorers"
)

func TestMerge_SingleFeed(t *testing.T) {
	// Single feed should return as-is
	feed := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Stop 1"},
			},
		},
		Index:  0,
		Source: "test.zip",
	}

	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed})

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, NONE, result.Strategy)
	assert.Equal(t, 1, len(result.Merged.Agencies))
	assert.Equal(t, 1, len(result.Merged.Stops))
}

func TestMerge_TwoFeeds_NoDuplicates(t *testing.T) {
	lat1, lon1 := 40.0, -74.0
	lat2, lon2 := 40.1, -74.1

	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Agency 1"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Stop 1", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency2", Name: "Agency 2"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop2", Name: "Stop 2", Latitude: &lat2, Longitude: &lon2},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.NotNil(t, result)

	// Should have both agencies
	assert.Equal(t, 2, len(result.Merged.Agencies))

	// Should have both stops
	assert.Equal(t, 2, len(result.Merged.Stops))

	// No duplicates detected
	assert.Equal(t, 0, result.DuplicatesA)
}

func TestMerge_WithDuplicates(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"},
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{
				{Id: "agency1", Name: "Test Agency"}, // Duplicate
			},
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St"}, // Duplicate
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 1, len(result.Merged.Agencies))
	assert.Equal(t, 1, len(result.Merged.Stops))
	assert.Equal(t, 2, result.DuplicatesA) // 2 duplicates found
}

func TestMerge_WithRoutesAndTrips(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{
				{Id: "route1", ShortName: "R1"},
			},
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip1", Headsign: "Downtown"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{
				{Id: "route1", ShortName: "R1"}, // Same ID, will be renamed
			},
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip1", Headsign: "Uptown"}, // Same ID, will be renamed
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = IDENTITY

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Merged.Routes))
	assert.Equal(t, 2, len(result.Merged.Trips))
	assert.Equal(t, 2, result.RenamingsA) // 2 renamings (route + trip)
}

func TestMerge_NoFeeds(t *testing.T) {
	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "no feeds provided")
}

func TestMerge_WithServices(t *testing.T) {
	feed1 := &Feed{
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{Id: "weekday"},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{Id: "weekend"},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	assert.Equal(t, 2, len(result.Merged.Services))
}

func TestRenameID(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		feedIndex  int
		renameMode RenameMode
		expected   string
	}{
		{
			name:       "Context mode, first feed",
			id:         "stop1",
			feedIndex:  0,
			renameMode: CONTEXT,
			expected:   "a-stop1",
		},
		{
			name:       "Context mode, second feed",
			id:         "stop1",
			feedIndex:  1,
			renameMode: CONTEXT,
			expected:   "b-stop1",
		},
		{
			name:       "Agency mode fallback",
			id:         "route1",
			feedIndex:  0,
			renameMode: AGENCY,
			expected:   "a-route1", // Falls back to CONTEXT
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.RenameMode = tt.renameMode

			merger := NewMerger(opts)
			result := merger.renameID(tt.id, tt.feedIndex)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRegisterScorer(t *testing.T) {
	merger := NewMerger(DefaultOptions())

	// Register a custom scorer
	scorer := &StopNameScorer{}
	merger.RegisterScorer("stop", scorer)

	// Verify it was registered
	assert.NotNil(t, merger.scorers["stop"])
}

type StopNameScorer struct{}

func (s *StopNameScorer) Score(a, b interface{}) float64 {
	return 1.0
}

func TestMerge_FuzzyStrategy_Stops(t *testing.T) {
	// Feed1: stop id="stop1", name="Main St", close location
	// Feed2: stop id="different-id", name="Main St", very close location (~10m away)
	// With FUZZY strategy, should detect as duplicate despite different IDs

	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 40.7590, -73.9851 // ~11m away

	feed1 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "different-id", Name: "Main St", Latitude: &lat2, Longitude: &lon2},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = FUZZY
	opts.Threshold = 0.5

	merger := NewMerger(opts)
	merger.RegisterScorer("stop", scorers.NewCompositeStopScorer())

	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	// Should have 1 stop (duplicate detected via fuzzy matching)
	assert.Equal(t, 1, len(result.Merged.Stops))
	// Should have 1 duplicate recorded
	assert.Equal(t, 1, result.DuplicatesA)
	assert.Equal(t, FUZZY, result.Strategy)
}

func TestMerge_FuzzyStrategy_NoMatch(t *testing.T) {
	// Stops with different names and far apart - should not match

	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 41.0, -74.0 // Far away

	feed1 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "stop2", Name: "Oak Ave", Latitude: &lat2, Longitude: &lon2},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = FUZZY
	opts.Threshold = 0.5

	merger := NewMerger(opts)
	merger.RegisterScorer("stop", scorers.NewCompositeStopScorer())

	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	// Should have 2 stops (no match)
	assert.Equal(t, 2, len(result.Merged.Stops))
	// No duplicates found
	assert.Equal(t, 0, result.DuplicatesA)
}

func TestMerge_FuzzyStrategy_NoScorer(t *testing.T) {
	// Test behavior when FUZZY used but no scorer registered
	lat1, lon1 := 40.7589, -73.9851

	feed1 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "stop1", Name: "Main St", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{
				{Id: "stop2", Name: "Main St", Latitude: &lat1, Longitude: &lon1},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = FUZZY
	// Don't register a scorer

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})

	require.NoError(t, err)
	// Without scorer, FUZZY strategy can't find duplicates, should keep both
	assert.Equal(t, 2, len(result.Merged.Stops))
	assert.Equal(t, 0, result.DuplicatesA)
}

func TestMerge_ReferenceUpdating_FuzzyDuplicate(t *testing.T) {
	// End-to-end test: FUZZY merge with reference updating
	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := lat1+0.00001, lon1 // ~1m away

	stop1 := &gtfs.Stop{Id: "stop1", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stop2 := &gtfs.Stop{Id: "stopX", Name: "Main St", Latitude: &lat2, Longitude: &lon2}
	route1 := &gtfs.Route{Id: "route1", ShortName: "1"}

	feed1 := &Feed{
		Data: &gtfs.Static{
			Stops:  []gtfs.Stop{{Id: "stop1", Name: "Main St", Latitude: &lat1, Longitude: &lon1}},
			Routes: []gtfs.Route{{Id: "route1", ShortName: "1"}},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:    "trip1",
					Route: route1,
					StopTimes: []gtfs.ScheduledStopTime{
						{Stop: stop1},
					},
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{{Id: "stopX", Name: "Main St", Latitude: &lat2, Longitude: &lon2}},
			Trips: []gtfs.ScheduledTrip{
				{
					ID: "trip2",
					StopTimes: []gtfs.ScheduledStopTime{
						{Stop: stop2},
					},
				},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = FUZZY
	opts.Threshold = 0.5

	merger := NewMerger(opts)
	merger.RegisterScorer("stop", scorers.NewCompositeStopScorer())

	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Should have 1 stop (duplicate detected)
	assert.Equal(t, 1, len(result.Merged.Stops))

	// CRITICAL: All stop references should point to the merged stop (stopX from feed2)
	// trip1 should reference stopX (was stop1, got updated)
	assert.Equal(t, "stopX", result.Merged.Trips[0].StopTimes[0].Stop.Id,
		"trip1 should reference the merged stop stopX")

	// trip2 already references stopX, should still be correct
	assert.Equal(t, "stopX", result.Merged.Trips[1].StopTimes[0].Stop.Id,
		"trip2 should still reference stopX")
}

func TestMerge_ReferenceUpdating_IDCollision(t *testing.T) {
	// End-to-end test: ID collision with reference updating
	route1 := &gtfs.Route{Id: "route1", ShortName: "A"}
	route1B := &gtfs.Route{Id: "route1", ShortName: "B"}

	feed1 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{{Id: "route1", ShortName: "A"}},
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip1", Route: route1},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Routes: []gtfs.Route{{Id: "route1", ShortName: "B"}}, // Different route, same ID
			Trips: []gtfs.ScheduledTrip{
				{ID: "trip2", Route: route1B},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = NONE // No duplicate detection

	merger := NewMerger(opts)
	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Should have 2 routes (one renamed)
	assert.Equal(t, 2, len(result.Merged.Routes))

	// Find the renamed route (feed1's route gets renamed because feed2 is added first)
	var renamedRoute *gtfs.Route
	for i := range result.Merged.Routes {
		if result.Merged.Routes[i].Id[:2] == "a-" {
			renamedRoute = &result.Merged.Routes[i]
			break
		}
	}
	require.NotNil(t, renamedRoute, "Should have renamed one route with 'a-' prefix")

	// CRITICAL: Feeds are processed newest-first, so:
	// - feed2 added first with route1 (kept as-is)
	// - feed1 merged second, route1 collides → renamed to a-route1
	// Therefore:
	// - trip1 (from feed1) should reference a-route1 (the renamed route)
	// - trip2 (from feed2) should reference route1 (the original)

	// Find which trip is which
	var trip1Idx, trip2Idx int
	for i := range result.Merged.Trips {
		if result.Merged.Trips[i].ID == "trip1" {
			trip1Idx = i
		} else if result.Merged.Trips[i].ID == "trip2" {
			trip2Idx = i
		}
	}

	assert.Equal(t, renamedRoute.Id, result.Merged.Trips[trip1Idx].Route.Id,
		"trip1 should reference the renamed route")

	assert.Equal(t, "route1", result.Merged.Trips[trip2Idx].Route.Id,
		"trip2 should reference route1")
}

// TestMerge_ReferenceUpdating_ShapeIDCollision tests that shape references are updated when shape IDs collide
func TestMerge_ReferenceUpdating_ShapeIDCollision(t *testing.T) {
	// Create two feeds with shapes that have the same ID but are different shapes
	shape1Feed1 := gtfs.Shape{ID: "shape1"}
	shape1Feed2 := gtfs.Shape{ID: "shape1"}

	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{{Id: "route1", ShortName: "R1"}},
			Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop 1"}},
			Shapes:   []gtfs.Shape{shape1Feed1},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:    "trip1",
					Route: &gtfs.Route{Id: "route1"},
					Shape: &shape1Feed1,
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{{Id: "route1", ShortName: "R1"}},
			Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop 1"}},
			Shapes:   []gtfs.Shape{shape1Feed2},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:    "trip2",
					Route: &gtfs.Route{Id: "route1"},
					Shape: &shape1Feed2,
				},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	// Merge with IDENTITY strategy
	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Verify we have 2 shapes (one original, one renamed)
	assert.Equal(t, 2, len(result.Merged.Shapes))

	// Find the renamed shape - feed1's shape should be renamed since feed2 is processed first
	var renamedShape gtfs.Shape
	for _, shape := range result.Merged.Shapes {
		if shape.ID != "shape1" {
			renamedShape = shape
			break
		}
	}
	assert.NotEmpty(t, renamedShape.ID, "Should have a renamed shape")
	assert.Contains(t, renamedShape.ID, "shape1", "Renamed shape should contain original ID")

	// Find which trip is which
	var trip1, trip2 *gtfs.ScheduledTrip
	for i := range result.Merged.Trips {
		if result.Merged.Trips[i].ID == "trip1" {
			trip1 = &result.Merged.Trips[i]
		} else if result.Merged.Trips[i].ID == "trip2" {
			trip2 = &result.Merged.Trips[i]
		}
	}

	require.NotNil(t, trip1, "trip1 should exist")
	require.NotNil(t, trip2, "trip2 should exist")

	// Verify trip1 (from feed1) references the renamed shape
	require.NotNil(t, trip1.Shape, "trip1 should have a shape reference")
	assert.Equal(t, renamedShape.ID, trip1.Shape.ID,
		"trip1 should reference the renamed shape")

	// Verify trip2 (from feed2) references the original shape
	require.NotNil(t, trip2.Shape, "trip2 should have a shape reference")
	assert.Equal(t, "shape1", trip2.Shape.ID,
		"trip2 should reference shape1")
}
