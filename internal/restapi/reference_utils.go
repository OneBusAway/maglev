package restapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

func buildAgencyReferences(agencies []gtfsdb.Agency) []models.AgencyReference {
	var refs []models.AgencyReference
	for _, agency := range agencies {
		refs = append(refs, models.AgencyReferenceFromDatabase(&agency))
	}
	return refs
}

func (api *RestAPI) BuildRouteReferences(ctx context.Context, agencyID string, stops []models.Stop) ([]models.Route, error) {
	routeIDSet := make(map[string]bool)
	originalRouteIDs := make([]string, 0)

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		for _, routeID := range stop.StaticRouteIDs {
			_, originalRouteID, err := utils.ExtractAgencyIDAndCodeID(routeID)
			if err != nil {
				continue
			}

			if !routeIDSet[originalRouteID] {
				routeIDSet[originalRouteID] = true
				originalRouteIDs = append(originalRouteIDs, originalRouteID)
			}
		}
	}

	if len(originalRouteIDs) == 0 {
		return []models.Route{}, nil
	}

	routes, err := api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs(ctx, originalRouteIDs)
	if err != nil {
		return nil, err
	}

	return buildRouteModels(ctx, agencyID, routes)
}

// buildRouteModels converts a slice of database routes into model routes.
// It is the single source of truth for mapping gtfsdb.Route → models.Route.
func buildRouteModels(ctx context.Context, agencyID string, routes []gtfsdb.Route) ([]models.Route, error) {
	modelRoutes := make([]models.Route, 0, len(routes))
	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		combinedID := utils.FormCombinedID(agencyID, route.ID)

		routeModel := models.NewRoute(
			combinedID,
			agencyID,
			route.ShortName.String,
			route.LongName.String,
			route.Desc.String,
			models.RouteType(route.Type),
			route.Url.String,
			route.Color.String,
			route.TextColor.String,
		)
		modelRoutes = append(modelRoutes, routeModel)
	}

	return modelRoutes, nil
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
	routesByStop, uniqueRouteMap := groupRoutesByStop(agencyID, allRoutes)

	modelStops := make([]models.Stop, 0, len(uniqueStopIDs))
	for _, stopID := range uniqueStopIDs {
		stop, exists := stopMap[stopID]
		if !exists {
			continue
		}
		combinedRouteIDs := api.combinedRouteIDsForStop(agencyID, routesByStop[stopID])
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
// builds a map of unique routes keyed by their agency-combined ID.
func groupRoutesByStop(agencyID string, allRoutes []gtfsdb.GetRoutesForStopsRow) (map[string][]gtfsdb.Route, map[string]gtfsdb.GetRoutesForStopsRow) {
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
		combinedID := utils.FormCombinedID(agencyID, routeRow.ID)
		uniqueRouteMap[combinedID] = routeRow
	}
	return routesByStop, uniqueRouteMap
}

// combinedRouteIDsForStop sorts the routes serving a stop into a stable, human-friendly
// order and returns their agency-combined IDs.
func (api *RestAPI) combinedRouteIDsForStop(agencyID string, routesForStop []gtfsdb.Route) []string {
	// Sort naturally by ShortName (falling back to LongName, then AgencyID, then ID) so the
	// route IDs are returned in a stable, human-friendly order.
	utils.SortRoutesByName(routesForStop, utils.DBRouteSortKey)
	combinedRouteIDs := make([]string, len(routesForStop))
	for i, rt := range routesForStop {
		combinedRouteIDs[i] = utils.FormCombinedID(agencyID, rt.ID)
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
