package scorers

import (
	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestTripScorer_SameRoute(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route1,
	}

	score := scorer.Score(tripA, tripB)

	// Trips on the same route with no stops: (route:1.0 + stop:0.0 + direction:1.0) / 3 = 0.67
	assert.InDelta(t, 0.67, score, 0.01, "Trips on same route with no stops should score ~0.67")
}

func TestTripScorer_DifferentRoutes(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	route2 := &gtfs.Route{Id: "route2", ShortName: "R2"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route2,
	}

	score := scorer.Score(tripA, tripB)

	// Trips on different routes return 0.0 immediately (can't be duplicates)
	assert.Equal(t, 0.0, score, "Trips on different routes should score 0.0")
}

func TestTripScorer_InvalidTypes(t *testing.T) {
	scorer := &TripScorer{}

	score := scorer.Score("not a trip", "also not a trip")

	assert.Equal(t, 0.0, score, "Invalid types should return 0.0")
}

func TestTripScorer_IdenticalStopSequence(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}
	stop3 := &gtfs.Stop{Id: "stop3"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
			{Stop: stop2},
			{Stop: stop3},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
			{Stop: stop2},
			{Stop: stop3},
		},
	}

	score := scorer.Score(tripA, tripB)

	// Identical stop sequence on same route = very high score
	assert.Greater(t, score, 0.9, "Identical stop sequences should score > 0.9")
}

func TestTripScorer_PartialStopOverlap(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}
	stop3 := &gtfs.Stop{Id: "stop3"}
	stop4 := &gtfs.Stop{Id: "stop4"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
			{Stop: stop2},
			{Stop: stop3},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop2},
			{Stop: stop3},
			{Stop: stop4},
		},
	}

	score := scorer.Score(tripA, tripB)

	// Partial overlap (2 out of 4 unique stops) = medium score
	assert.Greater(t, score, 0.5, "Partial overlap should score > 0.5")
	assert.Less(t, score, 0.9, "Partial overlap should score < 0.9")
}

func TestTripScorer_NoStopOverlap(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}
	stop2 := &gtfs.Stop{Id: "stop2"}
	stop3 := &gtfs.Stop{Id: "stop3"}
	stop4 := &gtfs.Stop{Id: "stop4"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
			{Stop: stop2},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop3},
			{Stop: stop4},
		},
	}

	score := scorer.Score(tripA, tripB)

	// No stop overlap: (route:1.0 + stops:0.0 + direction:1.0) / 3 = 0.67
	assert.InDelta(t, 0.67, score, 0.01, "No stop overlap should score ~0.67")
}

func TestTripScorer_SameDirection(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	tripA := &gtfs.ScheduledTrip{
		ID:          "trip1",
		Route:       route1,
		DirectionId: 0,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:          "trip2",
		Route:       route1,
		DirectionId: 0,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
		},
	}

	score := scorer.Score(tripA, tripB)

	// Same route, same stops, same direction = very high score
	assert.Greater(t, score, 0.9, "Same direction should boost score > 0.9")
}

func TestTripScorer_DifferentDirection(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}
	stop1 := &gtfs.Stop{Id: "stop1"}

	tripA := &gtfs.ScheduledTrip{
		ID:          "trip1",
		Route:       route1,
		DirectionId: 0,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:          "trip2",
		Route:       route1,
		DirectionId: 1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: stop1},
		},
	}

	score := scorer.Score(tripA, tripB)

	// Different direction should lower the score
	assert.Less(t, score, 0.9, "Different direction should lower score < 0.9")
}

func TestTripScorer_NilRoute(t *testing.T) {
	scorer := &TripScorer{}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: nil,
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: &gtfs.Route{Id: "route1"},
	}

	score := scorer.Score(tripA, tripB)

	assert.Equal(t, 0.0, score, "Nil route should return 0.0")
}

func TestTripScorer_EmptyStopTimes(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}

	tripA := &gtfs.ScheduledTrip{
		ID:        "trip1",
		Route:     route1,
		StopTimes: []gtfs.ScheduledStopTime{},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:        "trip2",
		Route:     route1,
		StopTimes: []gtfs.ScheduledStopTime{},
	}

	score := scorer.Score(tripA, tripB)

	// Same route, no stops, same direction: (1.0 + 0.0 + 1.0) / 3 = 0.67
	assert.InDelta(t, 0.67, score, 0.01, "Empty stop times should still score route and direction")
}

func TestTripScorer_NilStops(t *testing.T) {
	scorer := &TripScorer{}

	route1 := &gtfs.Route{Id: "route1", ShortName: "R1"}

	tripA := &gtfs.ScheduledTrip{
		ID:    "trip1",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: nil},
		},
	}

	tripB := &gtfs.ScheduledTrip{
		ID:    "trip2",
		Route: route1,
		StopTimes: []gtfs.ScheduledStopTime{
			{Stop: nil},
		},
	}

	score := scorer.Score(tripA, tripB)

	// Should handle nil stops gracefully
	assert.Greater(t, score, 0.0, "Should handle nil stops without crashing")
}
