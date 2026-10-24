package gtfs

import (
	"context"
	"fmt"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
)

// consolidatedStopIDs maps each stop that stop consolidation absorbed, and
// whose stop times moved, to the stored stop that took them over. Realtime
// feeds keep sending the absorbed IDs, and Java translates them to the
// canonical stop.
type consolidatedStopIDs map[string]string

// loadConsolidatedStopIDs reads the stops the last import absorbed. It reads
// the database, not the file, because a restart that skips an unchanged
// import never reads the file.
func loadConsolidatedStopIDs(ctx context.Context, db *gtfsdb.Client) (consolidatedStopIDs, error) {
	rows, err := db.Queries.ListStopConsolidations(ctx)
	if err != nil {
		return nil, fmt.Errorf("error loading consolidated stops: %w", err)
	}
	ids := make(consolidatedStopIDs)
	for _, row := range rows {
		if row.CanonicalID.Valid {
			ids[row.AbsorbedID] = row.CanonicalID.String
		}
	}
	return ids, nil
}

// translate replaces absorbed stop IDs in realtime with their canonical
// stops: in trip updates, in vehicle positions and in alerts.
func (ids consolidatedStopIDs) translate(realtime *gtfs.Realtime) {
	if len(ids) == 0 || realtime == nil {
		return
	}
	for i := range realtime.Trips {
		trip := &realtime.Trips[i]
		for j := range trip.StopTimeUpdates {
			ids.replace(&trip.StopTimeUpdates[j].StopID)
		}
		if trip.Vehicle != nil {
			ids.replace(&trip.Vehicle.StopID)
		}
	}
	for i := range realtime.Vehicles {
		ids.replace(&realtime.Vehicles[i].StopID)
	}
	for i := range realtime.Alerts {
		entities := realtime.Alerts[i].InformedEntities
		for j := range entities {
			ids.replace(&entities[j].StopID)
		}
	}
}

// replace points stopID at the canonical stop's ID when it names an absorbed
// stop. It swaps the pointer rather than writing through it, because parsed
// messages can share strings.
func (ids consolidatedStopIDs) replace(stopID **string) {
	if *stopID == nil {
		return
	}
	if canonical, absorbed := ids[**stopID]; absorbed {
		*stopID = &canonical
	}
}
