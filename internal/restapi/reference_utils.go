package restapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

func buildAgencyReferences(agencies []gtfsdb.Agency) []models.AgencyReference {
	refs := make([]models.AgencyReference, 0, len(agencies))
	for _, agency := range agencies {
		refs = append(refs, models.AgencyReferenceFromDatabase(&agency))
	}
	return refs
}

// BuildRouteReferences resolves the route references for the given stops,
// scoped to the requested agency. Only routes whose combined ID matches one
// of the stop's StaticRouteIDs are returned, each carrying its own agency
// rather than being overwritten with the caller's.
func (api *RestAPI) BuildRouteReferences(ctx context.Context, agencyID string, stops []models.Stop) ([]models.Route, error) {
	expectedCombinedIDs, originalRouteIDs, err := expectedRouteIDsForStops(ctx, stops, agencyID)
	if err != nil {
		return nil, err
	}
	if len(originalRouteIDs) == 0 {
		return []models.Route{}, nil
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, originalRouteIDs)
	if err != nil {
		return nil, err
	}

	filteredRoutes := filterRoutesByExpectedCombinedID(routes, expectedCombinedIDs, agencyID)
	return buildRouteModels(ctx, agencyID, filteredRoutes)
}

// expectedRouteIDsForStops walks every stop's StaticRouteIDs and returns the
// set of agency-combined IDs a route must match to be a legitimate reference,
// plus the deduplicated bare route IDs to query the database for. A
// cancelled context aborts the walk and surfaces ctx.Err() to the caller.
func expectedRouteIDsForStops(ctx context.Context, stops []models.Stop, agencyID string) (map[string]bool, []string, error) {
	routeIDSet := make(map[string]bool)
	expectedCombinedIDs := make(map[string]bool)
	originalRouteIDs := make([]string, 0)

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		for _, routeID := range stop.StaticRouteIDs {
			routeAgencyID, originalRouteID, err := utils.ExtractAgencyIDAndCodeID(routeID)
			if err != nil {
				originalRouteID = routeID
				routeAgencyID = agencyID
			}

			expectedCombinedIDs[utils.FormCombinedID(routeAgencyID, originalRouteID)] = true

			if !routeIDSet[originalRouteID] {
				routeIDSet[originalRouteID] = true
				originalRouteIDs = append(originalRouteIDs, originalRouteID)
			}
		}
	}

	return expectedCombinedIDs, originalRouteIDs, nil
}

// filterRoutesByExpectedCombinedID keeps only the routes whose real,
// agency-qualified ID (not the caller's agency) was actually requested by a
// stop, deduplicating by that combined ID. This is what discards a route
// that only matched by a bare-ID collision with an unrelated agency.
func filterRoutesByExpectedCombinedID(routes []gtfsdb.Route, expectedCombinedIDs map[string]bool, agencyID string) []gtfsdb.Route {
	filtered := make([]gtfsdb.Route, 0, len(routes))
	seenCombinedIDs := make(map[string]bool)
	for _, route := range routes {
		targetAgencyID := route.AgencyID
		if targetAgencyID == "" {
			targetAgencyID = agencyID
		}

		combinedID := utils.FormCombinedID(targetAgencyID, route.ID)
		if !expectedCombinedIDs[combinedID] || seenCombinedIDs[combinedID] {
			continue
		}

		seenCombinedIDs[combinedID] = true
		filtered = append(filtered, route)
	}
	return filtered
}

// buildRouteModels converts a slice of database routes into model routes.
// It is the single source of truth for mapping gtfsdb.Route → models.Route.
func buildRouteModels(ctx context.Context, agencyID string, routes []gtfsdb.Route) ([]models.Route, error) {
	modelRoutes := make([]models.Route, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		targetAgencyID := route.AgencyID
		if targetAgencyID == "" {
			targetAgencyID = agencyID
		}

		combinedID := utils.FormCombinedID(targetAgencyID, route.ID)

		routeModel := models.NewRoute(
			combinedID,
			targetAgencyID,
			nulls.StringOrEmpty(route.ShortName),
			nulls.StringOrEmpty(route.LongName),
			nulls.StringOrEmpty(route.Desc),
			models.RouteType(route.Type),
			nulls.StringOrEmpty(route.Url),
			nulls.StringOrEmpty(route.Color),
			nulls.StringOrEmpty(route.TextColor),
		)
		modelRoutes = append(modelRoutes, routeModel)
	}

	return modelRoutes, nil
}

// routeReferenceByID builds the Route reference for a single route, looked up by
// its bare (agency-less) route ID. The reference is labelled with the route's own
// agency, which need not be the agency of the entity that referenced it.
func (api *RestAPI) routeReferenceByID(ctx context.Context, routeID string) (models.Route, error) {
	route, err := api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		return models.Route{}, err
	}

	routeModels, err := buildRouteModels(ctx, route.AgencyID, []gtfsdb.Route{route})
	if err != nil {
		return models.Route{}, err
	}

	return routeModels[0], nil
}

// routeReferenceFromStopRow builds a Route reference from a stops-derived route row,
// labelled with the route's own agency rather than the agency that was queried, so
// results spanning multiple agencies stay resolvable.
func routeReferenceFromStopRow(row gtfsdb.GetRoutesForStopsRow) models.Route {
	return models.NewRoute(
		utils.FormCombinedID(row.AgencyID, row.ID),
		row.AgencyID,
		nulls.StringOrEmpty(row.ShortName),
		nulls.StringOrEmpty(row.LongName),
		nulls.StringOrEmpty(row.Desc),
		models.RouteType(row.Type),
		nulls.StringOrEmpty(row.Url),
		nulls.StringOrEmpty(row.Color),
		nulls.StringOrEmpty(row.TextColor))
}

// routeReferencesForStops deduplicates the rows returned by GetRoutesForStops into Route
// reference objects, keyed by each row's own agency ID so results spanning multiple
// agencies are handled correctly.
func routeReferencesForStops(routesRows []gtfsdb.GetRoutesForStopsRow) []models.Route {
	routesMap := make(map[string]models.Route)
	for _, row := range routesRows {
		combinedRouteID := utils.FormCombinedID(row.AgencyID, row.ID)
		if _, exists := routesMap[combinedRouteID]; exists {
			continue
		}

		routesMap[combinedRouteID] = routeReferenceFromStopRow(row)
	}

	return utils.MapValues(routesMap)
}

// routeReferenceForTrip returns the Route reference for the route a trip runs on.
// A route already resolved for the trip's stop references is preferred over a fresh
// lookup, since a trip's own route commonly also serves its closest and next stops.
func (api *RestAPI) routeReferenceForTrip(ctx context.Context, routeID string, stopRoutes map[string]gtfsdb.GetRoutesForStopsRow) (models.Route, error) {
	for _, row := range stopRoutes {
		if row.ID == routeID {
			return routeReferenceFromStopRow(row), nil
		}
	}

	return api.routeReferenceByID(ctx, routeID)
}

// appendRouteAgencyReference adds a route's own agency to references when it is not
// the agency the request was scoped to, so the agencyId a cross-agency route
// reference carries resolves against references.agencies. A lookup that fails costs
// the reference rather than the response, as with the route itself. Safe to call once
// per route even when several routes share the same foreign agency — an agency
// already present in references is never added twice.
func (api *RestAPI) appendRouteAgencyReference(ctx context.Context, references *models.ReferencesModel, routeAgencyID, requestAgencyID string) {
	if routeAgencyID == requestAgencyID {
		return
	}
	for _, existing := range references.Agencies {
		if existing.ID == routeAgencyID {
			return
		}
	}

	routeAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, routeAgencyID)
	if err != nil {
		api.Logger.Warn("failed to fetch route agency for reference",
			"agencyID", routeAgencyID, "error", err)
		return
	}

	references.Agencies = append(references.Agencies, models.AgencyReferenceFromDatabase(&routeAgency))
}

// mergeParentRouteReferences appends any parent-station routes not already present in
// routes, so parent stop references never point at an absent route.
func mergeParentRouteReferences(routes []models.Route, parentRoutes map[string]gtfsdb.GetRoutesForStopsRow) []models.Route {
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		seen[route.ID] = true
	}

	for combinedRouteID, row := range parentRoutes {
		if seen[combinedRouteID] {
			continue
		}
		routes = append(routes, models.NewRoute(
			combinedRouteID,
			row.AgencyID,
			nulls.StringOrEmpty(row.ShortName),
			nulls.StringOrEmpty(row.LongName),
			nulls.StringOrEmpty(row.Desc),
			models.RouteType(row.Type),
			nulls.StringOrEmpty(row.Url),
			nulls.StringOrEmpty(row.Color),
			nulls.StringOrEmpty(row.TextColor)))
		seen[combinedRouteID] = true
	}

	return routes
}

// agencyReferencesForStops deduplicates the rows returned by GetAgenciesForStops into
// AgencyReference objects, reusing AgencyReferenceFromDatabase for the field mapping.
func agencyReferencesForStops(agencyRows []gtfsdb.GetAgenciesForStopsRow) []models.AgencyReference {
	agenciesMap := make(map[string]models.AgencyReference)
	for _, row := range agencyRows {
		if _, exists := agenciesMap[row.ID]; exists {
			continue
		}

		agenciesMap[row.ID] = models.AgencyReferenceFromDatabase(&gtfsdb.Agency{
			ID:       row.ID,
			Name:     row.Name,
			Url:      row.Url,
			Timezone: row.Timezone,
			Lang:     row.Lang,
			Phone:    row.Phone,
			FareUrl:  row.FareUrl,
			Email:    row.Email,
		})
	}

	return utils.MapValues(agenciesMap)
}

func (api *RestAPI) BuildSituationReferences(alerts []gtfs.Alert) []models.Situation {
	situations := make([]models.Situation, 0, len(alerts))

	for _, alert := range alerts {
		situation := models.Situation{
			ID:                 alert.ID,
			CreationTime:       models.NewModelTime(time.Time{}),
			ActiveWindows:      make([]models.ActiveWindow, 0, len(alert.ActivePeriods)),
			AllAffects:         make([]models.AffectedEntity, 0, len(alert.InformedEntities)),
			ConsequenceMessage: "",
			Consequences:       []any{},
			PublicationWindows: []any{},
			Reason:             mapAlertCauseToReason(alert.Cause),
			Severity:           mapAlertEffectToSeverity(alert.Effect),
		}

		for _, period := range alert.ActivePeriods {
			window := models.ActiveWindow{}
			if period.StartsAt != nil {
				window.From = period.StartsAt.UnixMilli()
			}
			if period.EndsAt != nil {
				window.To = period.EndsAt.UnixMilli()
			}
			situation.ActiveWindows = append(situation.ActiveWindows, window)
		}

		for _, entity := range alert.InformedEntities {
			affectedEntity := models.AffectedEntity{
				AgencyID:      getStringValue(entity.AgencyID),
				ApplicationID: "",
				DirectionID:   entity.DirectionID.String(),
				RouteID:       getStringValue(entity.RouteID),
				StopID:        getStringValue(entity.StopID),
				TripID:        "",
			}

			if entity.TripID != nil {
				affectedEntity.TripID = entity.TripID.ID
			}

			situation.AllAffects = append(situation.AllAffects, affectedEntity)
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

		situations = append(situations, situation)
	}

	return situations
}

func getStringValue(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func mapAlertCauseToReason(cause gtfs.AlertCause) string {
	switch cause {
	case 1: // UNKNOWN_CAUSE
		return "UNKNOWN_CAUSE"
	case 2: // OTHER_CAUSE
		return "miscellaneousReason"
	case 3: // TECHNICAL_PROBLEM
		return "equipmentReason"
	case 4: // STRIKE
		return "personnelReason"
	case 5: // DEMONSTRATION
		return "miscellaneousReason"
	case 6: // ACCIDENT
		return "miscellaneousReason"
	case 7: // HOLIDAY
		return "miscellaneousReason"
	case 8: // WEATHER
		return "environmentReason"
	case 9: // MAINTENANCE
		return "equipmentReason"
	case 10: // CONSTRUCTION
		return "equipmentReason"
	case 11: // POLICE_ACTIVITY
		return "securityAlert"
	case 12: // MEDICAL_EMERGENCY
		return "miscellaneousReason"
	default:
		return "UNKNOWN_CAUSE"
	}
}

func mapAlertEffectToSeverity(effect gtfs.AlertEffect) string {
	switch effect {
	case 1: // NO_SERVICE
		return "severe"
	case 2: // REDUCED_SERVICE
		return "normal"
	case 3: // SIGNIFICANT_DELAYS
		return "severe"
	case 4: // DETOUR
		return "normal"
	case 5: // ADDITIONAL_SERVICE
		return "noImpact"
	case 6: // MODIFIED_SERVICE
		return "normal"
	case 7: // OTHER_EFFECT
		return "normal"
	case 8: // UNKNOWN_EFFECT
		return "noImpact"
	case 9: // STOP_MOVED
		return "normal"
	default:
		return "noImpact"
	}
}

// deduplicateAlerts takes multiple slices of alerts and returns a single slice with unique alerts by ID.
func deduplicateAlerts(alertSlices ...[]gtfs.Alert) []gtfs.Alert {
	seen := make(map[string]struct{})
	var uniqueAlerts []gtfs.Alert

	for _, slice := range alertSlices {
		for _, alert := range slice {
			if _, exists := seen[alert.ID]; !exists {
				seen[alert.ID] = struct{}{}
				uniqueAlerts = append(uniqueAlerts, alert)
			}
		}
	}
	return uniqueAlerts
}

// collectAlertsForStops returns deduplicated alerts matching any of the given stop IDs.
// It acquires realTimeMutex internally via GetAlertsForStop; no external lock is required.
func (api *RestAPI) collectAlertsForStops(stopIDs []string) []gtfs.Alert {
	var alerts []gtfs.Alert
	for _, stopID := range stopIDs {
		alerts = append(alerts, api.GtfsManager.GetAlertsForStop(stopID)...)
	}
	return deduplicateAlerts(alerts)
}

// collectAlertsForRoutes returns deduplicated alerts matching any of the given route IDs.
// It acquires realTimeMutex internally via GetAlertsForRoute; no external lock is required.
func (api *RestAPI) collectAlertsForRoutes(routeIDs []string) []gtfs.Alert {
	var alerts []gtfs.Alert
	for _, routeID := range routeIDs {
		alerts = append(alerts, api.GtfsManager.GetAlertsForRoute(routeID)...)
	}
	return deduplicateAlerts(alerts)
}

// ShouldIncludeReferences parses the "includeReferences" query parameter from the request.
// It defaults to true if the parameter is absent or if it fails to parse as a boolean.
func ShouldIncludeReferences(r *http.Request) bool {
	val := r.URL.Query().Get("includeReferences")
	if val == "" {
		return true
	}

	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return true
	}

	return parsed
}

// BuildStopReferencesAndRouteIDsForStops builds full stop references and collects unique routes for the given stop IDs.
func BuildStopReferencesAndRouteIDsForStops(api *RestAPI, ctx context.Context, agencyID string, stopIDs []string) ([]models.Stop, map[string]gtfsdb.GetRoutesForStopsRow, error) {
	if len(stopIDs) == 0 {
		return []models.Stop{}, map[string]gtfsdb.GetRoutesForStopsRow{}, nil
	}

	uniqueStopIDs := dedupeStrings(stopIDs)

	stopsDB, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}
	stopMap := make(map[string]gtfsdb.Stop, len(stopsDB))
	for _, stop := range stopsDB {
		stopMap[stop.ID] = stop
	}

	allRoutes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, uniqueStopIDs)
	if err != nil {
		return nil, nil, err
	}
	routesByStop, uniqueRouteMap := groupRoutesByStop(allRoutes)

	modelStops := make([]models.Stop, 0, len(uniqueStopIDs))
	for _, stopID := range uniqueStopIDs {
		stop, exists := stopMap[stopID]
		if !exists {
			continue
		}
		combinedRouteIDs := api.combinedRouteIDsForStop(routesByStop[stopID])
		modelStops = append(modelStops, api.buildStopModel(ctx, agencyID, stop, combinedRouteIDs))
	}

	return modelStops, uniqueRouteMap, nil
}

// dedupeStrings returns the input slice with duplicates removed, preserving order.
func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, v := range values {
		if _, exists := seen[v]; !exists {
			seen[v] = struct{}{}
			unique = append(unique, v)
		}
	}
	return unique
}

// groupRoutesByStop groups the routes returned by GetRoutesForStops by their stop ID and
// builds a map of unique routes keyed by each route's own agency-combined ID.
func groupRoutesByStop(allRoutes []gtfsdb.GetRoutesForStopsRow) (map[string][]gtfsdb.Route, map[string]gtfsdb.GetRoutesForStopsRow) {
	routesByStop := make(map[string][]gtfsdb.Route)
	uniqueRouteMap := make(map[string]gtfsdb.GetRoutesForStopsRow)
	for _, routeRow := range allRoutes {
		route := gtfsdb.Route{
			ID:        routeRow.ID,
			AgencyID:  routeRow.AgencyID,
			ShortName: routeRow.ShortName,
			LongName:  routeRow.LongName,
			Desc:      routeRow.Desc,
			Type:      routeRow.Type,
			Url:       routeRow.Url,
			Color:     routeRow.Color,
			TextColor: routeRow.TextColor,
		}
		routesByStop[routeRow.StopID] = append(routesByStop[routeRow.StopID], route)
		combinedID := utils.FormCombinedID(routeRow.AgencyID, routeRow.ID)
		uniqueRouteMap[combinedID] = routeRow
	}
	return routesByStop, uniqueRouteMap
}

// combinedRouteIDsForStop sorts the routes serving a stop into a stable, human-friendly
// order and returns their agency-combined IDs, each keyed by its own agency.
func (api *RestAPI) combinedRouteIDsForStop(routesForStop []gtfsdb.Route) []string {
	// Sort naturally by ShortName (falling back to LongName, then AgencyID, then ID) so the
	// route IDs are returned in a stable, human-friendly order.
	utils.SortRoutesByName(routesForStop)
	combinedRouteIDs := make([]string, len(routesForStop))
	for i, rt := range routesForStop {
		combinedRouteIDs[i] = utils.FormCombinedID(rt.AgencyID, rt.ID)
	}
	return combinedRouteIDs
}

// buildStopModel converts a database stop into a models.Stop with the given combined route IDs.
func (api *RestAPI) buildStopModel(ctx context.Context, agencyID string, stop gtfsdb.Stop, combinedRouteIDs []string) models.Stop {
	return models.Stop{
		ID:                 utils.FormCombinedID(agencyID, stop.ID),
		Name:               stop.Name.String,
		Lat:                stop.Lat,
		Lon:                stop.Lon,
		Code:               nulls.StringOrDefault(stop.Code, stop.ID),
		Direction:          api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction),
		LocationType:       int(stop.LocationType.Int64),
		WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		RouteIDs:           combinedRouteIDs,
		StaticRouteIDs:     combinedRouteIDs,
	}
}

// idsPerBatchedQuery bounds how many IDs go into one IN (...) list, assuming
// the batched slice is the only bind the statement carries. SQLite rejects a
// statement carrying more bind variables than it allows rather than
// truncating it, and these ID sets are only bounded by how much the request
// matched. Kept well under the oldest limit (999) so the batch size does not
// depend on which SQLite the build links against. A statement binding
// anything besides the batched slice needs queryInBatchesReserving instead,
// so that budget also accounts for those binds.
const idsPerBatchedQuery = 900

// queryInBatches runs query over ids in batches small enough to stay under the
// bind variable limit, concatenating the results.
func queryInBatches[T any](ctx context.Context, ids []string, query func(context.Context, []string) ([]T, error)) ([]T, error) {
	return queryInBatchesReserving(ctx, ids, 0, query)
}

// queryInBatchesReserving is queryInBatches with reserved slots subtracted
// from the batch size, for a statement that binds something besides the
// batched slice — a second IN (...) list, a scalar. Without this, sizing the
// batch at idsPerBatchedQuery silently assumes the batched slice is the whole
// statement, and the untracked binds can push the total over the limit.
//
// ponytail: assumes the reserved binds themselves fit in one statement. A
// second dimension large enough on its own to need batching (hundreds of
// active service IDs on one day, say) would still overflow; batch that
// dimension too if a feed ever gets there.
func queryInBatchesReserving[T any](ctx context.Context, ids []string, reserved int,
	query func(context.Context, []string) ([]T, error)) ([]T, error) {
	batchSize := max(1, idsPerBatchedQuery-reserved)
	results := make([]T, 0, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := min(start+batchSize, len(ids))
		batch, err := query(ctx, ids[start:end])
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
	}
	return results, nil
}

// stopReferences builds the stop reference block for a set of list entries,
// labelling each stop with the combined ID the entries actually referred to it
// by — the referring trip's agency need not own the stop's first route. A stop
// whose routes do not resolve still gets a reference, with an empty route list,
// so every stop ID an entry emits resolves in the block.
//
// The routes each stop serves are returned alongside the references, for callers
// that collect route references from them.
func (api *RestAPI) stopReferences(ctx context.Context, stops []gtfsdb.Stop, idsByBareID map[string]string) ([]models.Stop, map[string][]string) {
	routeIDsByStop := api.routeIDsForStops(ctx, stops)

	stopList := make([]models.Stop, 0, len(stops))
	for _, stop := range stops {
		routeIDs := routeIDsByStop[stop.ID]
		if routeIDs == nil {
			routeIDs = []string{}
		}

		direction := api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)
		if direction == "" {
			direction = models.UnknownValue
		}

		parentID := ""
		if parentStation := nulls.StringOrEmpty(stop.ParentStation); parentStation != "" {
			agencyID, err := utils.ExtractAgencyID(idsByBareID[stop.ID])
			if err == nil {
				parentID = utils.FormCombinedID(agencyID, parentStation)
			}
		}

		stopList = append(stopList, models.Stop{
			Code:               nulls.StringOrEmpty(stop.Code),
			Direction:          direction,
			ID:                 idsByBareID[stop.ID],
			Lat:                stop.Lat,
			Lon:                stop.Lon,
			LocationType:       int(nulls.Int64OrDefault(stop.LocationType, 0)),
			Name:               nulls.StringOrEmpty(stop.Name),
			Parent:             parentID,
			RouteIDs:           routeIDs,
			StaticRouteIDs:     routeIDs,
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}
	return stopList, routeIDsByStop
}

// routeIDsForStops maps each stop to the combined IDs of the routes serving it.
func (api *RestAPI) routeIDsForStops(ctx context.Context, stops []gtfsdb.Stop) map[string][]string {
	routeIDsByStop := make(map[string][]string)
	if len(stops) == 0 {
		return routeIDsByStop
	}

	stopIDs := make([]string, len(stops))
	for i, stop := range stops {
		stopIDs[i] = stop.ID
	}

	rows, err := queryInBatches(ctx, stopIDs, api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops)
	if err != nil {
		logging.LogError(api.Logger, "failed to fetch routes for stop references", err)
		return routeIDsByStop
	}
	for _, row := range rows {
		if routeID, ok := row.RouteID.(string); ok {
			routeIDsByStop[row.StopID] = append(routeIDsByStop[row.StopID], routeID)
		}
	}
	return routeIDsByStop
}

// Situation references
//
// An entry's situationIds and the response's references.situations must use the
// same ID, or the IDs resolve to nothing. Everything that derives one derives
// the other, so the two cannot drift apart.
// situationRef pairs an alert with the situation ID that both list entries and
// situation references use to refer to it.
type situationRef struct {
	ID    string
	Alert gtfs.Alert
}

// situationRefsFromAlerts drops alerts with no ID and pairs the rest with the
// situation ID used to refer to them.
func situationRefsFromAlerts(alerts []gtfs.Alert, agencyID string) []situationRef {
	refs := make([]situationRef, 0, len(alerts))
	for _, alert := range alerts {
		if alert.ID == "" {
			continue
		}
		refs = append(refs, situationRef{ID: situationID(alert.ID, agencyIDForAlert(alert, agencyID)), Alert: alert})
	}
	return refs
}

// agencyIDForAlert returns the agency whose prefix an alert's situation ID
// carries, preferring the alert's own informed entity over the caller's agency.
//
// One alert is reachable by more than one path: a stop served by another
// agency's route matches the same alert through both the route and the stop, and
// each path knows a different agency. Scoping the ID to the caller's agency
// would then publish that alert twice under two IDs, and an entry's
// situationIds would resolve to whichever one it happened to be built from.
// The informed entity is the only source that does not vary by lookup path.
func agencyIDForAlert(alert gtfs.Alert, fallbackAgencyID string) string {
	for _, entity := range alert.InformedEntities {
		if entity.AgencyID != nil && *entity.AgencyID != "" {
			return *entity.AgencyID
		}
	}
	return fallbackAgencyID
}

// situationID returns the combined-form ID for an alert.
//
// Prefixing is idempotent, not unconditional: a GTFS-RT feed exported by an OBA
// instance already carries the agency prefix on its alert IDs — Puget Sound
// ships "1_92239" — and prefixing again would publish "1_1_92239", which
// resolves against nothing a client has seen. Upstream reports "1_92239".
func situationID(alertID, agencyID string) string {
	if agencyID == "" || strings.HasPrefix(alertID, agencyID+"_") {
		return alertID
	}
	return utils.FormCombinedID(agencyID, alertID)
}

// situationCollector deduplicates the alerts referenced by list entries so the
// response emits one situation reference per distinct situation ID.
type situationCollector struct {
	seen map[string]struct{}
	refs []situationRef
}

func newSituationCollector() *situationCollector {
	return &situationCollector{seen: make(map[string]struct{})}
}

// add records the alerts affecting one trip and returns their situation IDs.
func (c *situationCollector) add(alerts []gtfs.Alert, agencyID string) []string {
	return c.addRefs(situationRefsFromAlerts(alerts, agencyID))
}

// addRefs records already-resolved situation references and returns their IDs.
func (c *situationCollector) addRefs(refs []situationRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
		if _, ok := c.seen[ref.ID]; ok {
			continue
		}
		c.seen[ref.ID] = struct{}{}
		c.refs = append(c.refs, ref)
	}
	return ids
}

// situationReferences converts collected alerts into situation references,
// stamping the same IDs the list entries use. BuildSituationReferences emits raw
// alert IDs and preserves input order one-for-one.
func (api *RestAPI) situationReferences(refs []situationRef) []models.Situation {
	if len(refs) == 0 {
		return []models.Situation{}
	}

	alerts := make([]gtfs.Alert, 0, len(refs))
	for _, ref := range refs {
		alerts = append(alerts, ref.Alert)
	}

	situations := api.BuildSituationReferences(alerts)
	if len(situations) != len(refs) {
		// The ID stamping below pairs by position, so a length change in
		// BuildSituationReferences would silently mislabel every situation.
		logging.LogError(api.Logger, "situation reference count does not match the alerts it was built from",
			fmt.Errorf("built %d situations from %d alerts", len(situations), len(refs)))
		return situations
	}

	for i := range situations {
		situations[i].ID = refs[i].ID
	}
	return situations
}

func (rb *referenceBuilder) getAgenciesList() []models.AgencyReference {
	agencies := make([]models.AgencyReference, 0, len(rb.presentAgencies))
	for _, agency := range rb.presentAgencies {
		agencies = append(agencies, agency)
	}
	return agencies
}

func (rb *referenceBuilder) getRoutesList() []models.Route {
	routes := make([]models.Route, 0, len(rb.presentRoutes))
	for _, route := range rb.presentRoutes {
		if route.ID != "" {
			routes = append(routes, route)
		}
	}
	return routes
}

// situationIDsFromRefs returns the situation IDs of already-resolved references,
// so entry IDs are always derived the same way the references are.
func situationIDsFromRefs(refs []situationRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

// TripSituations returns a trip's situation IDs together with the matching
// situation references, so an entry's situationIds always resolve.
func (api *RestAPI) TripSituations(ctx context.Context, tripID string) ([]string, []models.Situation) {
	return api.situationsFromRefs(api.situationRefsForTrip(ctx, tripID))
}

// situationsFromRefs splits already-resolved references into the entry IDs and
// the reference block built from the same lookup.
func (api *RestAPI) situationsFromRefs(refs []situationRef) ([]string, []models.Situation) {
	return situationIDsFromRefs(refs), api.situationReferences(refs)
}

// tripSituationsFor returns a trip's situations, reusing the references
// BuildTripStatus already resolved for the same trip rather than querying for
// them a second time. Extras is nil when the caller skipped the status.
func (api *RestAPI) tripSituationsFor(ctx context.Context, tripID string, extras *tripStatusExtras) ([]string, []models.Situation) {
	if extras == nil {
		return api.TripSituations(ctx, tripID)
	}
	return api.situationsFromRefs(extras.situations)
}
