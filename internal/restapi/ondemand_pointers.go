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

// attachRouteOnDemandIDs sets the pointer on a single-route entry and on its
// references block.
func (api *RestAPI) attachRouteOnDemandIDs(route *models.Route, references *models.ReferencesModel) {
	index := api.pointerFlexIndex()
	setRouteOnDemandIDs(index, route)
	setOnDemandPointers(index, references.Routes, references.Stops)
}

// attachStopOnDemandIDs sets the pointer on a single-stop entry and on its
// references block.
func (api *RestAPI) attachStopOnDemandIDs(stop *models.Stop, references *models.ReferencesModel) {
	index := api.pointerFlexIndex()
	setStopOnDemandIDs(index, stop)
	setOnDemandPointers(index, references.Routes, references.Stops)
}

// attachStopListOnDemandIDs sets the pointer on a stop list and on its
// references block from one snapshot.
func (api *RestAPI) attachStopListOnDemandIDs(stops []models.Stop, references *models.ReferencesModel) {
	index := api.pointerFlexIndex()
	setOnDemandPointers(index, nil, stops)
	setOnDemandPointers(index, references.Routes, references.Stops)
}

// attachOnDemandPointers fills the pointer on every route and stop in place.
func (api *RestAPI) attachOnDemandPointers(routes []models.Route, stops []models.Stop) {
	setOnDemandPointers(api.pointerFlexIndex(), routes, stops)
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

// setOnDemandPointers fills the pointer on every route and stop from one index
// snapshot; a nil index leaves them untouched.
func setOnDemandPointers(index *gtfs.FlexIndex, routes []models.Route, stops []models.Stop) {
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

// setRouteOnDemandIDs sets a route's pointer from one index snapshot; a nil
// index, or a route whose id does not parse, leaves it untouched.
func setRouteOnDemandIDs(index *gtfs.FlexIndex, route *models.Route) {
	if index == nil {
		return
	}
	_, routeID, err := utils.ExtractAgencyIDAndCodeID(route.ID)
	if err != nil {
		return
	}
	route.OnDemandServiceIDs = index.OnDemandServiceIDsForRoute(routeID)
}

// setStopOnDemandIDs sets a stop's pointer from one index snapshot; a nil
// index, or a stop whose id does not parse, leaves it untouched.
func setStopOnDemandIDs(index *gtfs.FlexIndex, stop *models.Stop) {
	if index == nil {
		return
	}
	_, stopID, err := utils.ExtractAgencyIDAndCodeID(stop.ID)
	if err != nil {
		return
	}
	stop.OnDemandServiceIDs = index.OnDemandServiceIDsForStop(stopID)
}
