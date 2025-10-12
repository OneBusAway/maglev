package merge

import (
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/merge/pkg/merge/scorers"
)

func TestFindBestMatch_Stops_WithScorer(t *testing.T) {
	// Create test stops
	lat1, lon1 := 40.7589, -73.9851 // Times Square
	lat2, lon2 := 40.7590, -73.9851 // ~11m away - very close
	lat3, lon3 := 40.8000, -73.9851 // ~4.6km away - far

	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Name: "Main St", Latitude: &lat2, Longitude: &lon2}
	stopC := &gtfs.Stop{Id: "C", Name: "Oak Ave", Latitude: &lat3, Longitude: &lon3}

	candidates := []interface{}{stopB, stopC}
	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())
	match := merger.findBestMatch(stopA, candidates, scorer, threshold)

	require.NotNil(t, match, "Should find a match")
	assert.Equal(t, 0, match.IndexB, "Should match with stopB (index 0)")
	assert.Greater(t, match.Score, 0.8, "Score should be high for same name + close distance")
}

func TestFindBestMatch_NoMatchBelowThreshold(t *testing.T) {
	// Create test stops that are similar but below threshold
	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 40.8000, -73.9851 // ~4.6km away

	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Name: "Oak Ave", Latitude: &lat2, Longitude: &lon2} // Different name, far away

	candidates := []interface{}{stopB}
	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())
	match := merger.findBestMatch(stopA, candidates, scorer, threshold)

	assert.Nil(t, match, "Should not find a match when score is below threshold")
}

func TestFindBestMatch_EmptySlices(t *testing.T) {
	lat1, lon1 := 40.7589, -73.9851
	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}

	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())

	// Empty candidates
	match := merger.findBestMatch(stopA, []interface{}{}, scorer, threshold)
	assert.Nil(t, match, "Should return nil for empty candidates")
}

func TestFindBestMatch_PicksHighestScore(t *testing.T) {
	lat1, lon1 := 40.7589, -73.9851
	lat2, lon2 := 40.7590, -73.9851 // ~11m away
	lat3, lon3 := 40.7595, -73.9851 // ~67m away

	stopA := &gtfs.Stop{Id: "A", Name: "Main St", Latitude: &lat1, Longitude: &lon1}
	stopB := &gtfs.Stop{Id: "B", Name: "Main St", Latitude: &lat2, Longitude: &lon2} // Best match
	stopC := &gtfs.Stop{Id: "C", Name: "Main St", Latitude: &lat3, Longitude: &lon3} // Good but not best

	candidates := []interface{}{stopC, stopB} // Note: stopB is second
	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())
	match := merger.findBestMatch(stopA, candidates, scorer, threshold)

	require.NotNil(t, match)
	assert.Equal(t, 1, match.IndexB, "Should match with stopB (index 1) which has higher score")
	assert.Greater(t, match.Score, 0.8, "Should pick the match with highest score")
}

func TestFindDuplicatesParallel_Stops(t *testing.T) {
	// Create 50 stops in each feed
	// 25 are duplicates (same name, close location, different IDs)
	// 25 are unique
	entitiesA := make([]interface{}, 50)
	entitiesB := make([]interface{}, 50)

	// Create duplicates (first 25)
	for i := 0; i < 25; i++ {
		lat, lon := 40.7589+float64(i)*0.0001, -73.9851
		entitiesA[i] = &gtfs.Stop{
			Id:        "A" + string(rune('0'+i)),
			Name:      "Stop " + string(rune('A'+i)),
			Latitude:  &lat,
			Longitude: &lon,
		}
		latB, lonB := lat+0.00001, lon // ~1m away - very close
		entitiesB[i] = &gtfs.Stop{
			Id:        "B" + string(rune('0'+i)),
			Name:      "Stop " + string(rune('A'+i)), // Same name
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	// Create unique stops (last 25)
	for i := 25; i < 50; i++ {
		lat, lon := 40.7589+float64(i)*0.0001, -73.9851
		entitiesA[i] = &gtfs.Stop{
			Id:        "A" + string(rune('0'+i)),
			Name:      "Unique A " + string(rune('A'+i-25)),
			Latitude:  &lat,
			Longitude: &lon,
		}
		latB, lonB := 41.0+float64(i)*0.0001, -74.0 // Far away
		entitiesB[i] = &gtfs.Stop{
			Id:        "B" + string(rune('0'+i)),
			Name:      "Unique B " + string(rune('A'+i-25)),
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())
	matches := merger.findDuplicatesParallel(entitiesA, entitiesB, scorer, threshold)

	// Should find approximately 25 duplicate pairs
	assert.GreaterOrEqual(t, len(matches), 20, "Should find at least 20 duplicates")
	assert.LessOrEqual(t, len(matches), 30, "Should not find more than 30 duplicates")
}

func TestFindDuplicatesParallel_EmptyInputs(t *testing.T) {
	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())

	// Empty entitiesA
	matches := merger.findDuplicatesParallel([]interface{}{}, []interface{}{&gtfs.Stop{}}, scorer, threshold)
	assert.Empty(t, matches, "Should return empty for empty entitiesA")

	// Empty entitiesB
	matches = merger.findDuplicatesParallel([]interface{}{&gtfs.Stop{}}, []interface{}{}, scorer, threshold)
	assert.Empty(t, matches, "Should return empty for empty entitiesB")
}

func TestFindDuplicatesParallel_ConcurrentSafety(t *testing.T) {
	// This test will fail if there are data races (run with -race flag)
	// Create a larger dataset to increase chance of concurrent access
	entitiesA := make([]interface{}, 100)
	entitiesB := make([]interface{}, 100)

	for i := 0; i < 100; i++ {
		lat, lon := 40.7589+float64(i)*0.0001, -73.9851
		entitiesA[i] = &gtfs.Stop{
			Id:        "A" + string(rune('0'+i%10)),
			Name:      "Stop " + string(rune('A'+i%26)),
			Latitude:  &lat,
			Longitude: &lon,
		}
		latB, lonB := lat+0.00001, lon
		entitiesB[i] = &gtfs.Stop{
			Id:        "B" + string(rune('0'+i%10)),
			Name:      "Stop " + string(rune('A'+i%26)),
			Latitude:  &latB,
			Longitude: &lonB,
		}
	}

	scorer := scorers.NewCompositeStopScorer()
	threshold := 0.5

	merger := NewMerger(DefaultOptions())
	matches := merger.findDuplicatesParallel(entitiesA, entitiesB, scorer, threshold)

	// Just verify it completes without panicking
	assert.NotNil(t, matches, "Should complete without data races")
}
