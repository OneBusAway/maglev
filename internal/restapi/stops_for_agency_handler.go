package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// stopsForAgencyHandler returns all stops belonging to a given agency with full stop details.
func (api *RestAPI) stopsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	id, ok := api.extractAndValidateID(w, r)
	if !ok {
		return
	}

	// Validate agency exists
	agency, err := api.GtfsManager.FindAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if agency == nil {
		api.sendNotFound(w, r)
		return
	}

	// Get all stop IDs for the agency
	stopIDs, err := api.GtfsManager.GtfsDB.Queries.GetStopIDsForAgency(ctx, id)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Parent stations go to references.stops. Route references cover them too,
	// so a route that serves only the station still resolves.
	stopsList, parentRefs, routes, err := stopsAndParentStations(api, ctx, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	routeRefs := routeReferencesForStops(utils.MapValues(routes))

	// Build references
	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{models.AgencyReferenceFromDatabase(agency)}
	references.Routes = routeRefs
	references.Stops = append(references.Stops, parentRefs...)

	// A route in routeRefs can belong to a different agency than the one
	// requested here (a stop can be served by more than one agency's routes).
	// Every such route's agencyId must resolve against references.agencies too.
	for _, route := range routeRefs {
		api.appendRouteAgencyReference(ctx, references, route.AgencyID, id)
	}

	response := models.NewListResponse(stopsList, *references, false, api.Clock)
	api.sendResponse(w, r, response)
}
