package scorers

import (
	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// TestServiceScorer_IdenticalDayPattern tests services with identical day-of-week patterns
func TestServiceScorer_IdenticalDayPattern(t *testing.T) {
	scorer := &ServiceScorer{}

	serviceA := &gtfs.Service{
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
	}

	serviceB := &gtfs.Service{
		Id:        "weekday2",
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

	score := scorer.Score(serviceA, serviceB)
	assert.Equal(t, 1.0, score, "Identical services should score 1.0")
}

// TestServiceScorer_PartialDayMatch tests services with partial day-of-week overlap
func TestServiceScorer_PartialDayMatch(t *testing.T) {
	scorer := &ServiceScorer{}

	// Weekday service (5 days)
	serviceA := &gtfs.Service{
		Id:        "weekday",
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

	// Weekend service (2 days)
	serviceB := &gtfs.Service{
		Id:        "weekend",
		Monday:    false,
		Tuesday:   false,
		Wednesday: false,
		Thursday:  false,
		Friday:    false,
		Saturday:  true,
		Sunday:    true,
		StartDate: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}

	score := scorer.Score(serviceA, serviceB)
	// No overlapping days, but same date range
	// Day pattern contributes 0.0, date range contributes 1.0
	// Average: 0.5
	assert.Equal(t, 0.5, score, "No overlapping days but identical date range should score 0.5")
}

// TestServiceScorer_NoDayMatch tests services with no day overlap
func TestServiceScorer_NoDayMatch(t *testing.T) {
	scorer := &ServiceScorer{}

	serviceA := &gtfs.Service{
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
	}

	serviceB := &gtfs.Service{
		Id:        "weekend",
		Monday:    false,
		Tuesday:   false,
		Wednesday: false,
		Thursday:  false,
		Friday:    false,
		Saturday:  true,
		Sunday:    true,
		StartDate: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
	}

	score := scorer.Score(serviceA, serviceB)
	// No overlapping days (0.0) and no overlapping dates (0.0)
	// Average: 0.0
	assert.Equal(t, 0.0, score, "No overlap in days or dates should score 0.0")
}

// TestServiceScorer_WithAddedDates tests services with exception dates
func TestServiceScorer_WithAddedDates(t *testing.T) {
	scorer := &ServiceScorer{}

	serviceA := &gtfs.Service{
		Id:         "weekday",
		Monday:     true,
		Tuesday:    true,
		Wednesday:  true,
		Thursday:   true,
		Friday:     true,
		Saturday:   false,
		Sunday:     false,
		StartDate:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		AddedDates: []time.Time{time.Date(2024, 7, 4, 0, 0, 0, 0, time.UTC)}, // Independence Day
	}

	serviceB := &gtfs.Service{
		Id:         "weekday2",
		Monday:     true,
		Tuesday:    true,
		Wednesday:  true,
		Thursday:   true,
		Friday:     true,
		Saturday:   false,
		Sunday:     false,
		StartDate:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:    time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		AddedDates: []time.Time{time.Date(2024, 7, 4, 0, 0, 0, 0, time.UTC)}, // Same exception
	}

	score := scorer.Score(serviceA, serviceB)
	// Identical services should still score 1.0 even with exception dates
	assert.Equal(t, 1.0, score, "Identical services with same exception dates should score 1.0")
}

// TestServiceScorer_WithRemovedDates tests services with removed exception dates
func TestServiceScorer_WithRemovedDates(t *testing.T) {
	scorer := &ServiceScorer{}

	serviceA := &gtfs.Service{
		Id:           "weekday",
		Monday:       true,
		Tuesday:      true,
		Wednesday:    true,
		Thursday:     true,
		Friday:       true,
		Saturday:     false,
		Sunday:       false,
		StartDate:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		RemovedDates: []time.Time{time.Date(2024, 12, 25, 0, 0, 0, 0, time.UTC)}, // Christmas
	}

	serviceB := &gtfs.Service{
		Id:           "weekday2",
		Monday:       true,
		Tuesday:      true,
		Wednesday:    true,
		Thursday:     true,
		Friday:       true,
		Saturday:     false,
		Sunday:       false,
		StartDate:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:      time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		RemovedDates: nil, // No exceptions
	}

	score := scorer.Score(serviceA, serviceB)
	// Should still score high since base patterns match
	// Day pattern: 1.0, Date range: 1.0 → Average: 1.0
	assert.Equal(t, 1.0, score, "Services with different exception dates but same base pattern should score 1.0")
}

// TestServiceScorer_InvalidTypes tests scorer with invalid input types
func TestServiceScorer_InvalidTypes(t *testing.T) {
	scorer := &ServiceScorer{}

	// Test with nil
	score := scorer.Score(nil, nil)
	assert.Equal(t, 0.0, score, "Nil inputs should score 0.0")

	// Test with wrong type
	service := &gtfs.Service{Id: "weekday"}
	score = scorer.Score(service, "not a service")
	assert.Equal(t, 0.0, score, "Wrong type should score 0.0")
}
