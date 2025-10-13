package scorers

import (
	"github.com/OneBusAway/go-gtfs"
	"testing"
)

func TestAgencyScorer_InvalidTypes(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name string
		a    interface{}
		b    interface{}
		want float64
	}{
		{
			name: "nil values",
			a:    nil,
			b:    nil,
			want: 0.0,
		},
		{
			name: "first is not Agency",
			a:    "not an agency",
			b:    &gtfs.Agency{},
			want: 0.0,
		},
		{
			name: "second is not Agency",
			a:    &gtfs.Agency{},
			b:    123,
			want: 0.0,
		},
		{
			name: "both wrong types",
			a:    &gtfs.Stop{},
			b:    &gtfs.Route{},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("Score() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgencyScorer_NameMatching(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "exact name match",
			agencyA: &gtfs.Agency{
				Name: "Metro Transit",
			},
			agencyB: &gtfs.Agency{
				Name: "Metro Transit",
			},
			expected: 1.0,
		},
		{
			name: "name mismatch",
			agencyA: &gtfs.Agency{
				Name: "Metro Transit",
			},
			agencyB: &gtfs.Agency{
				Name: "City Bus",
			},
			expected: 0.0,
		},
		{
			name: "empty names",
			agencyA: &gtfs.Agency{
				Name: "",
			},
			agencyB: &gtfs.Agency{
				Name: "",
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_TimezoneMatching(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "matching name and timezone",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			expected: 1.0,
		},
		{
			name: "matching name, different timezone",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/New_York",
			},
			expected: 0.5,
		},
		{
			name: "different name, matching timezone",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "City Bus",
				Timezone: "America/Los_Angeles",
			},
			expected: 0.5,
		},
		{
			name: "empty timezone",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "",
			},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_URLMatching(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "matching URL contributes to score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
			},
			expected: 1.0,
		},
		{
			name: "different URL lowers score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://different.example.com",
			},
			expected: 2.0 / 3.0, // 2 out of 3 match
		},
		{
			name: "empty URL ignored in scoring",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "",
			},
			expected: 1.0,
		},
		{
			name: "one empty URL ignored",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "",
			},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_PhoneMatching(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "matching phone contributes to score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "+1-555-1234",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "+1-555-1234",
			},
			expected: 1.0,
		},
		{
			name: "different phone lowers score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "+1-555-1234",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "+1-555-9999",
			},
			expected: 2.0 / 3.0,
		},
		{
			name: "empty phone ignored",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "",
			},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_EmailMatching(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "matching email contributes to score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "contact@metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "contact@metro.example.com",
			},
			expected: 1.0,
		},
		{
			name: "different email lowers score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "contact@metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "info@metro.example.com",
			},
			expected: 2.0 / 3.0,
		},
		{
			name: "empty email ignored",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Email:    "",
			},
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_CompositeScoring(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "all fields match",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
			},
			expected: 1.0,
		},
		{
			name: "partial match - 3 out of 5",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://different.example.com",
				Phone:    "+1-555-9999",
				Email:    "info@metro.example.com",
			},
			expected: 2.0 / 5.0,
		},
		{
			name: "only required fields present",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			expected: 1.0,
		},
		{
			name: "mixed optional fields",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Phone:    "+1-555-1234",
			},
			expected: 1.0, // Only name and timezone are compared
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_EdgeCases(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "completely different agencies",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
			},
			agencyB: &gtfs.Agency{
				Name:     "City Bus",
				Timezone: "America/New_York",
				Url:      "https://citybus.example.com",
				Phone:    "+1-555-9999",
				Email:    "info@citybus.example.com",
			},
			expected: 0.0,
		},
		{
			name: "case sensitive name",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "metro transit",
				Timezone: "America/Los_Angeles",
			},
			expected: 0.5, // Only timezone matches
		},
		{
			name: "whitespace in names",
			agencyA: &gtfs.Agency{
				Name:     "Metro  Transit",
				Timezone: "America/Los_Angeles",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
			},
			expected: 0.5, // Only timezone matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAgencyScorer_LanguageAndFareUrl(t *testing.T) {
	scorer := &AgencyScorer{}

	tests := []struct {
		name     string
		agencyA  *gtfs.Agency
		agencyB  *gtfs.Agency
		expected float64
	}{
		{
			name: "all fields including language and fareurl match",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
				Language: "en",
				FareUrl:  "https://metro.example.com/fares",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Url:      "https://metro.example.com",
				Phone:    "+1-555-1234",
				Email:    "contact@metro.example.com",
				Language: "en",
				FareUrl:  "https://metro.example.com/fares",
			},
			expected: 1.0,
		},
		{
			name: "matching language contributes to score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Language: "en",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Language: "en",
			},
			expected: 1.0,
		},
		{
			name: "different language lowers score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Language: "en",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				Language: "es",
			},
			expected: 2.0 / 3.0,
		},
		{
			name: "matching fareurl contributes to score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				FareUrl:  "https://metro.example.com/fares",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				FareUrl:  "https://metro.example.com/fares",
			},
			expected: 1.0,
		},
		{
			name: "different fareurl lowers score",
			agencyA: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				FareUrl:  "https://metro.example.com/fares",
			},
			agencyB: &gtfs.Agency{
				Name:     "Metro Transit",
				Timezone: "America/Los_Angeles",
				FareUrl:  "https://different.example.com/fares",
			},
			expected: 2.0 / 3.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scorer.Score(tt.agencyA, tt.agencyB)
			if got != tt.expected {
				t.Errorf("Score() = %v, want %v", got, tt.expected)
			}
		})
	}
}
