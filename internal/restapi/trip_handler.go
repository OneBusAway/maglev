package restapi

import (
	"net/http"
	"strconv"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tripHandler returns details for a single trip, including its route, stop times, and shape.
func (api *RestAPI) tripHandler(w http.ResponseWriter, r *http.Request) {
	includeReferences := r.URL.Query().Get("includeReferences") != "false"
	if version := r.URL.Query().Get("version"); version != "" && version != "2" {
		api.sendError(w, r, http.StatusInternalServerError, "unknown version: "+version)
		return
	}

	agencyID, id, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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

	var blockID, shapeID string
	if trip.BlockID.Valid {
		blockID = utils.FormCombinedID(agencyID, trip.BlockID.String)
	}
	if trip.ShapeID.Valid {
		shapeID = utils.FormCombinedID(agencyID, trip.ShapeID.String)
	}

	tripModel := &models.Trip{
		ID:             utils.FormCombinedID(agencyID, trip.ID),
		RouteID:        utils.FormCombinedID(agencyID, trip.RouteID),
		ServiceID:      utils.FormCombinedID(agencyID, trip.ServiceID),
		DirectionID:    strconv.FormatInt(trip.DirectionID.Int64, 10),
		BlockID:        blockID,
		ShapeID:        shapeID,
		TripHeadsign:   trip.TripHeadsign.String,
		TripShortName:  trip.TripShortName.String,
		RouteShortName: route.ShortName.String,
	}

	tripEntry := map[string]any{
		"id":             tripModel.ID,
		"routeId":        tripModel.RouteID,
		"routeShortName": tripModel.RouteShortName,
		"tripHeadsign":   tripModel.TripHeadsign,
		"tripShortName":  tripModel.TripShortName,
		"directionId":    tripModel.DirectionID,
		"serviceId":      tripModel.ServiceID,
		"shapeId":        tripModel.ShapeID,
		"blockId":        tripModel.BlockID,
		"peakOffpeak":    tripModel.PeakOffPeak,
	}

	references := models.NewEmptyReferences()

	references.Routes = append(references.Routes, models.NewRoute(
		utils.FormCombinedID(route.AgencyID, trip.RouteID),
		route.AgencyID,
		route.ShortName.String,
		route.LongName.String,
		route.Desc.String,
		models.RouteType(route.Type),
		route.Url.String,
		route.Color.String,
		route.TextColor.String))

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

	if !includeReferences {
		api.sendResponse(w, r, models.NewOKResponse(map[string]any{"entry": tripEntry}, api.Clock))
		return
	}

	api.sendResponse(w, r, models.NewEntryResponse(tripEntry, *references, api.Clock))
}
