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
	utils.SortRoutesByName(routes)

	combinedRouteIDs := make([]string, len(routes))
	uniqueAgencyIDs := make(map[string]bool)

	for i, route := range routes {
		// Use route.AgencyID, not the stop's agencyID.
		// A stop can be served by routes from other agencies.
		combinedRouteIDs[i] = utils.FormCombinedID(route.AgencyID, route.ID)
		uniqueAgencyIDs[route.AgencyID] = true
	}

	// Validate the requested agency namespace
	if len(routes) > 0 {
		if !uniqueAgencyIDs[agencyID] {
			// Stop exists, but is not served by the requested agency namespace.
			api.sendNotFound(w, r)
			return
		}
	} else {
		// If the stop has no routes, we allow it to be retrieved under any valid agency namespace
		// because Maglev stops do not have a dedicated agency_id column in the schema.
		// Just ensure the requested agency actually exists.
		agency, err := api.GtfsManager.FindAgency(ctx, agencyID)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		if agency == nil {
			api.sendNotFound(w, r)
			return
		}
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
			parentStop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stop.ParentStation.String)
			if err == nil {
				parentRoutes, _ := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, parentStop.ID)

				// Sort parent routes naturally by ShortName
				utils.SortRoutesByName(parentRoutes)

				parentRouteIDs := make([]string, len(parentRoutes))
				for i, r := range parentRoutes {
					parentRouteIDs[i] = utils.FormCombinedID(r.AgencyID, r.ID)
				}
				references.Stops = append(references.Stops, models.Stop{
					ID:                 utils.FormCombinedID(agencyID, parentStop.ID),
					Name:               nulls.StringOrEmpty(parentStop.Name),
					Lat:                parentStop.Lat,
					Lon:                parentStop.Lon,
					Code:               nulls.StringOrDefault(parentStop.Code, parentStop.ID),
					Direction:          nulls.StringOrEmpty(parentStop.Direction),
					LocationType:       int(nulls.Int64OrDefault(parentStop.LocationType, 0)),
					WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(parentStop.WheelchairBoarding)),
					RouteIDs:           parentRouteIDs,
					StaticRouteIDs:     parentRouteIDs,
				})
			}
		}
	}

	response := models.NewEntryResponse(stopData, *references, api.Clock)
	api.sendResponse(w, r, response)
}
