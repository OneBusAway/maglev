package merge

import "github.com/OneBusAway/go-gtfs"

// ReferenceMap tracks ID replacements during merge (duplicates and renames)
type ReferenceMap struct {
	// Maps entity type -> old ID -> new ID
	replacements map[string]map[string]string
}

// ReferenceUpdater updates all entity references based on the reference map
type ReferenceUpdater struct {
	refMap *ReferenceMap
}

// NewReferenceMap creates a new reference map
func NewReferenceMap() *ReferenceMap {
	return &ReferenceMap{
		replacements: make(map[string]map[string]string),
	}
}

// RecordReplacement records that oldID should be replaced with newID for given entity type
func (rm *ReferenceMap) RecordReplacement(entityType, oldID, newID string) {
	if rm.replacements[entityType] == nil {
		rm.replacements[entityType] = make(map[string]string)
	}
	rm.replacements[entityType][oldID] = newID
}

// GetReplacement returns the replacement ID for oldID, following chains if necessary
// Returns (newID, true) if replacement exists, ("", false) otherwise
func (rm *ReferenceMap) GetReplacement(entityType, oldID string) (string, bool) {
	typeMap, ok := rm.replacements[entityType]
	if !ok {
		return "", false
	}

	// Follow the replacement chain
	currentID := oldID
	visited := make(map[string]bool)

	for {
		if visited[currentID] {
			// Circular reference detected, return current
			return currentID, true
		}
		visited[currentID] = true

		newID, ok := typeMap[currentID]
		if !ok {
			// End of chain
			if currentID == oldID {
				// No replacement found
				return "", false
			}
			return currentID, true
		}

		currentID = newID
	}
}

// NewReferenceUpdater creates a new reference updater
func NewReferenceUpdater(refMap *ReferenceMap) *ReferenceUpdater {
	return &ReferenceUpdater{refMap: refMap}
}

// UpdateStopReferences updates all stop references in the feed
func (ru *ReferenceUpdater) UpdateStopReferences(feed *gtfs.Static) {
	// Update stop times
	for tripIdx := range feed.Trips {
		for stIdx := range feed.Trips[tripIdx].StopTimes {
			stop := feed.Trips[tripIdx].StopTimes[stIdx].Stop
			if stop != nil {
				if newID, ok := ru.refMap.GetReplacement("stop", stop.Id); ok {
					feed.Trips[tripIdx].StopTimes[stIdx].Stop.Id = newID
				}
			}
		}
	}

	// Update transfers
	for i := range feed.Transfers {
		if feed.Transfers[i].From != nil {
			if newID, ok := ru.refMap.GetReplacement("stop", feed.Transfers[i].From.Id); ok {
				feed.Transfers[i].From.Id = newID
			}
		}
		if feed.Transfers[i].To != nil {
			if newID, ok := ru.refMap.GetReplacement("stop", feed.Transfers[i].To.Id); ok {
				feed.Transfers[i].To.Id = newID
			}
		}
	}

	// Update parent station references
	for i := range feed.Stops {
		if feed.Stops[i].Parent != nil {
			if newID, ok := ru.refMap.GetReplacement("stop", feed.Stops[i].Parent.Id); ok {
				feed.Stops[i].Parent.Id = newID
			}
		}
	}
}

// UpdateRouteReferences updates all route references in the feed
func (ru *ReferenceUpdater) UpdateRouteReferences(feed *gtfs.Static) {
	// Update trip route references
	for i := range feed.Trips {
		if feed.Trips[i].Route != nil {
			if newID, ok := ru.refMap.GetReplacement("route", feed.Trips[i].Route.Id); ok {
				feed.Trips[i].Route.Id = newID
			}
		}
	}
}

// UpdateServiceReferences updates all service references in the feed
func (ru *ReferenceUpdater) UpdateServiceReferences(feed *gtfs.Static) {
	// Update trip service references
	for i := range feed.Trips {
		if feed.Trips[i].Service != nil {
			if newID, ok := ru.refMap.GetReplacement("service", feed.Trips[i].Service.Id); ok {
				feed.Trips[i].Service.Id = newID
			}
		}
	}
}

// UpdateAgencyReferences updates all agency references in the feed
func (ru *ReferenceUpdater) UpdateAgencyReferences(feed *gtfs.Static) {
	// Update route agency references
	for i := range feed.Routes {
		if feed.Routes[i].Agency != nil {
			if newID, ok := ru.refMap.GetReplacement("agency", feed.Routes[i].Agency.Id); ok {
				feed.Routes[i].Agency.Id = newID
			}
		}
	}
}

// UpdateShapeReferences updates all shape references in the feed
func (ru *ReferenceUpdater) UpdateShapeReferences(feed *gtfs.Static) {
	// Update trip shape references
	for i := range feed.Trips {
		if feed.Trips[i].Shape != nil {
			if newID, ok := ru.refMap.GetReplacement("shape", feed.Trips[i].Shape.ID); ok {
				feed.Trips[i].Shape.ID = newID
			}
		}
	}
}

// UpdateTripReferences updates all trip references in the feed
// This must be called AFTER trips are merged to ensure all trip replacements are recorded
func (ru *ReferenceUpdater) UpdateTripReferences(feed *gtfs.Static) {
	// Update trip references in stop times
	for tripIdx := range feed.Trips {
		for stIdx := range feed.Trips[tripIdx].StopTimes {
			trip := feed.Trips[tripIdx].StopTimes[stIdx].Trip
			if trip != nil {
				if newID, ok := ru.refMap.GetReplacement("trip", trip.ID); ok {
					feed.Trips[tripIdx].StopTimes[stIdx].Trip.ID = newID
				}
			}
		}
	}

	// Note: Frequencies are embedded in ScheduledTrip.Frequencies[] and have no Trip references.
	// They are automatically updated when trips are merged since they're part of the trip struct.
}

// UpdateAllReferences updates all entity references in the feed
// Note: Trip references must be updated separately AFTER trips are merged
func (ru *ReferenceUpdater) UpdateAllReferences(feed *gtfs.Static) {
	ru.UpdateAgencyReferences(feed)
	ru.UpdateStopReferences(feed)
	ru.UpdateRouteReferences(feed)
	ru.UpdateServiceReferences(feed)
	ru.UpdateShapeReferences(feed)
}
