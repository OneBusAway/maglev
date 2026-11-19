package restapi

import (
	"net/http"
	"strconv"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// tripHandler returns details for a single trip, including its route, stop times, and shape.
func (api *RestAPI) tripHandler(w http.ResponseWriter, r *http.Request) {
	includeReferences, referenceErrors := ShouldIncludeReferences(r, nil)
	if len(referenceErrors) > 0 {
		api.validationErrorResponse(w, r, referenceErrors)
		return
	}

	agencyID, id, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, id)
	if err != nil {
		api.dbErrorResponse(w, r, err)
		return
	}

	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, trip.RouteID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	if route.AgencyID != agencyID {
		api.sendNotFound(w, r)
		return
	}

	// route.AgencyID was just read from the database, so a failure here is a
	// server-side problem rather than a client asking for something absent.
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, route.AgencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	if trip.ID == "" {
		api.sendNull(w, r)
		return
	}

	blockID := utils.FormBlockID(agencyID, nulls.StringOrEmpty(trip.BlockID), trip.ID)
	var shapeID string
	if trip.ShapeID.Valid {
		shapeID = utils.FormCombinedID(agencyID, nulls.StringOrEmpty(trip.ShapeID))
	}

	tripModel := &models.Trip{
		ID:            utils.FormCombinedID(agencyID, trip.ID),
		RouteID:       utils.FormCombinedID(agencyID, trip.RouteID),
		ServiceID:     utils.FormCombinedID(agencyID, trip.ServiceID),
		DirectionID:   strconv.FormatInt(trip.DirectionID.Int64, 10),
		BlockID:       blockID,
		ShapeID:       shapeID,
		TripHeadsign:  nulls.StringOrEmpty(trip.TripHeadsign),
		TripShortName: nulls.StringOrEmpty(trip.TripShortName),
		// RouteShortName is the trip's own per-trip route short name (the GTFS
		// trips.txt route_short_name extension), not the route's short name.
		// Maglev does not store that column, so it is always empty — matching the
		// Java reference (BeanFactoryV2.getTrip reads trip.getRouteShortName()).
		// Clients fall back to the route's shortName in the references block.
		RouteShortName: "",
	}
	tripResponse := models.NewTripResponse(
		tripModel,
		"",
		0,
	)

	references := models.NewEmptyReferences()

	// includeReferences defaults to true; when explicitly false the references
	// block is returned with all sub-arrays empty (matches the Java reference).
	if includeReferences {
		references.Routes = append(references.Routes, models.NewRoute(
			utils.FormCombinedID(route.AgencyID, trip.RouteID),
			route.AgencyID,
			nulls.StringOrEmpty(route.ShortName),
			nulls.StringOrEmpty(route.LongName),
			nulls.StringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			nulls.StringOrEmpty(route.Url),
			nulls.StringOrEmpty(route.Color),
			nulls.StringOrEmpty(route.TextColor)))

		references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))
	}

	api.sendResponse(w, r, models.NewEntryResponse(tripResponse, *references, api.Clock))
}
