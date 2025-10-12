package merge

import "github.com/OneBusAway/go-gtfs"

// detectStrategy auto-detects the best strategy for entity matching
func (m *Merger) detectStrategy(entitiesA, entitiesB []interface{}, scorer DuplicateScorer) Strategy {
	if len(entitiesA) == 0 || len(entitiesB) == 0 {
		return NONE
	}

	// Sample if too many entities
	sampleA := sampleEntities(entitiesA, m.opts.SampleSize)
	sampleB := sampleEntities(entitiesB, m.opts.SampleSize)

	// Try IDENTITY first: check ID overlap
	idOverlap := calculateIDOverlap(sampleA, sampleB)
	if idOverlap > 0.5 {
		// Check if overlapping entities are actually similar
		avgSimilarity := scoreSimilarity(sampleA, sampleB, scorer, true) // only overlapping IDs
		if avgSimilarity > 0.5 {
			return IDENTITY
		}
	}

	// Try FUZZY: find matches by similarity scoring
	matches := m.findDuplicatesParallel(sampleA, sampleB, scorer, m.opts.Threshold)
	matchRate := float64(len(matches)) / float64(len(sampleA))
	if matchRate > 0.5 {
		return FUZZY
	}

	// Fall back to NONE
	return NONE
}

// sampleEntities returns a sample of entities up to maxSize
func sampleEntities(entities []interface{}, maxSize int) []interface{} {
	if len(entities) <= maxSize {
		return entities
	}
	// Simple sampling: take evenly distributed entities
	step := len(entities) / maxSize
	result := make([]interface{}, 0, maxSize)
	for i := 0; i < len(entities) && len(result) < maxSize; i += step {
		result = append(result, entities[i])
	}
	return result
}

// calculateIDOverlap returns the fraction of IDs that appear in both sets
func calculateIDOverlap(entitiesA, entitiesB []interface{}) float64 {
	if len(entitiesA) == 0 {
		return 0.0
	}

	// Build ID set from B
	idsB := make(map[string]bool)
	for _, entity := range entitiesB {
		id := getEntityID(entity)
		if id != "" {
			idsB[id] = true
		}
	}

	// Count overlapping IDs from A
	overlapping := 0
	for _, entity := range entitiesA {
		id := getEntityID(entity)
		if id != "" && idsB[id] {
			overlapping++
		}
	}

	return float64(overlapping) / float64(len(entitiesA))
}

// scoreSimilarity returns average similarity score for entities
// If onlyOverlapping is true, only scores entities with matching IDs
func scoreSimilarity(entitiesA, entitiesB []interface{}, scorer DuplicateScorer, onlyOverlapping bool) float64 {
	if len(entitiesA) == 0 {
		return 0.0
	}

	// Build map from B for quick lookup
	entitiesBMap := make(map[string]interface{})
	for _, entity := range entitiesB {
		id := getEntityID(entity)
		if id != "" {
			entitiesBMap[id] = entity
		}
	}

	totalScore := 0.0
	scoredPairs := 0

	for _, entityA := range entitiesA {
		idA := getEntityID(entityA)
		if entityB, ok := entitiesBMap[idA]; ok {
			// IDs match, score the pair
			totalScore += scorer.Score(entityA, entityB)
			scoredPairs++
		}
	}

	if scoredPairs == 0 {
		return 0.0
	}

	return totalScore / float64(scoredPairs)
}

// getEntityID extracts the ID from an entity (handles different GTFS types)
func getEntityID(entity interface{}) string {
	switch e := entity.(type) {
	case *gtfs.Stop:
		return e.Id
	case *gtfs.Route:
		return e.Id
	case *gtfs.Agency:
		return e.Id
	case *gtfs.ScheduledTrip:
		return e.ID
	default:
		return ""
	}
}
