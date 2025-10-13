package scorers

import (
	"github.com/OneBusAway/go-gtfs"
)

// ServiceScorer scores services based on day-of-week pattern and date range overlap
type ServiceScorer struct{}

// Score returns similarity score between two services
// Considers day-of-week pattern and date range overlap
func (s *ServiceScorer) Score(a, b interface{}) float64 {
	serviceA, okA := a.(*gtfs.Service)
	serviceB, okB := b.(*gtfs.Service)

	if !okA || !okB {
		return 0.0
	}

	dayScore := s.scoreDayPattern(serviceA, serviceB)
	dateScore := s.scoreDateRange(serviceA, serviceB)

	// Average of day pattern and date range scores
	return (dayScore + dateScore) / 2.0
}

// scoreDayPattern compares day-of-week patterns
// Returns proportion of matching days (0.0 to 1.0)
func (s *ServiceScorer) scoreDayPattern(serviceA, serviceB *gtfs.Service) float64 {
	matches := 0
	totalDays := 7

	if serviceA.Monday == serviceB.Monday {
		matches++
	}
	if serviceA.Tuesday == serviceB.Tuesday {
		matches++
	}
	if serviceA.Wednesday == serviceB.Wednesday {
		matches++
	}
	if serviceA.Thursday == serviceB.Thursday {
		matches++
	}
	if serviceA.Friday == serviceB.Friday {
		matches++
	}
	if serviceA.Saturday == serviceB.Saturday {
		matches++
	}
	if serviceA.Sunday == serviceB.Sunday {
		matches++
	}

	return float64(matches) / float64(totalDays)
}

// scoreDateRange compares date ranges and returns overlap percentage
func (s *ServiceScorer) scoreDateRange(serviceA, serviceB *gtfs.Service) float64 {
	// Calculate overlap of date ranges
	overlapStart := serviceA.StartDate
	if serviceB.StartDate.After(overlapStart) {
		overlapStart = serviceB.StartDate
	}

	overlapEnd := serviceA.EndDate
	if serviceB.EndDate.Before(overlapEnd) {
		overlapEnd = serviceB.EndDate
	}

	// No overlap if start is after end
	if overlapStart.After(overlapEnd) {
		return 0.0
	}

	// Calculate overlap duration
	overlapDays := overlapEnd.Sub(overlapStart).Hours() / 24.0

	// Calculate total coverage (union of both ranges)
	unionStart := serviceA.StartDate
	if serviceB.StartDate.Before(unionStart) {
		unionStart = serviceB.StartDate
	}

	unionEnd := serviceA.EndDate
	if serviceB.EndDate.After(unionEnd) {
		unionEnd = serviceB.EndDate
	}

	unionDays := unionEnd.Sub(unionStart).Hours() / 24.0

	if unionDays == 0 {
		return 0.0
	}

	// Return Jaccard-like similarity (overlap / union)
	return overlapDays / unionDays
}
