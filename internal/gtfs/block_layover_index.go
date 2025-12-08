package gtfs

import (
	"sort"

	"github.com/OneBusAway/go-gtfs"
)

// BlockLayoverIndex represents a layover index that groups block trips by their layover patterns
type BlockLayoverIndex struct {
	ServiceIDs    []string
	LayoverStopID string
	Trips         []BlockLayoverTrip
	StartTimes    []int64
	EndTimes      []int64
	RouteIDs      []string
	BlockIDs      []string
}

type BlockLayoverTrip struct {
	TripID        string
	RouteID       string
	BlockID       string
	ServiceID     string
	LayoverStopID string
	LayoverStart  int64
	LayoverEnd    int64
}

// buildBlockLayoverIndices builds BlockLayoverIndex entries from GTFS static data
// Indices are grouped by (serviceID, layoverStopID), which means trips from multiple
// routes can end up in the same index if they share the same layover terminal.
func buildBlockLayoverIndices(staticData *gtfs.Static) map[string][]*BlockLayoverIndex {
	routeLayoverIndices := make(map[string][]*BlockLayoverIndex)

	type indexKey struct {
		serviceID     string
		layoverStopID string
	}
	globalIndices := make(map[indexKey]*BlockLayoverIndex)

	// Group trips by block and service
	type blockKey struct {
		blockID   string
		serviceID string
	}
	blockTrips := make(map[blockKey][]*gtfs.ScheduledTrip)

	for i := range staticData.Trips {
		trip := &staticData.Trips[i]
		if trip.BlockID == "" || len(trip.StopTimes) == 0 {
			continue
		}
		key := blockKey{blockID: trip.BlockID, serviceID: trip.Service.Id}
		blockTrips[key] = append(blockTrips[key], trip)
	}

	// For each block, find layovers between consecutive trips
	for key, trips := range blockTrips {
		if len(trips) < 2 {
			// Need at least 2 trips to have a layover
			continue
		}

		// Sort trips by their start time (first stop departure)
		sort.Slice(trips, func(i, j int) bool {
			return trips[i].StopTimes[0].DepartureTime < trips[j].StopTimes[0].DepartureTime
		})

		// Find layovers between consecutive trips
		for i := 0; i < len(trips)-1; i++ {
			currentTrip := trips[i]
			nextTrip := trips[i+1]

			// Get the last stop of current trip and first stop of next trip
			lastStopCurrent := currentTrip.StopTimes[len(currentTrip.StopTimes)-1]
			firstStopNext := nextTrip.StopTimes[0]

			// If they're the same stop, this is a layover
			if lastStopCurrent.Stop.Id == firstStopNext.Stop.Id {
				layoverStopID := lastStopCurrent.Stop.Id
				// Layover start = when previous trip DEPARTS from its last stop
				// Layover end = when current trip ARRIVES at its first stop
				layoverStart := int64(lastStopCurrent.DepartureTime)
				layoverEnd := int64(firstStopNext.ArrivalTime)

				// Create a layover entry for the NEXT trip (the one departing from the layover)
				layoverTrip := BlockLayoverTrip{
					TripID:        nextTrip.ID,
					RouteID:       nextTrip.Route.Id,
					BlockID:       key.blockID,
					ServiceID:     key.serviceID,
					LayoverStopID: layoverStopID,
					LayoverStart:  layoverStart,
					LayoverEnd:    layoverEnd,
				}

				globalKey := indexKey{
					serviceID:     "",
					layoverStopID: layoverStopID,
				}

				// Find or create the global index
				existingIndex, exists := globalIndices[globalKey]
				if !exists {
					existingIndex = &BlockLayoverIndex{
						ServiceIDs:    []string{}, // Will be populated below
						LayoverStopID: layoverStopID,
						Trips:         []BlockLayoverTrip{},
						StartTimes:    []int64{},
						EndTimes:      []int64{},
						RouteIDs:      []string{},
						BlockIDs:      []string{},
					}
					globalIndices[globalKey] = existingIndex
				}

				existingIndex.Trips = append(existingIndex.Trips, layoverTrip)
				existingIndex.StartTimes = append(existingIndex.StartTimes, layoverTrip.LayoverStart)
				existingIndex.EndTimes = append(existingIndex.EndTimes, layoverTrip.LayoverEnd)

				if !contains(existingIndex.ServiceIDs, key.serviceID) {
					existingIndex.ServiceIDs = append(existingIndex.ServiceIDs, key.serviceID)
				}

				if !contains(existingIndex.RouteIDs, layoverTrip.RouteID) {
					existingIndex.RouteIDs = append(existingIndex.RouteIDs, layoverTrip.RouteID)
				}
				if !contains(existingIndex.RouteIDs, currentTrip.Route.Id) {
					existingIndex.RouteIDs = append(existingIndex.RouteIDs, currentTrip.Route.Id)
				}
				if !contains(existingIndex.BlockIDs, layoverTrip.BlockID) {
					existingIndex.BlockIDs = append(existingIndex.BlockIDs, layoverTrip.BlockID)
				}
			}
		}
	}

	// Map global indices to routes
	// This causes cross-route contamination: each index gets added to ALL routes that have trips in it
	for _, index := range globalIndices {
		for _, routeID := range index.RouteIDs {
			routeLayoverIndices[routeID] = append(routeLayoverIndices[routeID], index)
		}
	}

	return routeLayoverIndices
}

func getBlockLayoverIndicesForRoute(indices map[string][]*BlockLayoverIndex, routeID string) []*BlockLayoverIndex {
	return indices[routeID]
}

// GetBlocksInTimeRange returns all block IDs from layover indices that have active layovers
func GetBlocksInTimeRange(indices []*BlockLayoverIndex, startTime, endTime int64) []string {
	blockSet := make(map[string]bool)

	for _, index := range indices {
		// For each trip in the index, check if its layover interval overlaps with our time range
		for i := range index.Trips {
			trip := index.Trips[i]

			layoverStart := index.StartTimes[i]
			layoverEnd := index.EndTimes[i]

			if layoverStart < endTime && layoverEnd > startTime {
				blockID := trip.BlockID
				blockSet[blockID] = true
			}
		}
	}

	blocks := make([]string, 0, len(blockSet))
	for blockID := range blockSet {
		blocks = append(blocks, blockID)
	}

	return blocks
}

func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
