package gtfsdb

import (
	"context"
	"fmt"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/nulls"
)

// StopReplacement is what stop consolidation does to one stop: the stop is
// not stored, and its stop times move to Canonical, or are dropped when
// Canonical is nil. RuleCanonical is the canonical stop as the
// consolidation file names it.
type StopReplacement struct {
	RuleCanonical string
	Canonical     *gtfs.Stop
}

// StopReplacements maps the IDs of absorbed stops to what replaces them.
type StopReplacements map[string]StopReplacement

func (r StopReplacements) isAbsorbed(stopID string) bool {
	_, absorbed := r[stopID]
	return absorbed
}

// storedStopID is the ID to store for a reference to stopID: its canonical
// stop's ID if it was absorbed, or "" when that canonical stop is not
// stored.
func (r StopReplacements) storedStopID(stopID string) string {
	replacement, absorbed := r[stopID]
	if !absorbed {
		return stopID
	}
	if replacement.Canonical == nil {
		return ""
	}
	return replacement.Canonical.Id
}

// consolidate returns stopTimes with each absorbed stop replaced by its
// canonical stop, leaving out stop times whose canonical stop is not
// stored. A trip that visits no absorbed stop is returned as it is.
func (r StopReplacements) consolidate(stopTimes []gtfs.ScheduledStopTime) []gtfs.ScheduledStopTime {
	if !r.touches(stopTimes) {
		return stopTimes
	}
	consolidated := make([]gtfs.ScheduledStopTime, 0, len(stopTimes))
	for _, stopTime := range stopTimes {
		if replacement, absorbed := r[stopTime.Stop.Id]; absorbed {
			if replacement.Canonical == nil {
				continue
			}
			stopTime.Stop = replacement.Canonical
		}
		consolidated = append(consolidated, stopTime)
	}
	return consolidated
}

func (r StopReplacements) touches(stopTimes []gtfs.ScheduledStopTime) bool {
	if len(r) == 0 {
		return false
	}
	return slices.ContainsFunc(stopTimes, func(stopTime gtfs.ScheduledStopTime) bool {
		return r.isAbsorbed(stopTime.Stop.Id)
	})
}

// storeStopConsolidations records each absorbed stop with its name and
// location. The stop/{id} alias and realtime translation still to come read
// this table, because a restart that skips an unchanged import never reads
// the consolidation file again.
func storeStopConsolidations(ctx context.Context, qtx *Queries, stops []gtfs.Stop, replacements StopReplacements) error {
	for _, stop := range stops {
		replacement, absorbed := replacements[stop.Id]
		if !absorbed {
			continue
		}
		params := CreateStopConsolidationParams{
			AbsorbedID:    stop.Id,
			CanonicalID:   nulls.NonEmptyString(replacements.storedStopID(stop.Id)),
			RuleCanonical: replacement.RuleCanonical,
			AbsorbedName:  nulls.String(stop.Name),
			AbsorbedLat:   nulls.Float64FromPtr(stop.Latitude),
			AbsorbedLon:   nulls.Float64FromPtr(stop.Longitude),
		}
		if err := qtx.CreateStopConsolidation(ctx, params); err != nil {
			return fmt.Errorf("unable to record consolidated stop %s: %w", stop.Id, err)
		}
	}
	return nil
}
