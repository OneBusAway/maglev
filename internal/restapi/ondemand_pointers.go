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
	if !api.hasFlexIndex() {
		return
	}
	setRouteOnDemandIDs(api.GtfsManager.FlexIndex(), route)
}

// attachStopOnDemandIDs sets the pointer on a stop model from its combined id.
func (api *RestAPI) attachStopOnDemandIDs(stop *models.Stop) {
	if !api.hasFlexIndex() {
		return
	}
	setStopOnDemandIDs(api.GtfsManager.FlexIndex(), stop)
}

// attachOnDemandPointers fills the pointer on every route and stop in place.
func (api *RestAPI) attachOnDemandPointers(routes []models.Route, stops []models.Stop) {
	if !api.hasFlexIndex() {
		return
	}
	index := api.GtfsManager.FlexIndex()
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

// hasFlexIndex guards tests that build a RestAPI without an Application or manager.
func (api *RestAPI) hasFlexIndex() bool {
	return api.Application != nil && api.GtfsManager != nil
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
