package restapi

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"maglev.onebusaway.org/internal/utils"
)

// vehiclePositionsForAgencyHandler serves the agency's vehicle positions as a
// GTFS-RT feed built from Maglev's retained realtime state.
func (api *RestAPI) vehiclePositionsForAgencyHandler(w http.ResponseWriter, r *http.Request) {
	req, ok := api.gtfsRealtimeExportRequestOrRespond(w, r)
	if !ok {
		return
	}
	candidates := selectExportVehicles(api.GtfsManager.ExportVehicles(), req)
	routeAgencies, err := api.vehicleRouteAgencies(r.Context(), candidates, req.RemoveAgencyIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	api.writeGtfsRealtimeFeed(w, r, req.Format, buildVehiclePositionsFeed(candidates, req, routeAgencies))
}

// vehicleRouteAgencies looks up the owners of active routes that have no
// block match. Only qualified output needs them.
func (api *RestAPI) vehicleRouteAgencies(ctx context.Context, candidates []vehicleExportCandidate, removeAgencyIDs bool) (map[string]string, error) {
	if removeAgencyIDs {
		return nil, nil
	}
	routeIDSet := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.vehicle.ActiveRouteID != "" {
			routeIDSet[candidate.vehicle.ActiveRouteID] = struct{}{}
		}
	}
	if len(routeIDSet) == 0 {
		return nil, nil
	}
	routes, err := utils.QueryInBatches(ctx, slices.Collect(maps.Keys(routeIDSet)), api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		return nil, err
	}
	routeAgencies := make(map[string]string, len(routes))
	for _, route := range routes {
		routeAgencies[route.ID] = route.AgencyID
	}
	return routeAgencies, nil
}
