package merge

import (
	"testing"
	"time"

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

// TestTripScorer_Registration tests that trip scorer can be registered and works correctly
func TestTripScorer_Registration(t *testing.T) {
	merger := NewMerger(DefaultOptions())
	merger.RegisterScorer("trip", &scorers.TripScorer{})

	// Verify scorer was registered
	assert.NotNil(t, merger.scorers["trip"])

	// Verify scorer works correctly
	route1 := &gtfs.Route{Id: "route1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	trip1 := &gtfs.ScheduledTrip{
		ID:          "trip1",
		Route:       route1,
		DirectionId: 0,
		StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
	}

	trip2 := &gtfs.ScheduledTrip{
		ID:          "trip2",
		Route:       route1,
		DirectionId: 0,
		StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
	}

	score := merger.scorers["trip"].Score(trip1, trip2)

	// Identical trips should score very high
	assert.Greater(t, score, 0.9, "Identical trips should score > 0.9")
}

// TestFindDuplicateTrip_Identity tests IDENTITY strategy for trip duplicate detection
func TestFindDuplicateTrip_Identity(t *testing.T) {
	route1 := &gtfs.Route{Id: "route1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	result := &gtfs.Static{
		Trips: []gtfs.ScheduledTrip{
			{
				ID:          "trip1",
				Route:       route1,
				DirectionId: 0,
				StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
			},
		},
	}

	// Same ID = duplicate
	tripSameID := &gtfs.ScheduledTrip{
		ID:          "trip1",
		Route:       route1,
		DirectionId: 0,
		StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
	}

	// Different ID = not duplicate
	tripDifferentID := &gtfs.ScheduledTrip{
		ID:          "trip2",
		Route:       route1,
		DirectionId: 0,
		StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
	}

	merger := NewMerger(DefaultOptions())

	duplicate := merger.findDuplicateTrip(result, tripSameID, IDENTITY)
	assert.NotNil(t, duplicate, "Same trip ID should be found as duplicate")
	assert.Equal(t, "trip1", duplicate.ID, "Should return the existing trip")

	notDuplicate := merger.findDuplicateTrip(result, tripDifferentID, IDENTITY)
	assert.Nil(t, notDuplicate, "Different trip ID should not be duplicate")
}

// TestFindDuplicateService_Identity tests IDENTITY strategy for service duplicate detection
func TestFindDuplicateService_Identity(t *testing.T) {
	result := &gtfs.Static{
		Services: []gtfs.Service{
			{
				Id:        "weekday",
				Monday:    true,
				Tuesday:   true,
				Wednesday: true,
				Thursday:  true,
				Friday:    true,
				Saturday:  false,
				Sunday:    false,
			},
		},
	}

	// Same ID = duplicate
	serviceSameID := &gtfs.Service{
		Id:        "weekday",
		Monday:    true,
		Tuesday:   true,
		Wednesday: true,
		Thursday:  true,
		Friday:    true,
		Saturday:  false,
		Sunday:    false,
	}

	// Different ID = not duplicate
	serviceDifferentID := &gtfs.Service{
		Id:        "weekend",
		Monday:    false,
		Tuesday:   false,
		Wednesday: false,
		Thursday:  false,
		Friday:    false,
		Saturday:  true,
		Sunday:    true,
	}

	merger := NewMerger(DefaultOptions())

	duplicate := merger.findDuplicateService(result, serviceSameID, IDENTITY)
	assert.NotNil(t, duplicate, "Same service ID should be found as duplicate")
	assert.Equal(t, "weekday", duplicate.Id, "Should return the existing service")

	notDuplicate := merger.findDuplicateService(result, serviceDifferentID, IDENTITY)
	assert.Nil(t, notDuplicate, "Different service ID should not be duplicate")
}

// TestMerge_ServiceDuplicates_Identity tests service duplicate detection with IDENTITY strategy
func TestMerge_ServiceDuplicates_Identity(t *testing.T) {
	// Two feeds with services that have same ID = duplicate with IDENTITY
	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Services: []gtfs.Service{
				{
					Id:        "weekday",
					Monday:    true,
					Tuesday:   true,
					Wednesday: true,
					Thursday:  true,
					Friday:    true,
					Saturday:  false,
					Sunday:    false,
					StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Services: []gtfs.Service{
				{
					Id:        "weekday",
					Monday:    true,
					Tuesday:   true,
					Wednesday: true,
					Thursday:  true,
					Friday:    true,
					Saturday:  false,
					Sunday:    false,
					StartDate: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	merger := NewMerger(DefaultOptions())
	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Should have 1 service (duplicate not added)
	assert.Equal(t, 1, len(result.Merged.Services), "Should have 1 service with IDENTITY")

	// Should have 2 duplicates (agency + service)
	assert.Equal(t, 2, result.DuplicatesA, "Should have 2 duplicates (agency + service)")
}

// TestMerge_ServiceDuplicates_Fuzzy tests service duplicate detection with FUZZY strategy
func TestMerge_ServiceDuplicates_Fuzzy(t *testing.T) {
	// Two feeds with services that have different IDs but identical patterns
	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Services: []gtfs.Service{
				{
					Id:        "weekday-h1",
					Monday:    true,
					Tuesday:   true,
					Wednesday: true,
					Thursday:  true,
					Friday:    true,
					Saturday:  false,
					Sunday:    false,
					StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Services: []gtfs.Service{
				{
					Id:        "weekday-h2", // Different ID
					Monday:    true,         // But identical pattern
					Tuesday:   true,
					Wednesday: true,
					Thursday:  true,
					Friday:    true,
					Saturday:  false,
					Sunday:    false,
					StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		Index:  1,
		Source: "feed2.zip",
	}

	opts := DefaultOptions()
	opts.Strategy = FUZZY
	opts.Threshold = 0.9
	merger := NewMerger(opts)
	merger.RegisterScorer("service", &scorers.ServiceScorer{})

	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Should have 1 service (duplicate not added with FUZZY)
	assert.Equal(t, 1, len(result.Merged.Services), "Should have 1 service with FUZZY")

	// Should have at least 1 duplicate (service, agencies processed with IDENTITY)
	assert.GreaterOrEqual(t, result.DuplicatesA, 1, "Should have at least 1 duplicate (service)")
}

// TestFindDuplicateService_Fuzzy tests FUZZY strategy for service duplicate detection
func TestFindDuplicateService_Fuzzy(t *testing.T) {
	result := &gtfs.Static{
		Services: []gtfs.Service{
			{
				Id:        "weekday1",
				Monday:    true,
				Tuesday:   true,
				Wednesday: true,
				Thursday:  true,
				Friday:    true,
				Saturday:  false,
				Sunday:    false,
				StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	// Different ID but identical properties = duplicate with FUZZY
	serviceSimilar := &gtfs.Service{
		Id:        "weekdayX", // Different ID
		Monday:    true,
		Tuesday:   true,
		Wednesday: true,
		Thursday:  true,
		Friday:    true,
		Saturday:  false,
		Sunday:    false,
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}

	opts := DefaultOptions()
	opts.Threshold = 0.9
	merger := NewMerger(opts)
	merger.RegisterScorer("service", &scorers.ServiceScorer{})

	duplicate := merger.findDuplicateService(result, serviceSimilar, FUZZY)
	assert.NotNil(t, duplicate, "Identical service with different ID should be found as duplicate with FUZZY")
	assert.Equal(t, "weekday1", duplicate.Id, "Should return the existing service")
}

// TestFindDuplicateTrip_Fuzzy tests FUZZY strategy for trip duplicate detection
func TestFindDuplicateTrip_Fuzzy(t *testing.T) {
	route1 := &gtfs.Route{Id: "route1"}
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}

	result := &gtfs.Static{
		Trips: []gtfs.ScheduledTrip{
			{
				ID:          "trip1",
				Route:       route1,
				DirectionId: 0,
				StopTimes: []gtfs.ScheduledStopTime{
					{Stop: stop1},
					{Stop: stop2},
				},
			},
		},
	}

	// Different ID but identical properties = duplicate with FUZZY
	tripSimilar := &gtfs.ScheduledTrip{
		ID:          "tripX", // Different ID
		Route:       route1,  // Same route
		DirectionId: 0,       // Same direction
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1}, // Same stops
			{Stop: stop2},
		},
	}

	opts := DefaultOptions()
	opts.Threshold = 0.9
	merger := NewMerger(opts)
	merger.RegisterScorer("trip", &scorers.TripScorer{})

	duplicate := merger.findDuplicateTrip(result, tripSimilar, FUZZY)
	assert.NotNil(t, duplicate, "Identical trip with different ID should be found as duplicate with FUZZY")
	assert.Equal(t, "trip1", duplicate.ID, "Should return the existing trip")
}

// TestFindDuplicateTrip_Fuzzy_NoScorer tests FUZZY without scorer registered
func TestFindDuplicateTrip_Fuzzy_NoScorer(t *testing.T) {
	route1 := &gtfs.Route{Id: "route1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	result := &gtfs.Static{
		Trips: []gtfs.ScheduledTrip{
			{
				ID:          "trip1",
				Route:       route1,
				DirectionId: 0,
				StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
			},
		},
	}

	trip := &gtfs.ScheduledTrip{
		ID:          "trip2",
		Route:       route1,
		DirectionId: 0,
		StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
	}

	merger := NewMerger(DefaultOptions())
	// No scorer registered

	duplicate := merger.findDuplicateTrip(result, trip, FUZZY)
	assert.Nil(t, duplicate, "Should return nil when no scorer is registered")
}

// TestMerge_TripDuplicates_Identity tests trip duplicate detection during merge with IDENTITY strategy
func TestMerge_TripDuplicates_Identity(t *testing.T) {
	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{*route1},
			Stops:    []gtfs.Stop{*stop1},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:          "trip1",
					Route:       route1,
					DirectionId: 0,
					StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{*route1},
			Stops:    []gtfs.Stop{*stop1},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:          "trip1", // Same ID = duplicate
					Route:       route1,
					DirectionId: 0,
					StopTimes:   []gtfs.ScheduledStopTime{{Stop: stop1}},
				},
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

	// Should detect trip as duplicate
	assert.Equal(t, 1, len(result.Merged.Trips), "Duplicate trip should not be added")
	// DuplicatesA counts ALL duplicates: 1 agency + 1 stop + 1 trip = 3
	assert.Equal(t, 3, result.DuplicatesA, "Feed1 should have 3 duplicates (agency, stop, trip)")
}

// TestMerge_TripDuplicates_Fuzzy tests trip duplicate detection during merge with FUZZY strategy
func TestMerge_TripDuplicates_Fuzzy(t *testing.T) {
	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}

	feed1 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{*route1},
			Stops:    []gtfs.Stop{*stop1, *stop2},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:          "trip1",
					Route:       route1,
					DirectionId: 0,
					StopTimes: []gtfs.ScheduledStopTime{
						{Stop: stop1},
						{Stop: stop2},
					},
				},
			},
		},
		Index:  0,
		Source: "feed1.zip",
	}

	feed2 := &Feed{
		Data: &gtfs.Static{
			Agencies: []gtfs.Agency{{Id: "agency1", Name: "Agency 1"}},
			Routes:   []gtfs.Route{*route1},
			Stops:    []gtfs.Stop{*stop1, *stop2},
			Trips: []gtfs.ScheduledTrip{
				{
					ID:          "tripX", // Different ID but identical trip
					Route:       route1,
					DirectionId: 0,
					StopTimes: []gtfs.ScheduledStopTime{
						{Stop: stop1},
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
	opts.Threshold = 0.9
	merger := NewMerger(opts)
	merger.RegisterScorer("trip", &scorers.TripScorer{})

	result, err := merger.Merge([]*Feed{feed1, feed2})
	require.NoError(t, err)

	// Should detect trip as duplicate with FUZZY
	assert.Equal(t, 1, len(result.Merged.Trips), "Duplicate trip should not be added with FUZZY")
	// DuplicatesA counts ALL duplicates: 1 agency + 1 stop (not scored) + 1 trip = at least 1 trip duplicate
	assert.GreaterOrEqual(t, result.DuplicatesA, 1, "Should have at least 1 duplicate")
}

func TestMerge_ServiceDuplicates_MergesCalendarDates(t *testing.T) {
	// Test that calendar exception dates are merged from duplicate services
	date1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	date3 := time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)

	feedA := &Feed{
		Index: 0,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:         "service1",
					Monday:     true,
					StartDate:  date1,
					EndDate:    date3,
					AddedDates: []time.Time{date1}, // Feed A has date1 added
				},
			},
		},
	}

	feedB := &Feed{
		Index: 1,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:         "service1", // Same ID - should be detected as duplicate
					Monday:     true,
					StartDate:  date1,
					EndDate:    date3,
					AddedDates: []time.Time{date2}, // Feed B has date2 added
				},
			},
		},
	}

	merger := NewMerger(DefaultOptions())
	merger.RegisterScorer("service", &scorers.ServiceScorer{})

	result, err := merger.Merge([]*Feed{feedB, feedA})
	require.NoError(t, err)

	// Should have one service (duplicate detected)
	assert.Equal(t, 1, len(result.Merged.Services), "Should have 1 service after merging duplicates")

	// Should have BOTH calendar dates merged
	service := result.Merged.Services[0]
	assert.Equal(t, 2, len(service.AddedDates), "Should merge AddedDates from both services")

	// Check both dates are present
	dateMap := make(map[time.Time]bool)
	for _, d := range service.AddedDates {
		dateMap[d] = true
	}
	assert.True(t, dateMap[date1], "Should have date1 from feed A")
	assert.True(t, dateMap[date2], "Should have date2 from feed B")
}

func TestMerge_ServiceDuplicates_MergesRemovedDates(t *testing.T) {
	// Test that removed dates are also merged
	date1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	feedA := &Feed{
		Index: 0,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:           "service1",
					Monday:       true,
					RemovedDates: []time.Time{date1},
				},
			},
		},
	}

	feedB := &Feed{
		Index: 1,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:           "service1",
					Monday:       true,
					RemovedDates: []time.Time{date2},
				},
			},
		},
	}

	merger := NewMerger(DefaultOptions())
	merger.RegisterScorer("service", &scorers.ServiceScorer{})

	result, err := merger.Merge([]*Feed{feedB, feedA})
	require.NoError(t, err)

	service := result.Merged.Services[0]
	assert.Equal(t, 2, len(service.RemovedDates), "Should merge RemovedDates from both services")
}

func TestMerge_ServiceDuplicates_ConflictingCalendarDates(t *testing.T) {
	// Test behavior when same date is Added in one service, Removed in another
	// Current behavior: both are kept (documenting existing behavior)
	conflictDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	feedA := &Feed{
		Index: 0,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:         "service1",
					Monday:     true,
					AddedDates: []time.Time{conflictDate}, // Added in feed A
				},
			},
		},
	}

	feedB := &Feed{
		Index: 1,
		Data: &gtfs.Static{
			Services: []gtfs.Service{
				{
					Id:           "service1",
					Monday:       true,
					RemovedDates: []time.Time{conflictDate}, // Removed in feed B
				},
			},
		},
	}

	merger := NewMerger(DefaultOptions())
	merger.RegisterScorer("service", &scorers.ServiceScorer{})

	result, err := merger.Merge([]*Feed{feedB, feedA})
	require.NoError(t, err)

	service := result.Merged.Services[0]
	// Current behavior: both Added and Removed lists contain the date
	// This is acceptable - the GTFS consumer will need to handle conflicts
	assert.Contains(t, service.AddedDates, conflictDate, "Should have conflicting date in AddedDates")
	assert.Contains(t, service.RemovedDates, conflictDate, "Should have conflicting date in RemovedDates")
}

func TestMerge_TransferDuplicates_Identity(t *testing.T) {
	stopA := gtfs.Stop{Id: "stop_a"}
	stopB := gtfs.Stop{Id: "stop_b"}

	feedA := &Feed{
		Index: 0,
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{stopA, stopB},
			Transfers: []gtfs.Transfer{
				{
					From: &stopA,
					To:   &stopB,
					Type: gtfs.TransferType_Timed,
				},
			},
		},
	}

	feedB := &Feed{
		Index: 1,
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{stopA, stopB},
			Transfers: []gtfs.Transfer{
				{
					From: &stopA, // Same from/to stops = duplicate
					To:   &stopB,
					Type: gtfs.TransferType_Timed,
				},
			},
		},
	}

	merger := NewMerger(Options{Strategy: IDENTITY})
	result, err := merger.Merge([]*Feed{feedA, feedB}) // feedA first, then feedB
	require.NoError(t, err)

	// Should detect duplicate and keep only one transfer
	assert.Equal(t, 1, len(result.Merged.Transfers), "Should have 1 transfer after duplicate detection")
	// feedB is processed second, so duplicates are in DuplicatesA (first older feed processed)
	assert.Greater(t, result.DuplicatesA, 0, "Should report at least 1 duplicate")
}

func TestMerge_TransferDuplicates_Fuzzy(t *testing.T) {
	stopA := gtfs.Stop{Id: "stop_a"}
	stopB := gtfs.Stop{Id: "stop_b"}

	feedA := &Feed{
		Index: 0,
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{stopA, stopB},
			Transfers: []gtfs.Transfer{
				{
					From: &stopA,
					To:   &stopB,
					Type: gtfs.TransferType_Timed,
				},
			},
		},
	}

	feedB := &Feed{
		Index: 1,
		Data: &gtfs.Static{
			Stops: []gtfs.Stop{stopA, stopB},
			Transfers: []gtfs.Transfer{
				{
					From: &stopA, // Same from/to stops
					To:   &stopB,
					Type: gtfs.TransferType_Timed, // Same type
				},
			},
		},
	}

	merger := NewMerger(Options{Strategy: FUZZY, Threshold: 0.5})
	merger.RegisterScorer("transfer", &scorers.TransferScorer{})

	result, err := merger.Merge([]*Feed{feedA, feedB})
	require.NoError(t, err)

	// Should detect duplicate via TransferScorer and keep only one transfer
	assert.Equal(t, 1, len(result.Merged.Transfers), "Should have 1 transfer after FUZZY duplicate detection")
	assert.Greater(t, result.DuplicatesA, 0, "Should report at least 1 duplicate")
}

// TestFindDuplicateFrequency_IdentityStrategy tests frequency duplicate detection with IDENTITY strategy
func TestFindDuplicateFrequency_IdentityStrategy(t *testing.T) {
	t.Run("SameTripAndTimes", func(t *testing.T) {
		existing := []gtfs.Frequency{
			{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
		}

		newFreq := gtfs.Frequency{
			StartTime: 6 * time.Hour,
			EndTime:   9 * time.Hour,
			Headway:   15 * time.Minute, // Different headway but same times
		}

		merger := NewMerger(DefaultOptions())
		result := merger.findDuplicateFrequency(existing, &newFreq, IDENTITY)

		assert.NotNil(t, result, "Should find duplicate with same trip and times")
	})

	t.Run("DifferentStartTime", func(t *testing.T) {
		existing := []gtfs.Frequency{
			{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
		}

		newFreq := gtfs.Frequency{
			StartTime: 7 * time.Hour, // Different start time
			EndTime:   9 * time.Hour,
			Headway:   10 * time.Minute,
		}

		merger := NewMerger(DefaultOptions())
		result := merger.findDuplicateFrequency(existing, &newFreq, IDENTITY)

		assert.Nil(t, result, "Should not find duplicate with different start time")
	})

	t.Run("DifferentEndTime", func(t *testing.T) {
		existing := []gtfs.Frequency{
			{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
		}

		newFreq := gtfs.Frequency{
			StartTime: 6 * time.Hour,
			EndTime:   10 * time.Hour, // Different end time
			Headway:   10 * time.Minute,
		}

		merger := NewMerger(DefaultOptions())
		result := merger.findDuplicateFrequency(existing, &newFreq, IDENTITY)

		assert.Nil(t, result, "Should not find duplicate with different end time")
	})

	t.Run("EmptyExisting", func(t *testing.T) {
		existing := []gtfs.Frequency{}

		newFreq := gtfs.Frequency{
			StartTime: 6 * time.Hour,
			EndTime:   9 * time.Hour,
			Headway:   10 * time.Minute,
		}

		merger := NewMerger(DefaultOptions())
		result := merger.findDuplicateFrequency(existing, &newFreq, IDENTITY)

		assert.Nil(t, result, "Should return nil when no existing frequencies")
	})
}

// TestMergeFrequencies_DuplicateTrips tests that frequencies are merged when trips are duplicates
func TestMergeFrequencies_DuplicateTrips(t *testing.T) {
	lat, lon := 40.0, -74.0

	t.Run("MergeFrequenciesFromBothFeeds", func(t *testing.T) {
		agency := &gtfs.Agency{Id: "agency1", Name: "Agency"}
		route := &gtfs.Route{Id: "route1", Agency: agency}
		service := &gtfs.Service{Id: "service1"}

		feedA := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
						},
					},
				},
			},
			Index:  0,
			Source: "a.zip",
		}

		feedB := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1", // Same trip ID
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 9 * time.Hour, EndTime: 12 * time.Hour, Headway: 15 * time.Minute}, // Different time window
						},
					},
				},
			},
			Index:  1,
			Source: "b.zip",
		}

		merger := NewMerger(Options{Strategy: IDENTITY})
		result, err := merger.Merge([]*Feed{feedA, feedB})
		require.NoError(t, err)

		// Should have 1 trip (duplicate)
		assert.Equal(t, 1, len(result.Merged.Trips))

		// Trip should have both frequencies (6-9 and 9-12)
		assert.Equal(t, 2, len(result.Merged.Trips[0].Frequencies), "Should preserve frequencies from both feeds")
	})

	t.Run("DeduplicateFrequenciesWithinSameTrip", func(t *testing.T) {
		agency := &gtfs.Agency{Id: "agency1", Name: "Agency"}
		route := &gtfs.Route{Id: "route1", Agency: agency}
		service := &gtfs.Service{Id: "service1"}

		feedA := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
						},
					},
				},
			},
			Index:  0,
			Source: "a.zip",
		}

		feedB := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 15 * time.Minute}, // Same time window, different headway
						},
					},
				},
			},
			Index:  1,
			Source: "b.zip",
		}

		merger := NewMerger(Options{Strategy: IDENTITY})
		result, err := merger.Merge([]*Feed{feedA, feedB})
		require.NoError(t, err)

		// Should have 1 trip
		assert.Equal(t, 1, len(result.Merged.Trips))

		// Should have only 1 frequency (deduplicated by time window)
		assert.Equal(t, 1, len(result.Merged.Trips[0].Frequencies), "Should deduplicate frequencies with same time window")
	})
}

// TestMergeFrequencies_EdgeCases tests edge cases for frequency merging
func TestMergeFrequencies_EdgeCases(t *testing.T) {
	lat, lon := 40.0, -74.0

	t.Run("NilFrequencies", func(t *testing.T) {
		agency := &gtfs.Agency{Id: "agency1", Name: "Agency"}
		route := &gtfs.Route{Id: "route1", Agency: agency}
		service := &gtfs.Service{Id: "service1"}

		feedA := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:          "trip1",
						Route:       route,
						Service:     service,
						Frequencies: nil, // Nil frequencies
					},
				},
			},
			Index:  0,
			Source: "a.zip",
		}

		feedB := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
						},
					},
				},
			},
			Index:  1,
			Source: "b.zip",
		}

		merger := NewMerger(Options{Strategy: IDENTITY})
		result, err := merger.Merge([]*Feed{feedA, feedB})
		require.NoError(t, err)

		assert.Equal(t, 1, len(result.Merged.Trips))
		assert.Equal(t, 1, len(result.Merged.Trips[0].Frequencies), "Should handle nil frequencies gracefully")
	})

	t.Run("EmptyFrequencies", func(t *testing.T) {
		agency := &gtfs.Agency{Id: "agency1", Name: "Agency"}
		route := &gtfs.Route{Id: "route1", Agency: agency}
		service := &gtfs.Service{Id: "service1"}

		feedA := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:          "trip1",
						Route:       route,
						Service:     service,
						Frequencies: []gtfs.Frequency{}, // Empty array
					},
				},
			},
			Index:  0,
			Source: "a.zip",
		}

		feedB := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute},
						},
					},
				},
			},
			Index:  1,
			Source: "b.zip",
		}

		merger := NewMerger(Options{Strategy: IDENTITY})
		result, err := merger.Merge([]*Feed{feedA, feedB})
		require.NoError(t, err)

		assert.Equal(t, 1, len(result.Merged.Trips))
		assert.Equal(t, 1, len(result.Merged.Trips[0].Frequencies), "Should handle empty frequencies array")
	})

	t.Run("DifferentExactTimes", func(t *testing.T) {
		agency := &gtfs.Agency{Id: "agency1", Name: "Agency"}
		route := &gtfs.Route{Id: "route1", Agency: agency}
		service := &gtfs.Service{Id: "service1"}

		feedA := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute, ExactTimes: gtfs.FrequencyBased},
						},
					},
				},
			},
			Index:  0,
			Source: "a.zip",
		}

		feedB := &Feed{
			Data: &gtfs.Static{
				Agencies: []gtfs.Agency{*agency},
				Stops:    []gtfs.Stop{{Id: "stop1", Name: "Stop", Latitude: &lat, Longitude: &lon}},
				Routes:   []gtfs.Route{*route},
				Services: []gtfs.Service{*service},
				Trips: []gtfs.ScheduledTrip{
					{
						ID:      "trip1",
						Route:   route,
						Service: service,
						Frequencies: []gtfs.Frequency{
							{StartTime: 6 * time.Hour, EndTime: 9 * time.Hour, Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased},
						},
					},
				},
			},
			Index:  1,
			Source: "b.zip",
		}

		merger := NewMerger(Options{Strategy: IDENTITY})
		result, err := merger.Merge([]*Feed{feedA, feedB})
		require.NoError(t, err)

		assert.Equal(t, 1, len(result.Merged.Trips))
		// Should still deduplicate based on time window, even if ExactTimes differs
		assert.Equal(t, 1, len(result.Merged.Trips[0].Frequencies), "Should deduplicate by time window regardless of ExactTimes")
	})
}
