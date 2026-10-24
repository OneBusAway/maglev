package restapi

import (
	"context"
	"net/http"
	"slices"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Define params structure for the plural handler
type ArrivalsStopParams struct {
	After  time.Duration
	Before time.Duration
	Time   time.Time
}

// parseArrivalsAndDeparturesParams parses and validates parameters.
func (api *RestAPI) parseArrivalsAndDeparturesParams(r *http.Request) (ArrivalsStopParams, map[string][]string) {
	// Cap at one service day. Java doesn't cap; we do to bound per-request
	// work — the handler iterates only ±1 day of stop_times anyway.
	const maxBefore = 24 * time.Hour
	const maxAfter = 24 * time.Hour

	params := ArrivalsStopParams{
		After:  35 * time.Minute, // Default
		Before: 5 * time.Minute,  // Default
		Time:   api.Clock.Now(),  // Default to current time
	}

	var fieldErrors map[string][]string

	addError := func(field, msg string) {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		fieldErrors[field] = append(fieldErrors[field], msg)
	}

	query := r.URL.Query()

	params.After = parseMinutesValue(query, "minutesAfter", params.After, maxAfter, addError)
	params.Before = parseMinutesValue(query, "minutesBefore", params.Before, maxBefore, addError)
	params.Time = parseEpochMillisValue(query, "time", params.Time, addError)

	return params, fieldErrors
}

func (api *RestAPI) arrivalsAndDeparturesForStopHandler(w http.ResponseWriter, r *http.Request) {
	stopAgencyID, stopCode, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}
	stopID := utils.FormCombinedID(stopAgencyID, stopCode)

	// Install a request-scoped snapshot cache so BuildTripStatus, called
	// per-arrival, shares block snapshots across every row whose trip is
	// in the same shift. Without this each row pays for the full compute
	// chain (blockTripIDsForServiceDate + loadBlockTripData + emitBlock
	// Stops), amplifying to thousands of round-trips on wide time windows.
	ctx := WithSnapshotCache(r.Context(), newSnapshotCache())

	// Capture parsing errors
	params, fieldErrors := api.parseArrivalsAndDeparturesParams(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	if !api.requireStopAgency(w, r, stopAgencyID, stopCode) {
		return
	}

	stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopCode)
	if err != nil {
		api.dbErrorResponse(w, r, err)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, stopAgencyID)
	if err != nil {
		// Unknown agency (e.g. wrong-case agency in the URL) is a client
		// error — surface 404 instead of 500.
		api.dbErrorResponse(w, r, err)
		return
	}

	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	params.Time = params.Time.In(loc)

	acc := newArrivalsAccumulator(stopAgencyID)

	result, err := api.arrivalsForStop(ctx, stopArrivalsInput{
		StopCode:  stopCode,
		AgencyID:  stopAgencyID,
		Location:  loc,
		QueryTime: params.Time,
		Before:    params.Before,
		After:     params.After,
	}, acc)
	if err != nil {
		api.sendArrivalsError(w, r, ctx, err)
		return
	}

	// Sort after buildArrival so predicted times are already filled in.
	sortArrivalsByBestTime(result.Arrivals)

	// Java names the stop and every stop near it in references, whether or not
	// the window has arrivals.
	nearbyIDs := api.nearbyStopIDs(ctx, stop)
	acc.stopIDs[stopCode] = true
	for _, nearbyID := range nearbyIDs {
		acc.stopIDs[nearbyID] = true
	}

	references, err := api.buildArrivalsReferences(ctx, &agency, acc)
	if err != nil {
		api.sendArrivalsError(w, r, ctx, err)
		return
	}
	nearbyAgencies, err := api.loadStopAgencyIDs(ctx, nearbyIDs)
	if err != nil {
		api.sendArrivalsError(w, r, ctx, err)
		return
	}

	acc.situations.add(api.GtfsManager.GetAlertsForStop(stopCode), acc.alertAgencyID)
	references.Situations = append(references.Situations, api.situationReferences(ctx, acc.situations.refs)...)

	// The top-level list covers every alert reachable from this stop, whether it
	// was matched through an arrival's trip or through the stop itself.
	topLevelSituationIDs := situationIDsFromRefs(acc.situations.refs)

	nearbyStopIDs := nearbyAgencies.combinedStopIDs(nearbyIDs)
	response := models.NewArrivalsAndDepartureResponse(result.Arrivals, *references, nearbyStopIDs, topLevelSituationIDs, stopID, api.Clock)
	api.sendResponse(w, r, response)
}

// nearbyStopIDs returns the raw IDs of every stop within 100 m of stop, served
// or not and stations included, leaving out stop itself. This mirrors Java's
// NearbyStopsBeanServiceImpl, which StopWithArrivalsAndDeparturesBeanServiceImpl
// calls with a 100 m radius and no count limit. LatSpan and LonSpan stay zero so
// BoundsFromParams uses Radius; a span would take precedence in degrees.
func (api *RestAPI) nearbyStopIDs(ctx context.Context, stop gtfsdb.Stop) []string {
	const nearbyRadiusMeters = 100
	const noLimit = 0
	loc := &internalgtfs.LocationParams{Lat: stop.Lat, Lon: stop.Lon, Radius: nearbyRadiusMeters}
	return slices.DeleteFunc(api.GtfsManager.GetStopIDsWithinBounds(ctx, loc, noLimit), func(id string) bool {
		return id == stop.ID
	})
}
