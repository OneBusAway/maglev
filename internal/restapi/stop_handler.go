package restapi

import (
	"net/http"
	"sort"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// stopHandler returns details for a single stop, including its location and the routes that serve it.
func (api *RestAPI) stopHandler(w http.ResponseWriter, r *http.Request) {
	includeReferences, referenceErrors := ShouldIncludeReferences(r, nil)
	if len(referenceErrors) > 0 {
		api.validationErrorResponse(w, r, referenceErrors)
		return
	}

	agencyID, stopID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
	if err != nil {
		api.dbErrorResponse(w, r, err)
		return
	}
	if stop.ID == "" {
		api.sendNotFound(w, r)
		return
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStop(ctx, stopID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Sort routes naturally by ShortName
	utils.SortRoutesForStopRowsByName(routes)

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
		parentID = utils.FormCombinedID(agencyID, nulls.StringOrEmpty(stop.ParentStation))
	}

	stopData := &models.Stop{
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
		Name:               nulls.StringOrEmpty(stop.Name),
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		Code:               nulls.StringOrNonEmpty(stop.Code, stop.ID),
		Direction:          nulls.StringOrEmpty(stop.Direction),
		LocationType:       int(stop.LocationType.Int64),
		WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		RouteIDs:           combinedRouteIDs,
		StaticRouteIDs:     combinedRouteIDs,
		Parent:             parentID,
	}

	// Initialize empty references struct
	references := models.NewEmptyReferences()

	if includeReferences {
		uniqueRouteIDs := make(map[string]bool)

		// Add routes to references and collect unique agency IDs
		for _, route := range routes {
			combinedRouteID := utils.FormCombinedID(route.AgencyID, route.ID)

			// GetRoutesForStop is already DISTINCT, so there are no duplicates here.
			// This primarily seeds the dedup set for the parent station routes below.
			if !uniqueRouteIDs[combinedRouteID] {
				routeModel := models.NewRoute(
					combinedRouteID,
					route.AgencyID,
					nulls.StringOrEmpty(route.ShortName),
					nulls.StringOrEmpty(route.LongName),
					nulls.StringOrEmpty(route.Desc),
					models.RouteType(route.Type),
					nulls.StringOrEmpty(route.Url),
					nulls.StringOrEmpty(route.Color),
					nulls.StringOrEmpty(route.TextColor))

				references.Routes = append(references.Routes, routeModel)
				uniqueRouteIDs[combinedRouteID] = true
			}
		}

		if nulls.StringOrEmpty(stop.ParentStation) != "" {
			parentRefs, parentRoutesMap, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, []string{nulls.StringOrEmpty(stop.ParentStation)})
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			references.Stops = append(references.Stops, parentRefs...)

			for combinedRouteID, pr := range parentRoutesMap {
				// Only add if we haven't seen this route yet (Deduplication)
				if !uniqueRouteIDs[combinedRouteID] {
					routeModel := models.NewRoute(
						combinedRouteID,
						pr.AgencyID,
						nulls.StringOrEmpty(pr.ShortName),
						nulls.StringOrEmpty(pr.LongName),
						nulls.StringOrEmpty(pr.Desc),
						models.RouteType(pr.Type),
						nulls.StringOrEmpty(pr.Url),
						nulls.StringOrEmpty(pr.Color),
						nulls.StringOrEmpty(pr.TextColor))
					references.Routes = append(references.Routes, routeModel)
					uniqueRouteIDs[combinedRouteID] = true
					uniqueAgencyIDs[pr.AgencyID] = true
				}
			}
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
				api.dbErrorResponse(w, r, err)
				return
			}

			// Use the existing helper to map the database row to the model
			references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))
		}
	}

	response := models.NewEntryResponse(stopData, *references, api.Clock)
	api.sendResponse(w, r, response)
}
