package scorers

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestStopNameScorer(t *testing.T) {
	scorer := &StopNameScorer{}

	lat1, lon1 := 40.0, -74.0

	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopC := &gtfs.Stop{Id: "C", Name: "Oak Ave", Latitude: &lat1, Longitude: &lon1}

	// Same name
	assert.Equal(t, 1.0, scorer.Score(stopA, stopB))

	// Different names
	assert.Equal(t, 0.0, scorer.Score(stopA, stopC))

	// Invalid types
	assert.Equal(t, 0.0, scorer.Score("not a stop", stopA))
	assert.Equal(t, 0.0, scorer.Score(stopA, "not a stop"))
}

func TestStopDistanceScorer(t *testing.T) {
	scorer := &StopDistanceScorer{}

	// Coordinates for different distances
	lat1, lon1 := 40.7589, -73.9851 // Times Square, NYC
	lat2, lon2 := 40.7590, -73.9851 // ~11m away
	lat3, lon3 := 40.7595, -73.9851 // ~67m away
	lat4, lon4 := 40.7629, -73.9851 // ~445m away
	lat5, lon5 := 40.8000, -73.9851 // ~4.6km away

	stopA := &gtfs.Stop{Id: "A", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Latitude: &lat2, Longitude: &lon2}
	stopC := &gtfs.Stop{Id: "C", Latitude: &lat3, Longitude: &lon3}
	stopD := &gtfs.Stop{Id: "D", Latitude: &lat4, Longitude: &lon4}
	stopE := &gtfs.Stop{Id: "E", Latitude: &lat5, Longitude: &lon5}

	// <50m = 1.0
	score := scorer.Score(stopA, stopB)
	assert.Equal(t, 1.0, score)

	// <100m = 0.75
	score = scorer.Score(stopA, stopC)
	assert.Equal(t, 0.75, score)

	// <500m = 0.5
	score = scorer.Score(stopA, stopD)
	assert.Equal(t, 0.5, score)

	// >500m = 0.0
	score = scorer.Score(stopA, stopE)
	assert.Equal(t, 0.0, score)

	// Nil coordinates
	stopNil := &gtfs.Stop{Id: "F"}
	assert.Equal(t, 0.0, scorer.Score(stopA, stopNil))
	assert.Equal(t, 0.0, scorer.Score(stopNil, stopA))

	// Invalid types
	assert.Equal(t, 0.0, scorer.Score("not a stop", stopA))
}

func TestCompositeStopScorer(t *testing.T) {
	scorer := NewCompositeStopScorer()

	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 40.7590, -73.9851 // ~11m away

	// Same name and close distance
	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Name: "Main St", Latitude: &lat2, Longitude: &lon2}

	// Score should be average of name (1.0) and distance (1.0) = 1.0
	score := scorer.Score(stopA, stopB)
	assert.Equal(t, 1.0, score)

	// Same name but far distance
	lat3, lon3 := 40.8000, -73.9851 // ~4.6km away
	stopC := &gtfs.Stop{Id: "C", Name: "Main St", Latitude: &lat3, Longitude: &lon3}

	// Score should be average of name (1.0) and distance (0.0) = 0.5
	score = scorer.Score(stopA, stopC)
	assert.Equal(t, 0.5, score)

	// Different name and far distance
	stopD := &gtfs.Stop{Id: "D", Name: "Oak Ave", Latitude: &lat3, Longitude: &lon3}

	// Score should be average of name (0.0) and distance (0.0) = 0.0
	score = scorer.Score(stopA, stopD)
	assert.Equal(t, 0.0, score)
}

func TestHaversineDistance(t *testing.T) {
	// Test with known coordinates
	// Times Square to Central Park South (approximately 1.1 km)
	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 40.7678, -73.9812

	dist := haversineDistance(lat1, lon1, lat2, lon2)

	// Should be approximately 1100 meters
	assert.InDelta(t, 1100, dist, 100) // Allow 100m tolerance

	// Same point
	dist = haversineDistance(lat1, lon1, lat1, lon1)
	assert.Equal(t, 0.0, dist)
}
