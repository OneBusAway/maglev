package scorers

import "github.com/OneBusAway/go-gtfs"

// AgencyScorer scores agencies based on matching properties
type AgencyScorer struct{}

// Score returns similarity based on agency properties.
// Compares name, timezone, URL, phone, email, language, and fareUrl.
//
// Scoring algorithm:
// - Each non-empty field pair is compared
// - Matching fields contribute 1.0 to the score
// - Final score is the average of compared fields
// - Returns 0.0 if no fields can be compared
func (s *AgencyScorer) Score(a, b interface{}) float64 {
	agencyA, okA := a.(*gtfs.Agency)
	agencyB, okB := b.(*gtfs.Agency)

	if !okA || !okB {
		return 0.0
	}

	var score float64
	scoreParts := 0

	// Name match (required field)
	if agencyA.Name != "" && agencyB.Name != "" {
		if agencyA.Name == agencyB.Name {
			score += 1.0
		}
		scoreParts++
	}

	// Timezone match (required field)
	if agencyA.Timezone != "" && agencyB.Timezone != "" {
		if agencyA.Timezone == agencyB.Timezone {
			score += 1.0
		}
		scoreParts++
	}

	// URL match (optional field)
	if agencyA.Url != "" && agencyB.Url != "" {
		if agencyA.Url == agencyB.Url {
			score += 1.0
		}
		scoreParts++
	}

	// Phone match (optional field)
	if agencyA.Phone != "" && agencyB.Phone != "" {
		if agencyA.Phone == agencyB.Phone {
			score += 1.0
		}
		scoreParts++
	}

	// Email match (optional field)
	if agencyA.Email != "" && agencyB.Email != "" {
		if agencyA.Email == agencyB.Email {
			score += 1.0
		}
		scoreParts++
	}

	// Language match (optional field)
	if agencyA.Language != "" && agencyB.Language != "" {
		if agencyA.Language == agencyB.Language {
			score += 1.0
		}
		scoreParts++
	}

	// FareUrl match (optional field)
	if agencyA.FareUrl != "" && agencyB.FareUrl != "" {
		if agencyA.FareUrl == agencyB.FareUrl {
			score += 1.0
		}
		scoreParts++
	}

	if scoreParts == 0 {
		return 0.0
	}

	return score / float64(scoreParts)
}
