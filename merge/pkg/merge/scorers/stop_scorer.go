package scorers

import (
	"github.com/OneBusAway/go-gtfs"
	"math"
)

// StopNameScorer scores stops based on exact name matching
type StopNameScorer struct{}

// Score returns 1.0 if names match exactly, 0.0 otherwise
func (s *StopNameScorer) Score(a, b interface{}) float64 {
	stopA, okA := a.(*gtfs.Stop)
	stopB, okB := b.(*gtfs.Stop)

	if !okA || !okB {
		return 0.0
	}

	if stopA.Name == stopB.Name {
		return 1.0
	}
	return 0.0
}

// StopDistanceScorer scores stops based on geographic distance
type StopDistanceScorer struct{}

// Score returns a distance-based similarity score
// <50m = 1.0, <100m = 0.75, <500m = 0.5, else 0.0
func (s *StopDistanceScorer) Score(a, b interface{}) float64 {
	stopA, okA := a.(*gtfs.Stop)
	stopB, okB := b.(*gtfs.Stop)

	if !okA || !okB {
		return 0.0
	}

	// Check if both stops have coordinates
	if stopA.Latitude == nil || stopA.Longitude == nil || stopB.Latitude == nil || stopB.Longitude == nil {
		return 0.0
	}

	dist := haversineDistance(*stopA.Latitude, *stopA.Longitude, *stopB.Latitude, *stopB.Longitude)

	if dist < 50 {
		return 1.0
	} else if dist < 100 {
		return 0.75
	} else if dist < 500 {
		return 0.5
	}
	return 0.0
}

// haversineDistance calculates the great-circle distance between two points in meters
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000 // Earth radius in meters

	// Convert to radians
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180
	deltaLat := (lat2 - lat1) * math.Pi / 180
	deltaLon := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadius * c
}

// CompositeStopScorer combines multiple scorers
type CompositeStopScorer struct {
	scorers []interface {
		Score(a, b interface{}) float64
	}
}

// NewCompositeStopScorer creates a scorer that averages name and distance scores
func NewCompositeStopScorer() *CompositeStopScorer {
	return &CompositeStopScorer{
		scorers: []interface {
			Score(a, b interface{}) float64
		}{
			&StopNameScorer{},
			&StopDistanceScorer{},
		},
	}
}

// Score returns the average of all sub-scorers
func (c *CompositeStopScorer) Score(a, b interface{}) float64 {
	if len(c.scorers) == 0 {
		return 0.0
	}

	var total float64
	for _, scorer := range c.scorers {
		total += scorer.Score(a, b)
	}

	return total / float64(len(c.scorers))
}
