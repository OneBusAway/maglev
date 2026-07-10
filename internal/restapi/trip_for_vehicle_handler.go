package restapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// tripForVehicleHandler returns trip details for the trip currently being served by a given vehicle.
func (api *RestAPI) tripForVehicleHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, vehicleID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	vehicle, err := api.GtfsManager.GetVehicleByID(vehicleID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	// Return 404 when vehicle has no associated trip (idle vehicle)
	// or when the trip ID is empty (avoiding a futile DB lookup)
	if vehicle == nil || vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
		api.Logger.Debug("vehicle has no current trip (idle)",
			"vehicleID", vehicleID, "agencyID", agencyID)
		api.sendNotFound(w, r)
		return
	}

	ctx := r.Context()

	tripID := vehicle.Trip.ID.ID

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Parse query params with the agency's timezone so that serviceDate and time
	// are localized at parse time, preventing UTC date-extraction bugs.
	params, fieldErrors := api.parseTripParams(r, false, loc)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	var currentTime time.Time
	if params.Time != nil {
		currentTime = *params.Time
	} else {
		currentTime = api.Clock.Now().In(loc)
	}

	serviceDate, midnight := utils.ServiceDateMidnight(params.ServiceDate, currentTime)

	var status *models.TripStatus
	if params.IncludeStatus {
		var statusErr error
		status, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, serviceDate, currentTime)
		if statusErr != nil {
			api.Logger.Warn("failed to build trip status",
				"tripID", tripID,
				"agencyID", agencyID,
				"error", statusErr)
			status = nil
		}
	}

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		// If the trip doesn't exist in our DB (sql.ErrNoRows), return 404 instead of 500
		if errors.Is(err, sql.ErrNoRows) {
			api.Logger.Warn("vehicle references non-existent trip",
				"vehicleID", vehicleID, "tripID", tripID, "agencyID", agencyID)
			api.sendNotFound(w, r)
			return
		}
		api.Logger.Error("database error fetching trip",
			"error", err,
			"tripID", tripID,
			"agencyID", agencyID)
		api.serverErrorResponse(w, r, err)
		return
	}

	var schedule *models.Schedule
	if params.IncludeSchedule {
		var scheduleErr error
		schedule, scheduleErr = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, loc)
		if scheduleErr != nil {
			api.Logger.Warn("failed to build trip schedule",
				"tripID", tripID,
				"agencyID", agencyID,
				"error", scheduleErr)
		}
	}

	var situationIDs []string
	if status != nil && len(status.SituationIDs) > 0 {
		situationIDs = status.SituationIDs
	} else {
		situationIDs = api.GetSituationIDsForTrip(r.Context(), tripID)
	}

	entry := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, tripID),
		ServiceDate:  models.NewModelTime(midnight),
		Frequency:    nil,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: situationIDs,
	}

	// Build references
	references := models.NewEmptyReferences()

	agencyModel := models.AgencyReferenceFromDatabase(&agency)

	stopIDs := []string{}

	if status != nil {
		if status.ClosestStop != "" {
			_, closestStopID, err := utils.ExtractAgencyIDAndCodeID(status.ClosestStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, closestStopID)
		}
		if status.NextStop != "" {
			_, nextStopID, err := utils.ExtractAgencyIDAndCodeID(status.NextStop)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			stopIDs = append(stopIDs, nextStopID)
		}
	}
	stops, uniqueRouteMap, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, stopIDs)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references.Stops = stops

	routeRefs := make(map[string]models.Route, len(uniqueRouteMap))
	for combinedID, route := range uniqueRouteMap {
		routeRefs[combinedID] = models.NewRoute(
			utils.FormCombinedID(agencyID, route.ID),
			agencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String)
	}
	references.Routes = utils.MapValues(routeRefs)

	references.Agencies = append(references.Agencies, agencyModel)

	if params.IncludeTrip {
		tripRef := models.NewTripReference(
			utils.FormCombinedID(agencyID, trip.ID),
			utils.FormCombinedID(agencyID, trip.RouteID),
			utils.FormCombinedID(agencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			trip.TripShortName.String,
			strconv.FormatInt(trip.DirectionID.Int64, 10),
			utils.FormCombinedID(agencyID, trip.BlockID.String),
			utils.FormCombinedID(agencyID, trip.ShapeID.String),
		)
		references.Trips = append(references.Trips, *tripRef)
	}

	response := models.NewEntryResponse(entry, *references, api.Clock)
	api.sendResponse(w, r, response)
}
