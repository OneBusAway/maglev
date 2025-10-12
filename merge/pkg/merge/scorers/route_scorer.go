package scorers

import "github.com/OneBusAway/go-gtfs"

// RouteScorer scores routes based on matching properties
type RouteScorer struct{}

// Score returns similarity based on route properties
// Compares agency and route short name
func (s *RouteScorer) Score(a, b interface{}) float64 {
	routeA, okA := a.(*gtfs.Route)
	routeB, okB := b.(*gtfs.Route)

	if !okA || !okB {
		return 0.0
	}

	var score float64
	scoreParts := 0

	// Agency match
	if routeA.Agency != nil && routeB.Agency != nil {
		if routeA.Agency.Id == routeB.Agency.Id {
			score += 1.0
		}
		scoreParts++
	}

	// Short name match
	if routeA.ShortName != "" && routeB.ShortName != "" {
		if routeA.ShortName == routeB.ShortName {
			score += 1.0
		}
		scoreParts++
	}

	// Long name match
	if routeA.LongName != "" && routeB.LongName != "" {
		if routeA.LongName == routeB.LongName {
			score += 1.0
		}
		scoreParts++
	}

	if scoreParts == 0 {
		return 0.0
	}

	return score / float64(scoreParts)
}
