package gtfs

import (
	"context"
	"maps"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// staticTripRows memoizes the static rows of the trips one refresh names,
// so update assignment, block matching and route resolution read each row
// at most once. A nil row records a trip static data lacks.
type staticTripRows map[string]*gtfsdb.Trip

// load reads, in batches, the rows of tripIDs not loaded yet. Nothing is
// recorded when the query fails.
func (rows staticTripRows) load(ctx context.Context, queries *gtfsdb.Queries, tripIDs []string) error {
	unloaded := rows.unloaded(tripIDs)
	if len(unloaded) == 0 {
		return nil
	}
	trips, err := utils.QueryInBatches(ctx, unloaded, queries.GetTripsByIDs)
	if err != nil {
		return err
	}
	for _, tripID := range unloaded {
		rows[tripID] = nil
	}
	for i := range trips {
		rows[trips[i].ID] = &trips[i]
	}
	return nil
}

func (rows staticTripRows) unloaded(tripIDs []string) []string {
	unloaded := make(map[string]struct{})
	for _, tripID := range tripIDs {
		if _, loaded := rows[tripID]; !loaded {
			unloaded[tripID] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(unloaded))
}

// get returns the trip's static row, if it was loaded and exists.
func (rows staticTripRows) get(tripID string) (gtfsdb.Trip, bool) {
	trip := rows[tripID]
	if trip == nil {
		return gtfsdb.Trip{}, false
	}
	return *trip, true
}
