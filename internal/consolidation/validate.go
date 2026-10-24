package consolidation

import (
	"cmp"
	"fmt"
	"slices"

	"maglev.onebusaway.org/internal/utils"
)

// Location is where a loaded stop is.
type Location struct {
	Lat float64
	Lon float64
}

// Kind names a problem Validate found. The values are stable, so they can
// be used as metric labels.
type Kind string

// The problems Validate reports. None of them stops a file from loading,
// because Java loads such files without checking them.
const (
	SelfAbsorbed         Kind = "self_absorbed"
	DuplicateRule        Kind = "duplicate_rule"
	ConflictingCanonical Kind = "conflicting_canonical"
	AbsorbedCanonical    Kind = "absorbed_canonical"
	MissingCanonical     Kind = "missing_canonical"
	MissingAbsorbed      Kind = "missing_absorbed"
	FarApart             Kind = "far_apart"
)

// farApartMeters is the distance past which an absorbed stop is probably
// not at the same curb as its canonical stop.
const farApartMeters = 100

// Warning is one problem in a consolidation file. Stop is the stop the
// problem is about, OtherLine the earlier or later line involved, if any,
// and Meters the distance for FarApart.
type Warning struct {
	Kind      Kind
	Line      int
	Stop      StopID
	OtherLine int
	Meters    float64
}

// String describes the warning for a log line.
func (w Warning) String() string {
	return fmt.Sprintf("stop consolidation file line %d: %s: %s", w.Line, w.Kind, w.detail())
}

func (w Warning) detail() string {
	switch w.Kind {
	case SelfAbsorbed:
		return fmt.Sprintf("%s is absorbed into itself", w.Stop)
	case DuplicateRule:
		return fmt.Sprintf("%s is already absorbed into the same stop on line %d", w.Stop, w.OtherLine)
	case ConflictingCanonical:
		return fmt.Sprintf("%s is also absorbed on line %d; the later line wins", w.Stop, w.OtherLine)
	case AbsorbedCanonical:
		return fmt.Sprintf("canonical stop %s is absorbed on line %d", w.Stop, w.OtherLine)
	case MissingCanonical:
		return fmt.Sprintf("canonical stop %s is not in any loaded feed", w.Stop)
	case MissingAbsorbed:
		return fmt.Sprintf("absorbed stop %s is not in any loaded feed", w.Stop)
	case FarApart:
		return fmt.Sprintf("%s is %.0f m from its canonical stop", w.Stop, w.Meters)
	}
	return w.Stop.String()
}

// Validate reports problems in a consolidation file, checked against the
// stops the feeds loaded, sorted by line. It reports and never rejects:
// Java applies the same file without checking it.
func Validate(file *File, stops map[StopID]Location) []Warning {
	claims, warnings := checkClaims(file)
	warnings = append(warnings, checkAbsorbedCanonicals(file, claims)...)
	warnings = append(warnings, checkStops(file, stops, claims)...)
	slices.SortStableFunc(warnings, func(a, b Warning) int { return cmp.Compare(a.Line, b.Line) })
	return warnings
}

type claim struct {
	canonical StopID
	line      int
}

// checkClaims records which line last absorbed each stop, reporting stops
// absorbed into themselves and stops absorbed more than once. A stop absorbed
// into itself is still claimed, because Apply removes it like any other.
func checkClaims(file *File) (map[StopID]claim, []Warning) {
	claims := make(map[StopID]claim)
	var warnings []Warning
	for _, rule := range file.Rules {
		for _, absorbed := range rule.Absorbed {
			if absorbed == rule.Canonical {
				warnings = append(warnings, Warning{Kind: SelfAbsorbed, Line: rule.Line, Stop: absorbed})
			}
			if earlier, ok := claims[absorbed]; ok {
				kind := ConflictingCanonical
				if earlier.canonical == rule.Canonical {
					kind = DuplicateRule
				}
				warnings = append(warnings, Warning{Kind: kind, Line: rule.Line, Stop: absorbed, OtherLine: earlier.line})
			}
			claims[absorbed] = claim{canonical: rule.Canonical, line: rule.Line}
		}
	}
	return claims, warnings
}

// checkAbsorbedCanonicals reports canonical stops that a line, possibly
// their own, absorbs. Java drops such a stop, so its own absorbed stops
// vanish too.
func checkAbsorbedCanonicals(file *File, claims map[StopID]claim) []Warning {
	var warnings []Warning
	for _, rule := range file.Rules {
		if !absorbsOtherStops(rule) {
			continue
		}
		if absorbing, ok := claims[rule.Canonical]; ok {
			warnings = append(warnings, Warning{Kind: AbsorbedCanonical, Line: rule.Line, Stop: rule.Canonical, OtherLine: absorbing.line})
		}
	}
	return warnings
}

func absorbsOtherStops(rule Rule) bool {
	return slices.ContainsFunc(rule.Absorbed, func(absorbed StopID) bool {
		return absorbed != rule.Canonical
	})
}

// checkStops reports stops missing from the loaded feeds and absorbed
// stops far from their canonical stop. Distance is checked only on the line
// that last absorbs a stop, since that line's canonical stop is the one
// Apply uses.
func checkStops(file *File, stops map[StopID]Location, claims map[StopID]claim) []Warning {
	var warnings []Warning
	for _, rule := range file.Rules {
		if len(rule.Absorbed) == 0 {
			continue
		}
		canonical, canonicalLoaded := stops[rule.Canonical]
		if !canonicalLoaded {
			warnings = append(warnings, Warning{Kind: MissingCanonical, Line: rule.Line, Stop: rule.Canonical})
		}
		for _, absorbedID := range rule.Absorbed {
			if absorbedID == rule.Canonical {
				continue
			}
			absorbed, ok := stops[absorbedID]
			if !ok {
				warnings = append(warnings, Warning{Kind: MissingAbsorbed, Line: rule.Line, Stop: absorbedID})
				continue
			}
			if !canonicalLoaded || claims[absorbedID].line != rule.Line {
				continue
			}
			meters := utils.Distance(canonical.Lat, canonical.Lon, absorbed.Lat, absorbed.Lon)
			if meters > farApartMeters {
				warnings = append(warnings, Warning{Kind: FarApart, Line: rule.Line, Stop: absorbedID, Meters: meters})
			}
		}
	}
	return warnings
}
