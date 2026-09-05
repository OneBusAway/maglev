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

	tripID, ok := api.getVehicleTripIDOr404(w, r, agencyID, vehicleID)
	if !ok {
		return
	}

	ctx := r.Context()

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

	// Fetch the trip's frequency rows once and share them with BuildTripStatus.
	freqRows, err := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrip(ctx, tripID)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	freqMap := map[string][]gtfsdb.Frequency{tripID: freqRows}

	var status *models.TripStatus
	var statusExtras *tripStatusExtras
	if params.IncludeStatus {
		var statusErr error
		status, statusExtras, statusErr = api.BuildTripStatus(ctx, agencyID, tripID, nil, serviceDate, currentTime, freqMap)
		if statusErr != nil {
			api.Logger.Warn("BuildTripStatus failed", "tripID", tripID, "error", statusErr)
			status = nil
		}
	}

	trip, ok := api.getTripOr404(ctx, w, r, agencyID, vehicleID, tripID)
	if !ok {
		return
	}

	var schedule *models.Schedule
	if params.IncludeSchedule {
		var scheduleErr error
		schedule, scheduleErr = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, loc)
		if scheduleErr != nil {
			api.serverErrorResponse(w, r, scheduleErr)
			return
		}
	}

	// BuildTripStatus above was given this same tripID, so when the status was
	// built its situations are this trip's and are reused here.
	situationIDs, situationRefs := api.tripSituationsFor(ctx, tripID, statusExtras)

	var frequency *models.Frequency
	if len(freqRows) > 0 {
		// TripDetails has one frequency field; take the window-matched row.
		converted := models.NewFrequencyFromDB(*selectFrequency(freqRows, serviceDate, currentTime), serviceDate)
		frequency = &converted
	}

	entry := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, tripID),
		ServiceDate:  models.NewModelTime(midnight),
		Frequency:    frequency,
		Status:       status,
		Schedule:     schedule,
		SituationIDs: situationIDs,
	}

	references := models.NewEmptyReferences()
	// When includeReferences=false the references block is present but empty.
	if ShouldIncludeReferences(r) {
		references, err = api.buildTripForVehicleReferences(ctx, agencyID, agency, trip, status, schedule, params.IncludeTrip)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
		references.Situations = situationRefs
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
		routeRefs[combinedID] = routeReferenceFromStopRow(route)
	}
	references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))

	// The active trip belongs in references whenever the status block is present,
	// so includeTrip is only the fallback: it decides the matter solely when no
	// status block was built.
	if includeTrip || status != nil {
		// The trip's routeId must match the ID its route reference is filed under,
		// which carries the route's own agency rather than the vehicle's. The
		// vehicle-prefixed ID below is only the fallback for an unresolvable route.
		combinedRouteID := utils.FormCombinedID(agencyID, trip.RouteID)

		routeRef, err := api.routeReferenceForTrip(ctx, trip.RouteID, uniqueRouteMap)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// A dangling trips.route_id costs one reference, not the response: the
			// vehicle and its trip both resolved, and the batch reference builders
			// likewise omit rows they cannot resolve rather than failing. With no row
			// to read an agency from, the routeId keeps the vehicle's prefix.
			api.Logger.Warn("trip references non-existent route",
				"tripID", trip.ID, "routeID", trip.RouteID, "agencyID", agencyID)
		case err != nil:
			return nil, err
		default:
			combinedRouteID = routeRef.ID
			routeRefs[routeRef.ID] = routeRef
			api.appendRouteAgencyReference(ctx, references, routeRef.AgencyID, agencyID)
		}

		tripRef := models.NewTripReference(
			utils.FormCombinedID(agencyID, trip.ID),
			combinedRouteID,
			utils.FormCombinedID(agencyID, trip.ServiceID),
			trip.TripHeadsign.String,
			trip.TripShortName.String,
			strconv.FormatInt(trip.DirectionID.Int64, 10),
			utils.FormCombinedID(agencyID, trip.BlockID.String),
			utils.FormCombinedID(agencyID, trip.ShapeID.String),
		)
		references.Trips = append(references.Trips, *tripRef)
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

func (api *RestAPI) getVehicleTripIDOr404(w http.ResponseWriter, r *http.Request, agencyID, vehicleID string) (string, bool) {
	vehicle, err := api.GtfsManager.GetVehicleByID(vehicleID)
	if err != nil {
		api.sendNotFound(w, r)
		return "", false
	}

	// Return 404 when vehicle has no associated trip (idle vehicle)
	// or when the trip ID is empty (avoiding a futile DB lookup)
	if vehicle == nil || vehicle.Trip == nil || vehicle.Trip.ID.ID == "" {
		api.Logger.Debug("vehicle has no current trip (idle)",
			"vehicleID", vehicleID, "agencyID", agencyID)
		api.sendNotFound(w, r)
		return "", false
	}
	return vehicle.Trip.ID.ID, true
}

func (api *RestAPI) getTripOr404(ctx context.Context, w http.ResponseWriter, r *http.Request, agencyID, vehicleID, tripID string) (gtfsdb.Trip, bool) {
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		// If the trip doesn't exist in our DB (sql.ErrNoRows), return 404 instead of 500
		if errors.Is(err, sql.ErrNoRows) {
			api.Logger.Warn("vehicle references non-existent trip",
				"vehicleID", vehicleID, "tripID", tripID, "agencyID", agencyID)
			api.sendNotFound(w, r)
			return gtfsdb.Trip{}, false
		}
		api.Logger.Error("database error fetching trip",
			"error", err,
			"tripID", tripID,
			"agencyID", agencyID)
		api.serverErrorResponse(w, r, err)
		return gtfsdb.Trip{}, false
	}
	return trip, true
}
