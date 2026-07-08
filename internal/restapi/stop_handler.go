package restapi

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// stopHandler returns details for a single stop, including its location and the routes that serve it.
func (api *RestAPI) stopHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, stopID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil || stop.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Sort routes naturally by ShortName
	utils.SortRoutesByName(routes, utils.RouteRowSortKey)

	combinedRouteIDs := make([]string, len(routes))
	for i, route := range routes {
		// Use route.AgencyID, not the stop's agencyID.
		// A stop can be served by routes from other agencies.
		combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)
	}

	parentID := ""
	if nulls.StringOrEmpty(stop.ParentStation) != "" {
		parentID = utils.FormCombinedID(agencyID, stop.ParentStation.String)
	}

	stopData := &models.Stop{
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
		Name:               nulls.StringOrEmpty(stop.Name),
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		Code:               nulls.StringOrDefault(stop.Code, stop.ID),
		Direction:          nulls.StringOrEmpty(stop.Direction),
		LocationType:       int(stop.LocationType.Int64),
		WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		RouteIDs:           combinedRouteIDs,
		StaticRouteIDs:     combinedRouteIDs,
		Parent:             parentID,
	}

	// Initialize empty references struct
	references := models.NewEmptyReferences()

	// Only populate references if the query parameter is absent or true
	if ShouldIncludeReferences(r) {
		uniqueAgencyIDs := make(map[string]bool)

		// Add routes to references and collect unique agency IDs
		for _, route := range routes {
			routeModel := models.NewRoute(
				utils.FormCombinedID(route.AgencyID, route.ID),
				route.AgencyID,
				route.ShortName.String,
				route.LongName.String,
				route.Desc.String,
				models.RouteType(route.Type),
				route.Url.String,
				route.Color.String,
				route.TextColor.String)

			references.Routes = append(references.Routes, routeModel)
			uniqueAgencyIDs[route.AgencyID] = true
		}

		// Fetch references for ALL unique agencies involved, not just the first one.
		agencyIDs := make([]string, 0, len(uniqueAgencyIDs))
		for aid := range uniqueAgencyIDs {
			agencyIDs = append(agencyIDs, aid)
		}

		sort.Strings(agencyIDs)

		for _, aid := range agencyIDs {
			agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, aid)

			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					api.sendNotFound(w, r)
					return
				}
				api.serverErrorResponse(w, r, err)
				return
			}

			// Use the existing helper to map the database row to the model
			references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))

		}

		if nulls.StringOrEmpty(stop.ParentStation) != "" {
			parentRefs, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, []string{stop.ParentStation.String})
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			references.Stops = append(references.Stops, parentRefs...)
		}
	}

	response := models.NewEntryResponse(stopData, *references, api.Clock)
	api.sendResponse(w, r, response)
}
