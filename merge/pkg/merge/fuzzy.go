package merge

import (
	"runtime"
	"sync"
)

// findBestMatch returns the best matching entity from candidates, or nil if no match above threshold
func (m *Merger) findBestMatch(entity interface{}, candidates []interface{},
	scorer DuplicateScorer, threshold float64) *Match {

	if len(candidates) == 0 {
		return nil
	}

	var bestMatch *Match
	for i, candidate := range candidates {
		score := scorer.Score(entity, candidate)
		if score >= threshold && (bestMatch == nil || score > bestMatch.Score) {
			bestMatch = &Match{IndexA: 0, IndexB: i, Score: score}
		}
	}
	return bestMatch
}

// findDuplicatesParallel finds all duplicate pairs using parallel scoring
func (m *Merger) findDuplicatesParallel(entitiesA, entitiesB []interface{},
	scorer DuplicateScorer, threshold float64) []Match {

	if len(entitiesA) == 0 || len(entitiesB) == 0 {
		return []Match{}
	}

	workers := runtime.NumCPU()
	matches := make(chan Match, 100)
	var wg sync.WaitGroup

	// Divide work across workers
	chunkSize := (len(entitiesA) + workers - 1) / workers

	for w := 0; w < workers; w++ {
		wg.Add(1)
		start := w * chunkSize
		end := start + chunkSize
		if end > len(entitiesA) {
			end = len(entitiesA)
		}

		go func(start, end int) {
			defer wg.Done()
			for i := start; i < end; i++ {
				if match := m.findBestMatch(entitiesA[i], entitiesB, scorer, threshold); match != nil {
					match.IndexA = i
					matches <- *match
				}
			}
		}(start, end)
	}

	// Close matches channel when all workers are done
	go func() {
		wg.Wait()
		close(matches)
	}()

	// Collect results
	var results []Match
	for match := range matches {
		results = append(results, match)
	}

	return results
}
