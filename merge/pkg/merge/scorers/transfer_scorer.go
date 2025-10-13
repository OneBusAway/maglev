package scorers

import "github.com/OneBusAway/go-gtfs"

// TransferScorer scores transfers based on matching properties
type TransferScorer struct{}

// Score returns similarity based on transfer properties.
// Compares from_stop, to_stop, transfer_type, and min_transfer_time.
//
// Scoring algorithm:
// - From and To stops must match for any score > 0.0
// - Transfer type comparison contributes to score
// - MinTransferTime comparison contributes if both are present
// - Final score is the average of compared fields
func (s *TransferScorer) Score(a, b interface{}) float64 {
	transferA, okA := a.(*gtfs.Transfer)
	transferB, okB := b.(*gtfs.Transfer)

	if !okA || !okB {
		return 0.0
	}

	// From and To stops must both be present and match
	if transferA.From == nil || transferA.To == nil || transferB.From == nil || transferB.To == nil {
		return 0.0
	}

	// Both from and to stops must match for any positive score
	if transferA.From.Id != transferB.From.Id || transferA.To.Id != transferB.To.Id {
		return 0.0
	}

	// If stops match, start scoring other fields
	var score float64 = 1.0 // Stops match
	scoreParts := 1

	// Transfer type comparison
	if transferA.Type == transferB.Type {
		score += 1.0
	}
	scoreParts++

	// MinTransferTime comparison (optional field)
	if transferA.MinTransferTime != nil && transferB.MinTransferTime != nil {
		if *transferA.MinTransferTime == *transferB.MinTransferTime {
			score += 1.0
		}
		scoreParts++
	}

	return score / float64(scoreParts)
}
