package restapi

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

type situationEntityIDs struct {
	routes, stops, trips []string
}

type situationAgencyLookup struct {
	known                map[string]struct{}
	firstStaticAgencyID  string
	routes, stops, trips map[string]string
}

// collectSituationEntityIDs includes raw and possibly qualified IDs so database
// resolution can take precedence over an alert's fallback agency.
func collectSituationEntityIDs(alerts []internalgtfs.FeedAlert) situationEntityIDs {
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

// appendSituationCandidates also tries the suffix after the first underscore;
// the database lookup later decides whether that underscore was a prefix.
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

// loadSituationAgencies keeps independent lookup failures local: missing maps
// fall back during qualification rather than preventing situation construction.
func (api *RestAPI) loadSituationAgencies(ctx context.Context, alerts []internalgtfs.FeedAlert) situationAgencyLookup {
	agencies := situationAgencyLookup{
		known: make(map[string]struct{}), routes: make(map[string]string),
		stops: make(map[string]string), trips: make(map[string]string),
	}
	if api == nil || api.Application == nil || api.GtfsManager == nil || api.GtfsManager.GtfsDB == nil || api.GtfsManager.GtfsDB.Queries == nil {
		return agencies
	}
	ids := collectSituationEntityIDs(alerts)
	agencies.firstStaticAgencyID = api.loadSituationKnownAgencies(ctx, agencies.known)
	api.loadSituationRouteAgencies(ctx, ids.routes, agencies.routes, "routes")
	api.loadSituationStopAgencies(ctx, ids.stops, agencies.stops)
	api.loadSituationTripAgencies(ctx, ids.trips, agencies)
	return agencies
}

// loadSituationKnownAgencies identifies prefixes that qualification may retain
// even when the referenced entity is absent from the static feed.
func (api *RestAPI) loadSituationKnownAgencies(ctx context.Context, agencies map[string]struct{}) string {
	ids, err := api.GtfsManager.GtfsDB.Queries.ListAgencyIds(ctx)
	if err != nil {
		logQueryError(logging.ForComponent(ctx, "http_server"), "failed to fetch agencies for situation reference", err)
		return ""
	}
	for _, id := range ids {
		agencies[id] = struct{}{}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// loadSituationRouteAgencies deduplicates candidates and publishes results only
// after every batch succeeds, leaving the existing map intact on failure.
func (api *RestAPI) loadSituationRouteAgencies(ctx context.Context, ids []string, agencies map[string]string, purpose string) {
	slices.Sort(ids)
	routes, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		logQueryError(logging.ForComponent(ctx, "http_server"), "failed to fetch "+purpose+" for situation reference", err)
		return
	}
	for _, route := range routes {
		agencies[route.ID] = route.AgencyID
	}
}

// loadSituationStopAgencies keeps the first agency returned for a shared stop;
// GetAgenciesForStops orders those rows by agency ID.
func (api *RestAPI) loadSituationStopAgencies(ctx context.Context, ids []string, agencies map[string]string) {
	slices.Sort(ids)
	rows, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetAgenciesForStops)
	if err != nil {
		logQueryError(logging.ForComponent(ctx, "http_server"), "failed to fetch stops for situation reference", err)
		return
	}
	for _, row := range rows {
		if _, exists := agencies[row.StopID]; !exists {
			agencies[row.StopID] = row.ID
		}
	}
}

// loadSituationTripAgencies reuses loaded routes and fetches only missing route
// candidates, retaining already resolved trip agencies if that lookup fails.
func (api *RestAPI) loadSituationTripAgencies(ctx context.Context, ids []string, agencies situationAgencyLookup) {
	slices.Sort(ids)
	trips, err := utils.QueryInBatches(ctx, slices.Compact(ids), api.GtfsManager.GtfsDB.Queries.GetTripsByIDs)
	if err != nil {
		logQueryError(logging.ForComponent(ctx, "http_server"), "failed to fetch trips for situation reference", err)
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
	api.loadSituationRouteAgencies(ctx, missingRouteIDs, agencies.routes, "missing routes")
	for _, trip := range trips {
		if agency, ok := agencies.routes[trip.RouteID]; ok {
			agencies.trips[trip.ID] = agency
		}
	}
}

// situationConsequences preserves an empty array for absent or unknown effects.
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

// buildSituationReference constructs one situation without reordering selectors
// or consulting the database again.
func buildSituationReference(alert internalgtfs.FeedAlert, agencies situationAgencyLookup) models.Situation {
	situation := models.Situation{
		ID:                 alert.ID,
		CreationTime:       models.NewModelTime(time.Time{}),
		ActiveWindows:      situationActiveWindows(alert.Alert),
		AllAffects:         situationAffectedEntities(alert, agencies),
		ConsequenceMessage: "",
		Consequences:       situationConsequences(alert.Effect),
		PublicationWindows: []any{},
		Reason:             mapAlertCauseToReason(alert.Cause),
		Severity:           mapAlertEffectToSeverity(alert.Effect),
	}

	situation.Summary = firstSituationTranslation(alert.Header)
	situation.Description = firstSituationTranslation(alert.Description)
	situation.URL = firstSituationTranslation(alert.URL)
	return situation
}

// firstSituationTranslation considers only the first entry; an empty first
// text suppresses the field even when a later translation is populated.
func firstSituationTranslation(texts []gtfs.AlertText) *models.TranslatedString {
	if len(texts) == 0 || texts[0].Text == "" {
		return nil
	}
	return &models.TranslatedString{Value: texts[0].Text, Lang: texts[0].Language}
}

// situationActiveWindows preserves input order and zero bounds for open ends.
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

// situationAffectedEntities preserves repeated selectors and their order while
// qualifying IDs from database ownership before applying fallback agencies.
func situationAffectedEntities(alert internalgtfs.FeedAlert, agencies situationAgencyLookup) []models.AffectedEntity {
	entities := make([]models.AffectedEntity, 0, len(alert.InformedEntities))
	for _, entity := range alert.InformedEntities {
		rawRouteID, rawStopID, rawTripID := extractRawEntityIDs(entity)

		fallbackAgency := entityFallbackAgency(alert, entity, agencies.firstStaticAgencyID)

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
