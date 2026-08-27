package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// stopsForTripHandler returns the ordered list of stops for a single trip.
func (api *RestAPI) stopsForTripHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencyID, tripCodeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripCodeID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	orderedStopIDs, err := api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, tripCodeID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	stopsList, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, orderedStopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references := models.NewEmptyReferences()

	if ShouldIncludeReferences(r) {
		routeRefs, err := api.BuildRouteReferences(ctx, agencyID, stopsList)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		parentRefs, err := api.buildParentStationReferences(ctx, agencyID, stopsList)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}

		references.Agencies = []models.AgencyReference{models.AgencyReferenceFromDatabase(&agency)}
		references.Routes = routeRefs
		references.Stops = parentRefs
	}

	api.sendResponse(w, r, models.NewListResponse(stopsList, *references, false, api.Clock))
}
