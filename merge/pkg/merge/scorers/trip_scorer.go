package scorers

import "github.com/OneBusAway/go-gtfs"

// TripScorer scores trips based on route, stop sequence, and timing
type TripScorer struct{}

// Score returns similarity score between two trips
// Considers route matching, stop sequence similarity, and direction
func (s *TripScorer) Score(a, b interface{}) float64 {
	tripA, okA := a.(*gtfs.ScheduledTrip)
	tripB, okB := b.(*gtfs.ScheduledTrip)

	if !okA || !okB {
		return 0.0
	}

	routeScore := s.scoreRoute(tripA, tripB)

	// If routes don't match, trips can't be duplicates
	if routeScore == 0.0 {
		return 0.0
	}

	stopScore := s.scoreStopSequence(tripA, tripB)
	directionScore := s.scoreDirection(tripA, tripB)

	// Average all scores
	return (routeScore + stopScore + directionScore) / 3.0
}

// scoreRoute compares route IDs
func (s *TripScorer) scoreRoute(tripA, tripB *gtfs.ScheduledTrip) float64 {
	if tripA.Route == nil || tripB.Route == nil {
		return 0.0
	}

	if tripA.Route.Id == tripB.Route.Id {
		return 1.0 // Perfect score for same route
	}

	return 0.0
}

// scoreStopSequence uses Jaccard similarity for stop sequences
// Returns intersection size / union size
func (s *TripScorer) scoreStopSequence(tripA, tripB *gtfs.ScheduledTrip) float64 {
	if len(tripA.StopTimes) == 0 || len(tripB.StopTimes) == 0 {
		return 0.0
	}

	// Build sets of stop IDs
	stopsA := make(map[string]bool)
	for _, st := range tripA.StopTimes {
		if st.Stop != nil {
			stopsA[st.Stop.Id] = true
		}
	}

	stopsB := make(map[string]bool)
	for _, st := range tripB.StopTimes {
		if st.Stop != nil {
			stopsB[st.Stop.Id] = true
		}
	}

	// Calculate intersection
	intersection := 0
	for stopID := range stopsA {
		if stopsB[stopID] {
			intersection++
		}
	}

	// Calculate union
	union := len(stopsA) + len(stopsB) - intersection

	if union == 0 {
		return 0.0
	}

	// Jaccard similarity
	return float64(intersection) / float64(union)
}

// scoreDirection compares trip directions
func (s *TripScorer) scoreDirection(tripA, tripB *gtfs.ScheduledTrip) float64 {
	// If directions match, return 1.0
	if tripA.DirectionId == tripB.DirectionId {
		return 1.0
	}

	// Different directions = 0.0
	return 0.0
}
