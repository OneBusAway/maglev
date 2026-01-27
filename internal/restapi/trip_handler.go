package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) tripHandler(w http.ResponseWriter, r *http.Request) {
	queryParamID := utils.ExtractIDFromParams(r)

	// Validate ID
	if err := utils.ValidateID(queryParamID); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, id, err := utils.ExtractAgencyIDAndCodeID(queryParamID)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, id)
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

	if trip.ID == "" {
		api.sendNull(w, r)
		return
	}

	tripModel := &models.Trip{
		ID:             utils.FormCombinedID(agencyID, trip.ID),
		RouteID:        utils.FormCombinedID(agencyID, trip.RouteID),
		ServiceID:      utils.FormCombinedID(agencyID, trip.ServiceID),
		DirectionID:    trip.DirectionID.Int64,
		BlockID:        utils.FormCombinedID(agencyID, trip.BlockID.String),
		ShapeID:        utils.FormCombinedID(agencyID, trip.ShapeID.String),
		TripHeadsign:   trip.TripHeadsign.String,
		TripShortName:  trip.TripShortName.String,
		RouteShortName: route.ShortName.String,
	}
	tripResponse := models.NewTripResponse(
		tripModel,
		"",
		0,
	)

	references := models.NewEmptyReferences()

	references.Routes = append(references.Routes, models.NewRoute(
		utils.FormCombinedID(agencyID, trip.RouteID),
		route.AgencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String,
		route.ShortName.String,
	))

	references.Agencies = append(references.Agencies, models.NewAgencyReference(
		agency.ID,
		agency.Name,
		agency.Url,
		agency.Timezone,
		agency.Lang.String,
		agency.Phone.String,
		agency.Email.String,
		agency.FareUrl.String,
		"",
		false,
	))

	api.sendResponse(w, r, models.NewEntryResponse(tripResponse, references, api.Clock))
}
