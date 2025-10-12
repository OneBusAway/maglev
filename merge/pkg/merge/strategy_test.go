package merge

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/merge/pkg/merge/scorers"
)

func TestDetectStrategy_HighIDOverlap_ShouldUseIdentity(t *testing.T) {
	// Create stops with 80% ID overlap
	// IDs that overlap should also have high similarity
	lat1, lon1 := 40.7589, -73.9851

	entitiesA := make([]interface{}, 10)
	entitiesB := make([]interface{}, 10)

	// 8 overlapping stops with same IDs and same names
	for i := 0; i < 8; i++ {
		lat, lon := lat1+float64(i)*0.0001, lon1
		stop := &gtfs.Stop{
			Id:        "shared" + string(rune('0'+i)),
			Name:      "Stop " + string(rune('A'+i)),
			Latitude:  &lat,
			Longitude: &lon,
		}
		entitiesA[i] = stop
		// Clone for B
		stopB := &gtfs.Stop{
			Id:        stop.Id, // Same ID
			Name:      stop.Name,
			Latitude:  stop.Latitude,
			Longitude: stop.Longitude,
		}
		entitiesB[i] = stopB
	}

	// 2 unique stops
	for i := 8; i < 10; i++ {
		lat, lon := lat1+float64(i)*0.0001, lon1
		entitiesA[i] = &gtfs.Stop{
			Id:        "uniqueA" + string(rune('0'+i)),
			Name:      "Unique A",
			Latitude:  &lat,
			Longitude: &lon,
		}
		entitiesB[i] = &gtfs.Stop{
			Id:        "uniqueB" + string(rune('0'+i)),
			Name:      "Unique B",
			Latitude:  &lat,
			Longitude: &lon,
		}
	}

	scorer := scorers.NewCompositeStopScorer()
	merger := NewMerger(DefaultOptions())

	strategy := merger.detectStrategy(entitiesA, entitiesB, scorer)

	assert.Equal(t, IDENTITY, strategy, "Should detect IDENTITY when >50% IDs overlap with high similarity")
}

func TestDetectStrategy_LowIDOverlap_HighSimilarity_ShouldUseFuzzy(t *testing.T) {
	// 10% ID overlap, but 70% fuzzy similarity (different IDs, same names+locations)
	lat1, lon1 := 40.7589, -73.9851

	entitiesA := make([]interface{}, 10)
	entitiesB := make([]interface{}, 10)

	// 1 overlapping ID
	lat, lon := lat1, lon1
	entitiesA[0] = &gtfs.Stop{
		Id:        "shared1",
		Name:      "Main St",
		Latitude:  &lat,
		Longitude: &lon,
	}
	entitiesB[0] = &gtfs.Stop{
		Id:        "shared1", // Same ID
		Name:      "Main St",
		Latitude:  &lat,
		Longitude: &lon,
	}

	// 7 stops with different IDs but same names/close locations (fuzzy duplicates)
	for i := 1; i < 8; i++ {
		latA, lonA := lat1+float64(i)*0.0001, lon1
		latB, lonB := latA+0.00001, lonA // ~1m away
		entitiesA[i] = &gtfs.Stop{
			Id:        "A" + string(rune('0'+i)),
			Name:      "Stop " + string(rune('A'+i)),
			Latitude:  &latA,
			Longitude: &lonA,
		}
		entitiesB[i] = &gtfs.Stop{
			Id:        "B" + string(rune('0'+i)),     // Different ID
			Name:      "Stop " + string(rune('A'+i)), // Same name
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	// 2 unique stops
	for i := 8; i < 10; i++ {
		latA, lonA := lat1+float64(i)*0.0001, lon1
		latB, lonB := lat1+float64(i+10)*0.0001, lon1+1.0 // Far away
		entitiesA[i] = &gtfs.Stop{
			Id:        "uniqueA" + string(rune('0'+i)),
			Name:      "Unique A",
			Latitude:  &latA,
			Longitude: &lonA,
		}
		entitiesB[i] = &gtfs.Stop{
			Id:        "uniqueB" + string(rune('0'+i)),
			Name:      "Unique B",
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	scorer := scorers.NewCompositeStopScorer()
	merger := NewMerger(DefaultOptions())

	strategy := merger.detectStrategy(entitiesA, entitiesB, scorer)

	assert.Equal(t, FUZZY, strategy, "Should detect FUZZY when ID overlap is low but fuzzy similarity is high")
}

func TestDetectStrategy_NoMatches_ShouldUseNone(t *testing.T) {
	// No ID overlap, no fuzzy matches - all stops are unique
	lat1, lon1 := 40.7589, -73.9851

	entitiesA := make([]interface{}, 10)
	entitiesB := make([]interface{}, 10)

	for i := 0; i < 10; i++ {
		latA, lonA := lat1+float64(i)*0.0001, lon1
		latB, lonB := lat1+float64(i+20)*0.0001, lon1+1.0 // Far away
		entitiesA[i] = &gtfs.Stop{
			Id:        "A" + string(rune('0'+i)),
			Name:      "Stop A " + string(rune('0'+i)),
			Latitude:  &latA,
			Longitude: &lonA,
		}
		entitiesB[i] = &gtfs.Stop{
			Id:        "B" + string(rune('0'+i)),
			Name:      "Stop B " + string(rune('0'+i)),
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	scorer := scorers.NewCompositeStopScorer()
	merger := NewMerger(DefaultOptions())

	strategy := merger.detectStrategy(entitiesA, entitiesB, scorer)

	assert.Equal(t, NONE, strategy, "Should detect NONE when no duplicates found")
}

func TestDetectStrategy_EmptyInputs(t *testing.T) {
	scorer := scorers.NewCompositeStopScorer()
	merger := NewMerger(DefaultOptions())

	// Empty A
	strategy := merger.detectStrategy([]interface{}{}, []interface{}{&gtfs.Stop{}}, scorer)
	assert.Equal(t, NONE, strategy, "Should return NONE for empty entitiesA")

	// Empty B
	strategy = merger.detectStrategy([]interface{}{&gtfs.Stop{}}, []interface{}{}, scorer)
	assert.Equal(t, NONE, strategy, "Should return NONE for empty entitiesB")
}
