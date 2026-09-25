package restapi

import (
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// The onDemandServiceIds pointer is the only /where coupling to GTFS-Flex
// (wiki §3.1). These helpers fill it from the FlexIndex on every Route and Stop
// serialization; each handler calls them right before building its response.
//
// Every exported entry point fetches the index once and hands that snapshot to
// the per-item setters: FlexIndex() takes the static read lock, so fetching it
// per item could stall behind a waiting reload and mix two snapshots.

// attachRouteOnDemandIDs sets the pointer on a route model from its combined id.
func (api *RestAPI) attachRouteOnDemandIDs(route *models.Route) {
	if index := api.pointerFlexIndex(); index != nil {
		setRouteOnDemandIDs(index, route)
	}
}

// attachStopOnDemandIDs sets the pointer on a stop model from its combined id.
func (api *RestAPI) attachStopOnDemandIDs(stop *models.Stop) {
	if index := api.pointerFlexIndex(); index != nil {
		setStopOnDemandIDs(index, stop)
	}
}

// attachOnDemandPointers fills the pointer on every route and stop in place.
func (api *RestAPI) attachOnDemandPointers(routes []models.Route, stops []models.Stop) {
	index := api.pointerFlexIndex()
	if index == nil {
		return
	}
	for i := range routes {
		setRouteOnDemandIDs(index, &routes[i])
	}
	for i := range stops {
		setStopOnDemandIDs(index, &stops[i])
	}
}

// attachOnDemandPointersToReferences fills the pointer on a references block's
// routes and stops.
func (api *RestAPI) attachOnDemandPointersToReferences(references *models.ReferencesModel) {
	api.attachOnDemandPointers(references.Routes, references.Stops)
}

// pointerFlexIndex returns the snapshot to fill pointers from, or nil when no
// pointer can be set: a RestAPI built without an Application or manager (some
// tests), or a feed with no on-demand services, which then skips per-item id
// parsing on every /where response.
func (api *RestAPI) pointerFlexIndex() *gtfs.FlexIndex {
	if api.Application == nil || api.GtfsManager == nil {
		return nil
	}
	index := api.GtfsManager.FlexIndex()
	if index.IsFlexEmpty() {
		return nil
	}
	return index
}

// setRouteOnDemandIDs sets a route's pointer from one index snapshot; routes
// whose id does not parse are left untouched.
func setRouteOnDemandIDs(index *gtfs.FlexIndex, route *models.Route) {
	_, routeID, err := utils.ExtractAgencyIDAndCodeID(route.ID)
	if err != nil {
		return
	}
	route.OnDemandServiceIDs = index.OnDemandServiceIDsForRoute(routeID)
}

// setStopOnDemandIDs sets a stop's pointer from one index snapshot; stops
// whose id does not parse are left untouched.
func setStopOnDemandIDs(index *gtfs.FlexIndex, stop *models.Stop) {
	_, stopID, err := utils.ExtractAgencyIDAndCodeID(stop.ID)
	if err != nil {
		return
	}
	stop.OnDemandServiceIDs = index.OnDemandServiceIDsForStop(stopID)
}
