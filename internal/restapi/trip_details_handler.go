package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// TripParams holds the common query parameters for trip-related endpoints
// (trip-details, trip-for-vehicle, etc.).
type TripParams struct {
	ServiceDate     *time.Time
	IncludeTrip     bool
	IncludeSchedule bool
	IncludeStatus   bool
	Time            *time.Time
	VehicleID       string
}

// TripParamDefaults holds the values the include* params take when the request
// omits them. They differ per endpoint: trip-details defaults includeTrip and
// includeSchedule to true, trip-for-vehicle defaults both to false.
type TripParamDefaults struct {
	IncludeTrip     bool
	IncludeSchedule bool
}

// Layouts accepted by the serviceDate and time params, alongside Unix millis.
const (
	serviceDateLayout = "2006-01-02"          // e.g. "2024-06-15"
	tripTimeLayout    = "2006-01-02_15-04-05" // e.g. "2024-06-15_14-30-00"
)

const (
	errInvalidServiceDate = "must be a valid Unix timestamp in milliseconds or a date in yyyy-MM-dd format"
	errInvalidTime        = "must be a valid Unix timestamp in milliseconds or a datetime in yyyy-MM-dd_HH-mm-ss format"
)

// parseEpochOrLayoutTime parses a param accepting either a Unix timestamp in
// milliseconds or a timestamp in the given layout. A missing param yields a nil
// time and no error; ok is false only when a supplied value matches neither form.
func parseEpochOrLayoutTime(value, layout string, loc *time.Location) (parsed *time.Time, ok bool) {
	if value == "" {
		return nil, true
	}

	if epochMillis, err := strconv.ParseInt(value, 10, 64); err == nil {
		fromEpoch := time.UnixMilli(epochMillis)
		return &fromEpoch, true
	}

	fromLayout, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		return nil, false
	}

	return &fromLayout, true
}

// localizeTripTimes re-expresses the parsed times in loc, so that downstream
// Year()/Month()/Day()/Format() calls extract the agency's calendar date rather
// than UTC's — time.Unix() alone would land those endpoints a day out for
// agencies at a positive UTC offset.
func localizeTripTimes(params *TripParams, loc *time.Location) {
	if params.ServiceDate != nil {
		localized := params.ServiceDate.In(loc)
		params.ServiceDate = &localized
	}
	if params.Time != nil {
		localized := params.Time.In(loc)
		params.Time = &localized
	}
}

// resolveLocation returns the caller-supplied timezone, or UTC when absent.
func resolveLocation(loc ...*time.Location) *time.Location {
	if len(loc) > 0 && loc[0] != nil {
		return loc[0]
	}
	return time.UTC
}

// parseTimeField parses the time-valued query param key into a timestamp,
// recording a field error when it matches neither Unix millis nor layout.
func parseTimeField(query url.Values, key, layout, errMsg string, loc *time.Location, fieldErrors map[string][]string) *time.Time {
	parsed, ok := parseEpochOrLayoutTime(query.Get(key), layout, loc)
	if !ok {
		fieldErrors[key] = []string{errMsg}
		return nil
	}
	return parsed
}

// parseTripParams parses and validates the common trip query params, applying
// the caller's per-endpoint defaults to any include* param the request omits.
func (api *RestAPI) parseTripParams(r *http.Request, defaults TripParamDefaults, loc ...*time.Location) (TripParams, map[string][]string) {
	query := r.URL.Query()

	// Timestamps without an explicit offset are read in the agency's timezone when
	// the caller supplies one, and in UTC otherwise.
	parseLoc := resolveLocation(loc...)

	params := TripParams{
		IncludeTrip:     defaults.IncludeTrip,
		IncludeSchedule: defaults.IncludeSchedule,
		IncludeStatus:   true,
		VehicleID:       query.Get("vehicleId"),
	}

	fieldErrors := make(map[string][]string)

	params.ServiceDate = parseTimeField(query, "serviceDate", serviceDateLayout, errInvalidServiceDate, parseLoc, fieldErrors)
	params.Time = parseTimeField(query, "time", tripTimeLayout, errInvalidTime, parseLoc, fieldErrors)

	params.IncludeTrip, fieldErrors = utils.ParseBoolParam(query, "includeTrip", params.IncludeTrip, fieldErrors)
	params.IncludeSchedule, fieldErrors = utils.ParseBoolParam(query, "includeSchedule", params.IncludeSchedule, fieldErrors)
	params.IncludeStatus, fieldErrors = utils.ParseBoolParam(query, "includeStatus", params.IncludeStatus, fieldErrors)

	if len(fieldErrors) > 0 {
		return params, fieldErrors
	}

	localizeTripTimes(&params, parseLoc)

	return params, nil
}

// tripDetailsHandler returns extended information for a trip, including its schedule,
// real-time status, and optionally the full stop time sequence.
func (api *RestAPI) tripDetailsHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, tripID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
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
	defaults := TripParamDefaults{IncludeTrip: true, IncludeSchedule: true}
	params, fieldErrors := api.parseTripParams(r, defaults, loc)
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

	// When serviceDate is explicitly provided, validate that the trip operates on
	// that date. Per the wiki spec: "serviceDate is provided but no
	// block instance exists for that service date → HTTP 404".
	if params.ServiceDate != nil {
		formattedDate := serviceDate.Format("20060102")
		activeServiceIDs, svcErr := api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
		if svcErr != nil {
			api.serverErrorResponse(w, r, svcErr)
			return
		}
		serviceActive := false
		for _, svcID := range activeServiceIDs {
			if svcID == trip.ServiceID {
				serviceActive = true
				break
			}
		}
		if !serviceActive {
			api.sendNotFound(w, r)
			return
		}
	}

	var requestedVehicle *gtfs.Vehicle
	if params.VehicleID != "" {
		vehicleAgencyID, rawVehicleID, vErr := utils.ExtractAgencyIDAndCodeID(params.VehicleID)
		if vErr != nil {
			api.sendNotFound(w, r)
			return
		}
		if vehicleAgencyID != agencyID {
			api.sendNotFound(w, r)
			return
		}
		v, vErr := api.GtfsManager.GetVehicleByID(rawVehicleID)
		if vErr != nil || v == nil {
			api.sendNotFound(w, r)
			return
		}
		// The trip-details endpoint must not accept a vehicle currently on
		// a different trip. Java's TripDetailsBeanService restricts the
		// vehicle argument to the trip we're describing; a mismatch is 404
		// so the response never conflates two unrelated trips.
		if v.Trip == nil || v.Trip.ID.ID != trip.ID {
			api.sendNotFound(w, r)
			return
		}
		requestedVehicle = v
	}

	var schedule *models.Schedule
	var status *models.TripStatus
	var statusExtras *tripStatusExtras

	if params.IncludeStatus {
		var statusErr error
		status, statusExtras, statusErr = api.BuildTripStatus(ctx, agencyID, trip.ID, requestedVehicle, serviceDate, currentTime, nil)
		if statusErr != nil {
			api.serverErrorResponse(w, r, statusErr)
			return
		}

		// Extension 4e: Explicitly nil out the status if there is no actual tracking record.
		// BuildTripStatus returns a default placeholder when tracking is absent, so we nil it to trigger JSON omitempty.
		if status != nil && status.IsUntracked() {
			status = nil
		}
	}

	if params.IncludeSchedule {
		schedule, err = api.BuildTripSchedule(ctx, agencyID, serviceDate, &trip, loc)
		if err != nil {
			api.Logger.Warn("BuildTripSchedule failed",
				"trip_id", trip.ID,
				"error", err.Error())
			schedule = nil
		}
	}

	// trip is looked up by tripID and BuildTripStatus was given trip.ID, so when
	// the status was built its situations are this trip's and are reused here.
	situationsIDs, situationRefs := api.tripSituationsFor(ctx, tripID, statusExtras)

	freqRows, err := api.GtfsManager.GtfsDB.Queries.GetFrequenciesForTrip(ctx, tripID)
	if err != nil {
		api.Logger.Warn("GetFrequenciesForTrip failed",
			"trip_id", tripID,
			"error", err.Error())
		freqRows = nil
	}

	var frequency *models.Frequency
	if len(freqRows) > 0 {
		// TripDetails has only one frequency field, but GetFrequenciesForTrip query can return multiple rows
		// when there are multiple frequency entries for the same trip. In order to adhere to the API contract,
		// we take the first row which gives us the frequency with the earliest start_time
		converted := models.NewFrequencyFromDB(freqRows[0], serviceDate)
		frequency = &converted
	}

	tripDetails := &models.TripDetails{
		TripID:       utils.FormCombinedID(agencyID, trip.ID),
		ServiceDate:  models.NewModelTime(midnight),
		Schedule:     schedule,
		Frequency:    frequency,
		SituationIDs: situationsIDs,
	}

	if status != nil {
		tripDetails.Status = status
	}

	references := models.NewEmptyReferences()

	includeReferences := ShouldIncludeReferences(r)

	if includeReferences {
		tripsToInclude := []string{}

		// Only include the main trip if includeTrip=true.
		// Related trips (preceding/following/active) are appended independently.
		if params.IncludeTrip {
			tripsToInclude = append(tripsToInclude, utils.FormCombinedID(agencyID, trip.ID))
		}

		if params.IncludeSchedule && schedule != nil {
			if schedule.NextTripID != "" {
				tripsToInclude = append(tripsToInclude, schedule.NextTripID)
			}
			if schedule.PreviousTripID != "" {
				tripsToInclude = append(tripsToInclude, schedule.PreviousTripID)
			}
		}

		if params.IncludeStatus && status != nil && status.ActiveTripID != "" {
			tripsToInclude = append(tripsToInclude, status.ActiveTripID)
		}

		if len(tripsToInclude) > 0 {
			referencedTrips, err := api.buildReferencedTrips(ctx, agencyID, tripsToInclude, trip)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}

			for _, t := range referencedTrips {
				references.Trips = append(references.Trips, *t)
			}
		}

		agencyModel := models.AgencyReferenceFromDatabase(&agency)
		references.Agencies = append(references.Agencies, agencyModel)

		references.Situations = append(references.Situations, situationRefs...)

		if params.IncludeSchedule && schedule != nil {
			stopIDs := make([]string, 0, len(schedule.StopTimes))
			for _, st := range schedule.StopTimes {
				_, rawStopID, err := utils.ExtractAgencyIDAndCodeID(st.StopID)
				if err != nil {
					continue
				}
				stopIDs = append(stopIDs, rawStopID)
			}

			stops, _, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, stopIDs)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}
			references.Stops = stops

			routes, err := api.BuildRouteReferences(ctx, agencyID, stops)
			if err != nil {
				api.serverErrorResponse(w, r, err)
				return
			}

			references.Routes = routes
		}
	}

	response := models.NewEntryResponse(tripDetails, *references, api.Clock)
	api.sendResponse(w, r, response)
}

func (api *RestAPI) buildReferencedTrips(ctx context.Context, agencyID string, tripsToInclude []string, mainTrip gtfsdb.Trip) ([]*models.Trip, error) {
	referencedTrips := []*models.Trip{}

	// extract unique trip IDs for the batch fetch
	uniqueTripIDs := make([]string, 0, len(tripsToInclude))
	seen := make(map[string]bool)
	type tripEntry struct {
		combinedID string
		refTripID  string
	}
	orderedEntries := make([]tripEntry, 0, len(tripsToInclude))

	for _, tripID := range tripsToInclude {
		_, refTripID, err := utils.ExtractAgencyIDAndCodeID(tripID)
		if err != nil {
			continue
		}
		orderedEntries = append(orderedEntries, tripEntry{combinedID: tripID, refTripID: refTripID})
		if !seen[refTripID] {
			seen[refTripID] = true
			uniqueTripIDs = append(uniqueTripIDs, refTripID)
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// batch fetch
	batchedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, uniqueTripIDs)
	if err != nil {
		return referencedTrips, fmt.Errorf("batch fetch trips: %w", err)
	}

	tripMap := make(map[string]gtfsdb.Trip)
	routeIDSet := make(map[string]bool)
	for _, t := range batchedTrips {
		tripMap[t.ID] = t
		routeIDSet[t.RouteID] = true
	}

	// batch fetch
	routeIDs := make([]string, 0, len(routeIDSet))
	for rid := range routeIDSet {
		routeIDs = append(routeIDs, rid)
	}

	batchedRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, routeIDs)
	if err != nil {
		return referencedTrips, fmt.Errorf("batch fetch routes: %w", err)
	}

	routeMap := make(map[string]gtfsdb.Route)
	for _, rt := range batchedRoutes {
		routeMap[rt.ID] = rt
	}

	for _, entry := range orderedEntries {
		if entry.refTripID == mainTrip.ID && len(referencedTrips) > 0 {
			continue
		}

		refTrip, tripExists := tripMap[entry.refTripID]
		if !tripExists {
			continue
		}

		refRoute, routeExists := routeMap[refTrip.RouteID]
		if !routeExists {
			continue
		}

		var blockID string
		if refTrip.BlockID.Valid && refTrip.BlockID.String != "" {
			blockID = utils.FormCombinedID(agencyID, refTrip.BlockID.String)
		}

		refTripModel := &models.Trip{
			ID:             entry.combinedID,
			RouteID:        utils.FormCombinedID(agencyID, refTrip.RouteID),
			ServiceID:      utils.FormCombinedID(agencyID, refTrip.ServiceID),
			ShapeID:        utils.FormCombinedID(agencyID, refTrip.ShapeID.String),
			TripHeadsign:   refTrip.TripHeadsign.String,
			TripShortName:  refTrip.TripShortName.String,
			DirectionID:    strconv.FormatInt(refTrip.DirectionID.Int64, 10),
			BlockID:        blockID,
			RouteShortName: refRoute.ShortName.String,
			TimeZone:       "",
			PeakOffPeak:    0,
		}

		referencedTrips = append(referencedTrips, refTripModel)
	}

	return referencedTrips, nil
}
