package restapi

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type situationEntityIDs struct {
	routes, stops, trips []string
}

type situationAgencyLookup struct {
	known                map[string]struct{}
	routes, stops, trips map[string]string
}

func collectSituationEntityIDs(alerts []gtfs.Alert) situationEntityIDs {
	var ids situationEntityIDs
	for _, alert := range alerts {
		for _, entity := range alert.InformedEntities {
			route, stop, trip := extractRawEntityIDs(entity)
			ids.routes = appendSituationCandidates(ids.routes, route)
			ids.stops = appendSituationCandidates(ids.stops, stop)
			ids.trips = appendSituationCandidates(ids.trips, trip)
		}
	}
	return ids
}

func appendSituationCandidates(ids []string, id string) []string {
	if id == "" {
		return ids
	}
	ids = append(ids, id)
	if parts := strings.SplitN(id, "_", 2); len(parts) == 2 {
		ids = append(ids, parts[1])
	}
	return ids
}

func (api *RestAPI) loadSituationAgencies(ctx context.Context, alerts []gtfs.Alert) situationAgencyLookup {
	agencies := situationAgencyLookup{
		known: make(map[string]struct{}), routes: make(map[string]string),
		stops: make(map[string]string), trips: make(map[string]string),
	}
	if api == nil || api.Application == nil || api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		return agencies
	}
	ids := collectSituationEntityIDs(alerts)
	api.loadSituationKnownAgencies(ctx, agencies.known)
	api.loadSituationRouteAgencies(ctx, ids.routes, agencies.routes, "failed to fetch routes for situation reference")
	api.loadSituationStopAgencies(ctx, ids.stops, agencies.stops)
	api.loadSituationTripAgencies(ctx, ids.trips, agencies)
	return agencies
}

func logSituationLookupError(ctx context.Context, message string, err error) {
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		logging.ForComponent(ctx, "http_server").Error(message, "error", err)
	}
}

func (api *RestAPI) loadSituationKnownAgencies(ctx context.Context, agencies map[string]struct{}) {
	ids, err := api.GtfsManager.GtfsDB.Queries.ListAgencyIds(ctx)
	if err != nil {
		logSituationLookupError(ctx, "failed to fetch agencies for situation reference", err)
		return
	}
	for _, id := range ids {
		agencies[id] = struct{}{}
	}
}

func (api *RestAPI) loadSituationRouteAgencies(ctx context.Context, ids []string, agencies map[string]string, message string) {
	slices.Sort(ids)
	routes, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		logSituationLookupError(ctx, message, err)
		return
	}
	for _, route := range routes {
		agencies[route.ID] = route.AgencyID
	}
}

func (api *RestAPI) loadSituationStopAgencies(ctx context.Context, ids []string, agencies map[string]string) {
	slices.Sort(ids)
	rows, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops)
	if err != nil {
		logSituationLookupError(ctx, "failed to fetch stops for situation reference", err)
		return
	}
	for _, row := range rows {
		if _, exists := agencies[row.StopID]; !exists {
			agencies[row.StopID] = row.ID
		}
	}
}

func (api *RestAPI) loadSituationTripAgencies(ctx context.Context, ids []string, agencies situationAgencyLookup) {
	slices.Sort(ids)
	trips, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		logSituationLookupError(ctx, "failed to fetch trips for situation reference", err)
		return
	}
	var missingRouteIDs []string
	for _, trip := range trips {
		if agency, ok := agencies.routes[trip.RouteID]; ok {
			agencies.trips[trip.ID] = agency
		} else {
			missingRouteIDs = append(missingRouteIDs, trip.RouteID)
		}
	}
	if len(missingRouteIDs) == 0 {
		return
	}
	api.loadSituationRouteAgencies(ctx, missingRouteIDs, agencies.routes, "failed to fetch missing routes for situation reference")
	for _, trip := range trips {
		if agency, ok := agencies.routes[trip.RouteID]; ok {
			agencies.trips[trip.ID] = agency
		}
	}
}

func situationConsequences(effect gtfs.AlertEffect) []models.SituationConsequence {
	consequences := make([]models.SituationConsequence, 0)
	if alertHasEffect(effect) {
		consequences = append(consequences, models.SituationConsequence{
			Condition: "",
			ConditionDetails: &models.SituationConditionDetails{
				DiversionPath: models.Polyline{}, DiversionStopIDs: []string{},
			},
		})
	}
	return consequences
}

func buildSituationReference(alert gtfs.Alert, agencies situationAgencyLookup) models.Situation {
	situation := models.Situation{
		ID:                 alert.ID,
		CreationTime:       models.NewModelTime(time.Time{}),
		ActiveWindows:      situationActiveWindows(alert),
		AllAffects:         situationAffectedEntities(alert, agencies),
		ConsequenceMessage: "",
		Consequences:       situationConsequences(alert.Effect),
		PublicationWindows: []any{},
		Reason:             mapAlertCauseToReason(alert.Cause),
		Severity:           mapAlertEffectToSeverity(alert.Effect),
	}

	if len(alert.Header) > 0 && alert.Header[0].Text != "" {
		situation.Summary = &models.TranslatedString{
			Value: alert.Header[0].Text,
			Lang:  alert.Header[0].Language,
		}
	}

	if len(alert.Description) > 0 && alert.Description[0].Text != "" {
		situation.Description = &models.TranslatedString{
			Value: alert.Description[0].Text,
			Lang:  alert.Description[0].Language,
		}
	}

	if len(alert.URL) > 0 && alert.URL[0].Text != "" {
		situation.URL = &models.TranslatedString{
			Value: alert.URL[0].Text,
			Lang:  alert.URL[0].Language,
		}
	}
	return situation
}

func situationActiveWindows(alert gtfs.Alert) []models.ActiveWindow {
	windows := make([]models.ActiveWindow, 0, len(alert.ActivePeriods))
	for _, period := range alert.ActivePeriods {
		window := models.ActiveWindow{}
		if period.StartsAt != nil {
			window.From = period.StartsAt.UnixMilli()
		}
		if period.EndsAt != nil {
			window.To = period.EndsAt.UnixMilli()
		}
		windows = append(windows, window)
	}
	return windows
}

func situationAffectedEntities(alert gtfs.Alert, agencies situationAgencyLookup) []models.AffectedEntity {
	entities := make([]models.AffectedEntity, 0, len(alert.InformedEntities))
	for _, entity := range alert.InformedEntities {
		rawRouteID, rawStopID, rawTripID := extractRawEntityIDs(entity)

		fallbackAgency := getStringValue(entity.AgencyID)
		if fallbackAgency == "" {
			fallbackAgency = agencyIDForAlert(alert, "")
		}

		routeID := qualifyEntityID(rawRouteID, fallbackAgency, agencies.routes, agencies.known)
		stopID := qualifyEntityID(rawStopID, fallbackAgency, agencies.stops, agencies.known)
		tripID := qualifyEntityID(rawTripID, fallbackAgency, agencies.trips, agencies.known)

		affectedEntity := models.AffectedEntity{
			AgencyID:      getStringValue(entity.AgencyID),
			ApplicationID: "",
			DirectionID:   entity.DirectionID.String(),
			RouteID:       routeID,
			StopID:        stopID,
			TripID:        tripID,
		}

		entities = append(entities, affectedEntity)
	}
	return entities
}
