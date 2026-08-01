package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
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
	params, fieldErrors := api.parseTripParams(r, TripParamDefaults{}, loc)
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
		status, _, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, serviceDate, currentTime)
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

	references := models.NewEmptyReferences()
	// When includeReferences=false the references block is present but empty.
	if ShouldIncludeReferences(r) {
		references, err = api.buildTripForVehicleReferences(ctx, agencyID, agency, trip, status, schedule, params.IncludeTrip)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.sendNotFound(w, r)
				return
			}
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	response := models.NewEntryResponse(entry, *references, api.Clock)
	api.sendResponse(w, r, response)
}

// buildTripForVehicleReferences dereferences the IDs the entry exposes: the
// agency, the stops named by the status and schedule blocks, the routes serving
// those stops, and — when includeTrip is set or a status block was built — the
// scheduled trip record.
func (api *RestAPI) buildTripForVehicleReferences(ctx context.Context, agencyID string, agency gtfsdb.Agency, trip gtfsdb.Trip, status *models.TripStatus, schedule *models.Schedule, includeTrip bool) (*models.ReferencesModel, error) {
	references := models.NewEmptyReferences()

	stopIDs, err := referencedStopIDs(status, schedule)
	if err != nil {
		return nil, err
	}

	stops, uniqueRouteMap, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, stopIDs)
	if err != nil {
		return nil, err
	}
	references.Stops = stops

	routeRefs := make(map[string]models.Route, len(uniqueRouteMap))
	for combinedID, route := range uniqueRouteMap {
		routeRefs[combinedID] = models.NewRoute(
			combinedID,
			route.AgencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String)
	}
	references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))

	// The active trip belongs in references whenever the status block is present,
	// so includeTrip is only the fallback: it decides the matter solely when no
	// status block was built.
	if includeTrip || status != nil {
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

		routeRef, err := api.routeReferenceByID(ctx, agencyID, trip.RouteID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.Logger.Warn("trip references non-existent route",
					"tripID", trip.ID, "routeID", trip.RouteID, "agencyID", agencyID)
			}
			return nil, err
		}
		routeRefs[utils.FormCombinedID(agencyID, trip.RouteID)] = routeRef
	}

	references.Routes = utils.MapValues(routeRefs)

	return references, nil
}

// referencedStopIDs returns the bare stop IDs that the entry refers to and so
// need dereferencing in the response: the status block's closest and next stops,
// plus every stop the schedule block lists. Both blocks carry combined
// {agencyID}_{stopID} identifiers; the reference builders take bare ones.
func referencedStopIDs(status *models.TripStatus, schedule *models.Schedule) ([]string, error) {
	var combinedIDs []string

	if status != nil {
		combinedIDs = append(combinedIDs, status.ClosestStop, status.NextStop)
	}
	if schedule != nil {
		for _, stopTime := range schedule.StopTimes {
			combinedIDs = append(combinedIDs, stopTime.StopID)
		}
	}

	stopIDs := make([]string, 0, len(combinedIDs))
	for _, combinedID := range combinedIDs {
		if combinedID == "" {
			continue
		}

		_, stopID, err := utils.ExtractAgencyIDAndCodeID(combinedID)
		if err != nil {
			return nil, err
		}
		stopIDs = append(stopIDs, stopID)
	}

	return stopIDs, nil
}
