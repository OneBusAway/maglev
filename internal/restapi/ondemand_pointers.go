package restapi

import (
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// The onDemandServiceIds pointer is the only /where coupling to GTFS-Flex
// (wiki §3.1). These helpers fill it from the FlexIndex on every Route and Stop
// serialization; each handler calls them right before building its response.

// attachRouteOnDemandIDs sets the pointer on a route model from its combined id.
func (api *RestAPI) attachRouteOnDemandIDs(route *models.Route) {
	if !api.hasFlexIndex() {
		return
	}
	_, routeID, err := utils.ExtractAgencyIDAndCodeID(route.ID)
	if err != nil {
		return
	}
	route.OnDemandServiceIDs = api.GtfsManager.FlexIndex().OnDemandServiceIDsForRoute(routeID)
}

// attachStopOnDemandIDs sets the pointer on a stop model from its combined id.
func (api *RestAPI) attachStopOnDemandIDs(stop *models.Stop) {
	if !api.hasFlexIndex() {
		return
	}
	_, stopID, err := utils.ExtractAgencyIDAndCodeID(stop.ID)
	if err != nil {
		return
	}
	stop.OnDemandServiceIDs = api.GtfsManager.FlexIndex().OnDemandServiceIDsForStop(stopID)
}

// attachOnDemandPointers fills the pointer on every route and stop in place.
func (api *RestAPI) attachOnDemandPointers(routes []models.Route, stops []models.Stop) {
	for i := range routes {
		api.attachRouteOnDemandIDs(&routes[i])
	}
	for i := range stops {
		api.attachStopOnDemandIDs(&stops[i])
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
