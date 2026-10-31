package restapi

import (
	"context"
	"net/http"
	"time"

	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Define params structure for the plural handler
type ArrivalsStopParams struct {
	After           time.Duration
	Before          time.Duration
	FrequencyBefore time.Duration
	FrequencyAfter  time.Duration
	Time            time.Time
	TimeParam       string
}

// parseArrivalsAndDeparturesParams parses and validates parameters.
func (api *RestAPI) parseArrivalsAndDeparturesParams(r *http.Request) (ArrivalsStopParams, map[string][]string) {
	// Cap at one service day. Java doesn't cap; we do to bound per-request
	// work — the handler iterates only ±1 day of stop_times anyway.
	const maxBefore = 24 * time.Hour
	const maxAfter = 24 * time.Hour

	params := ArrivalsStopParams{
		After:           35 * time.Minute, // Default
		Before:          5 * time.Minute,  // Default
		FrequencyBefore: defaultFrequencyBefore,
		FrequencyAfter:  defaultFrequencyAfter,
		Time:            api.Clock.Now(), // Default to current time
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
	params.FrequencyBefore = parseMinutesValue(query, "frequencyMinutesBefore", params.FrequencyBefore, maxArrivalWindow, addError)
	params.FrequencyAfter = parseMinutesValue(query, "frequencyMinutesAfter", params.FrequencyAfter, maxArrivalWindow, addError)

	params.TimeParam, fieldErrors = utils.ValidateArrivalTimeParam(query.Get("time"), fieldErrors)
	if params.TimeParam != "" {
		if ms, ok := utils.ParseEpochMillis(params.TimeParam); ok {
			params.Time = time.UnixMilli(ms)
		}
	}

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
	params.Time = utils.ResolveArrivalTime(params.TimeParam, loc, api.Clock.Now())

	acc := newArrivalsAccumulator(stopAgencyID)
	references := models.NewEmptyReferences()
	references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))

	result, err := api.arrivalsForStop(ctx, stopArrivalsInput{
		StopCode:        stopCode,
		AgencyID:        stopAgencyID,
		Location:        loc,
		QueryTime:       params.Time,
		Before:          params.Before,
		After:           params.After,
		FrequencyBefore: params.FrequencyBefore,
		FrequencyAfter:  params.FrequencyAfter,
	}, acc)
	if err != nil {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
		} else {
			api.serverErrorResponse(w, r, err)
		}
		return
	}

	// Sort after buildArrival so predicted times are already filled in.
	sortArrivalsByBestTime(result.Arrivals)

	// Nothing scheduled in the window: skip the reference and nearby-stop
	// lookups, but keep the stop's alerts, which don't depend on arrivals.
	if !result.Matched {
		situationIDs := api.addStopSituations(ctx, references, acc, stopCode, stopAgencyID)
		response := models.NewArrivalsAndDepartureResponse(result.Arrivals, *references, []string{}, situationIDs, stopID, api.Clock)
		api.sendResponse(w, r, response)
		return
	}

	references, err = api.buildArrivalsReferences(ctx, arrivalsReferencesInput{
		fallbackAgencyID: stopAgencyID,
		primaryAgency:    &agency,
	}, acc)
	if err != nil {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
		} else {
			api.serverErrorResponse(w, r, err)
		}
		return
	}

	topLevelSituationIDs := api.addStopSituations(ctx, references, acc, stopCode, stopAgencyID)

	nearbyStopIDs := getNearbyStopIDs(api, ctx, stop.Lat, stop.Lon, stopCode, stopAgencyID)
	response := models.NewArrivalsAndDepartureResponse(result.Arrivals, *references, nearbyStopIDs, topLevelSituationIDs, stopID, api.Clock)
	api.sendResponse(w, r, response)
}

// addStopSituations records the stop's own and agency-wide alerts, adds every
// collected situation to references, and returns their IDs for the entry.
// The list covers every alert reachable from this stop, whether it was matched
// through an arrival's trip or through the stop itself.
func (api *RestAPI) addStopSituations(ctx context.Context, references *models.ReferencesModel, acc *arrivalsAccumulator, stopCode, stopAgencyID string) []string {
	acc.situations.add(api.GtfsManager.GetAlertsForStop(stopCode, stopAgencyID), acc.alertAgencyID)
	references.Situations = append(references.Situations, api.situationReferences(ctx, acc.situations.refs)...)
	return situationIDsFromRefs(acc.situations.refs)
}

func getNearbyStopIDs(api *RestAPI, ctx context.Context, lat, lon float64, stopID, fallbackAgencyID string) []string {
	// Mirrors Java: StopWithArrivalsAndDeparturesBeanServiceImpl calls
	// NearbyStopsBeanService with radius=100 m and no count limit
	// (StopWithArrivalsAndDeparturesBeanServiceImpl.java:74-75 +
	// NearbyStopsBeanServiceImpl.java:60-72). LatSpan/LonSpan must stay
	// zero so BoundsFromParams uses Radius — otherwise span-in-degrees
	// takes precedence and produces a city-sized bounding box.
	const nearbyRadiusMeters = 100
	const nearbyLimit = 0 // 0 = no cap
	loc := &internalgtfs.LocationParams{Lat: lat, Lon: lon, Radius: nearbyRadiusMeters}
	nearbyIDs := api.GtfsManager.GetStopIDsWithinBounds(ctx, loc, nearbyLimit)
	if len(nearbyIDs) == 0 {
		return nil
	}

	// Exclude the current stop from nearby results
	var candidateIDs []string
	for _, id := range nearbyIDs {
		if id != stopID {
			candidateIDs = append(candidateIDs, id)
		}
	}
	if len(candidateIDs) == 0 {
		return nil
	}

	reqLogger := logging.ForComponent(ctx, "http_server")
	// Batch-resolve the owning agency for each nearby stop so that
	// multi-agency feeds produce correct combined IDs.
	stopAgencyMap := make(map[string]string, len(candidateIDs))
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, candidateIDs)
	if err != nil {
		reqLogger.Warn("failed to resolve agencies for nearby stops, using fallback",
			"error", err, "fallbackAgencyID", fallbackAgencyID)
	} else {
		for _, row := range agencyRows {
			// First agency wins; a stop served by multiple agencies uses the first one found.
			if _, exists := stopAgencyMap[row.StopID]; !exists {
				stopAgencyMap[row.StopID] = row.ID
			}
		}
	}

	nearbyStopIDs := make([]string, 0, len(candidateIDs))
	for _, sid := range candidateIDs {
		agency := fallbackAgencyID
		if resolved, ok := stopAgencyMap[sid]; ok {
			agency = resolved
		}
		nearbyStopIDs = append(nearbyStopIDs, utils.FormCombinedID(agency, sid))
	}
	return nearbyStopIDs
}
