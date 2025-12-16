package restapi

import (
	"context"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) BuildRouteReferences(ctx context.Context, agencyID string, stops []models.Stop) ([]models.Route, error) {
	routeIDSet := make(map[string]bool)
	originalRouteIDs := make([]string, 0)

	for _, stop := range stops {
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

	modelRoutes := make([]models.Route, 0, len(routes))
	for _, route := range routes {
		routeModel := models.Route{
			ID:                utils.FormCombinedID(agencyID, route.ID),
			AgencyID:          agencyID,
			ShortName:         route.ShortName.String,
			LongName:          route.LongName.String,
			Description:       route.Desc.String,
			Type:              models.RouteType(route.Type),
			URL:               route.Url.String,
			Color:             route.Color.String,
			TextColor:         route.TextColor.String,
			NullSafeShortName: route.ShortName.String,
		}
		modelRoutes = append(modelRoutes, routeModel)
	}

	return modelRoutes, nil
}

func (api *RestAPI) BuildRouteReferencesAsInterface(ctx context.Context, agencyID string, stops []models.Stop) ([]interface{}, error) {
	routes, err := api.BuildRouteReferences(ctx, agencyID, stops)
	if err != nil {
		return nil, err
	}

	routeRefs := make([]interface{}, len(routes))
	for i, route := range routes {
		routeRefs[i] = route
	}

	return routeRefs, nil
}

func (api *RestAPI) BuildSituationReferences(alerts []gtfs.Alert, agencyID string) []models.Situation {
	situations := make([]models.Situation, 0, len(alerts))

	for _, alert := range alerts {
		situation := models.Situation{
			ID:                 alert.ID,
			CreationTime:       0,
			ActiveWindows:      make([]models.ActiveWindow, 0, len(alert.ActivePeriods)),
			AllAffects:         make([]models.AffectedEntity, 0, len(alert.InformedEntities)),
			ConsequenceMessage: "",
			Consequences:       []interface{}{},
			PublicationWindows: []interface{}{},
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
