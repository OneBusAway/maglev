package restapi

import (
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// The onDemandServiceIds pointer is the only /where coupling to GTFS-Flex
// (wiki §3.1). sendResponse fills it on every Route and Stop a response
// carries, so no handler has to remember to.
//
// The index is fetched once per response and that snapshot handed to the
// per-item setters: FlexIndex() takes the static read lock, so fetching it per
// item could stall behind a waiting reload and mix two snapshots.

// attachOnDemandPointersToResponse fills the pointer on every route and stop
// in the response: the references block and any route or stop entry or list,
// at any depth of the map-built envelope.
func (api *RestAPI) attachOnDemandPointersToResponse(response *models.ResponseModel) {
	index := api.pointerFlexIndex()
	if index == nil {
		return
	}
	response.Data = withOnDemandPointers(index, response.Data)
}

// withOnDemandPointers sets the pointer on the routes and stops within value
// and returns it. Slices and pointers are updated in place; a Route or Stop
// held by value is returned updated so the caller can store it back.
func withOnDemandPointers(index *gtfs.FlexIndex, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			typed[key] = withOnDemandPointers(index, nested)
		}
	case models.ReferencesModel:
		setOnDemandPointers(index, typed.Routes, typed.Stops)
	case *models.ReferencesModel:
		setOnDemandPointers(index, typed.Routes, typed.Stops)
	case models.OnDemandReferences:
		setOnDemandPointers(index, typed.Routes, typed.Stops)
	case *models.OnDemandReferences:
		setOnDemandPointers(index, typed.Routes, typed.Stops)
	case []models.Route:
		setOnDemandPointers(index, typed, nil)
	case []models.Stop:
		setOnDemandPointers(index, nil, typed)
	case models.Route:
		setRouteOnDemandIDs(index, &typed)
		return typed
	case *models.Route:
		setRouteOnDemandIDs(index, typed)
	case models.Stop:
		setStopOnDemandIDs(index, &typed)
		return typed
	case *models.Stop:
		setStopOnDemandIDs(index, typed)
	}
	return value
}

// setOnDemandPointers fills the pointer on every route and stop in place.
func setOnDemandPointers(index *gtfs.FlexIndex, routes []models.Route, stops []models.Stop) {
	for i := range routes {
		setRouteOnDemandIDs(index, &routes[i])
	}
	for i := range stops {
		setStopOnDemandIDs(index, &stops[i])
	}
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
