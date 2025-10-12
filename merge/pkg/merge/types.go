package merge

import (
	"github.com/OneBusAway/go-gtfs"
)

// Strategy represents the duplicate detection approach
type Strategy int

const (
	// IDENTITY assumes entities with the same ID are duplicates
	IDENTITY Strategy = iota
	// FUZZY uses similarity scoring to find duplicates with different IDs
	FUZZY
	// NONE treats everything as unique, prefixes IDs to avoid collisions
	NONE
)

func (s Strategy) String() string {
	switch s {
	case IDENTITY:
		return "IDENTITY"
	case FUZZY:
		return "FUZZY"
	case NONE:
		return "NONE"
	default:
		return "UNKNOWN"
	}
}

// RenameMode determines how ID collisions are handled
type RenameMode int

const (
	// CONTEXT mode prefixes IDs with feed index (a-, b-, c-)
	CONTEXT RenameMode = iota
	// AGENCY mode prefixes IDs with agency_id
	AGENCY
)

// Options configures the merge behavior
type Options struct {
	// Strategy to use for duplicate detection (auto-detected if not specified)
	Strategy Strategy
	// RenameMode for handling ID collisions
	RenameMode RenameMode
	// Threshold for fuzzy matching (0.0-1.0)
	Threshold float64
	// SampleSize for auto-detection
	SampleSize int
}

// DefaultOptions returns sensible defaults
func DefaultOptions() Options {
	return Options{
		Strategy:   IDENTITY, // Will be auto-detected
		RenameMode: CONTEXT,
		Threshold:  0.5,
		SampleSize: 100,
	}
}

// Feed represents a GTFS feed to be merged
type Feed struct {
	Data   *gtfs.Static
	Index  int    // Feed index for CONTEXT renaming
	Source string // URL or file path
}

// Match represents a potential duplicate pair
type Match struct {
	IndexA int
	IndexB int
	Score  float64
}

// MergeResult contains the merged feed and metadata
type MergeResult struct {
	Merged      *gtfs.Static
	Strategy    Strategy
	DuplicatesA int // Number of duplicates found in feed A
	DuplicatesB int // Number of duplicates found in feed B
	RenamingsA  int // Number of ID renamings in feed A
	RenamingsB  int // Number of ID renamings in feed B
}

// DuplicateScorer scores similarity between two entities
type DuplicateScorer interface {
	// Score returns a value from 0.0 (completely different) to 1.0 (identical)
	Score(a, b interface{}) float64
}
