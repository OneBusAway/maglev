package restapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

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

	if val := query.Get("minutesAfter"); val != "" {
		if minutes, err := strconv.Atoi(val); err == nil {
			paramAfter := time.Duration(minutes) * time.Minute
			if paramAfter < 0 {
				addError("minutesAfter", "must be a non-negative integer")
			} else {
				params.After = min(paramAfter, maxAfter)
			}
		} else {
			addError("minutesAfter", "must be a valid integer")
		}
	}

	if val := query.Get("minutesBefore"); val != "" {
		if minutes, err := strconv.Atoi(val); err == nil {
			paramBefore := time.Duration(minutes) * time.Minute
			if paramBefore < 0 {
				addError("minutesBefore", "must be a non-negative integer")
			} else {
				params.Before = min(paramBefore, maxBefore)
			}
		} else {
			addError("minutesBefore", "must be a valid integer")
		}
	}

	if val := query.Get("time"); val != "" {
		if timeMs, err := strconv.ParseInt(val, 10, 64); err == nil {
			params.Time = time.Unix(timeMs/1000, (timeMs%1000)*1000000)
		} else {
			addError("time", "must be a valid Unix timestamp in milliseconds")
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
		api.sendNotFound(w, r)
		return
	}

	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, stopAgencyID)
	if err != nil {
		// Unknown agency (e.g. wrong-case agency in the URL) is a client
		// error — surface 404 instead of 500.
		if errors.Is(err, sql.ErrNoRows) {
			api.sendNotFound(w, r)
		} else {
			api.serverErrorResponse(w, r, err)
		}
		return
	}

	loc, err := loadAgencyLocation(agency.ID, agency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	params.Time = params.Time.In(loc)

	acc := newArrivalsAccumulator(stopAgencyID)
	references := models.NewEmptyReferences()
	references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&agency))

	result, err := api.arrivalsForStop(ctx, stopArrivalsInput{
		StopCode:  stopCode,
		AgencyID:  stopAgencyID,
		Location:  loc,
		QueryTime: params.Time,
		Before:    params.Before,
		After:     params.After,
	}, acc)
	if err != nil {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
		} else {
			api.serverErrorResponse(w, r, err)
		}
		return
	}

	// Nothing scheduled in the window: emit the bare envelope without paying
	// for reference, alert or nearby-stop lookups.
	if !result.Matched {
		response := models.NewArrivalsAndDepartureResponse(result.Arrivals, *references, []string{}, []string{}, stopID, api.Clock)
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

	acc.situations.add(api.GtfsManager.GetAlertsForStop(stopCode), acc.alertAgencyID)
	references.Situations = append(references.Situations, api.situationReferences(acc.situations.refs)...)

	// The top-level list covers every alert reachable from this stop, whether it
	// was matched through an arrival's trip or through the stop itself.
	topLevelSituationIDs := situationIDsFromRefs(acc.situations.refs)

	nearbyStopIDs := getNearbyStopIDs(api, ctx, stop.Lat, stop.Lon, stopCode, stopAgencyID)
	response := models.NewArrivalsAndDepartureResponse(result.Arrivals, *references, nearbyStopIDs, topLevelSituationIDs, stopID, api.Clock)
	api.sendResponse(w, r, response)
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

	// Batch-resolve the owning agency for each nearby stop so that
	// multi-agency feeds produce correct combined IDs.
	stopAgencyMap := make(map[string]string, len(candidateIDs))
	agencyRows, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops(ctx, candidateIDs)
	if err != nil {
		api.Logger.Warn("failed to resolve agencies for nearby stops, using fallback",
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
