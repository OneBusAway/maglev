package restapi

import (
	"slices"
	"strconv"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// selectExportAlerts returns, once each and in retained order, the alerts
// that name the requested agency or something it owns and, with a route
// filter, that name the route. Active windows are not checked: the export
// serves current, future and expired alerts alike.
func selectExportAlerts(alerts []internalgtfs.ExportAlert, req gtfsRealtimeExportRequest) []internalgtfs.ExportAlert {
	var selected []internalgtfs.ExportAlert
	for _, alert := range alerts {
		if !alertAffectsAgency(alert, req.AgencyID) {
			continue
		}
		if req.RouteFilterID != "" && !alertNamesRoute(alert, req.RouteFilterID) {
			continue
		}
		selected = append(selected, alert)
	}
	return selected
}

func alertNamesRoute(alert internalgtfs.ExportAlert, routeID string) bool {
	return slices.ContainsFunc(alert.Selectors, func(selector internalgtfs.ExportAlertSelector) bool {
		candidate, ok := alertRouteFilterCandidate(selector.Entity)
		return ok && candidate == routeID
	})
}

// alertRouteFilterCandidate is the route a selector matches a route filter
// with, as in legacy GtfsRealtimeAlertLibrary: its route_id, except that a
// trip descriptor naming a route but no trip replaces it. Routes owning a
// named trip or a stop never count.
func alertRouteFilterCandidate(entity gtfs.AlertInformedEntity) (string, bool) {
	if entity.TripID != nil && entity.TripID.ID == "" && entity.TripID.RouteID != "" {
		return entity.TripID.RouteID, true
	}
	if entity.RouteID == nil {
		return "", false
	}
	return *entity.RouteID, true
}

func alertAffectsAgency(alert internalgtfs.ExportAlert, agencyID string) bool {
	return slices.ContainsFunc(alert.Selectors, func(selector internalgtfs.ExportAlertSelector) bool {
		return selector.AffectsAgency(agencyID)
	})
}

// buildAlertsFeed maps selected alerts to feed entities. Identified alerts
// keep their legacy {recordAgency}_{rawID} entity ID whatever
// removeAgencyIds says; anonymous ones get the entity count as their ID,
// as legacy does, skipping any ID already in the feed.
func buildAlertsFeed(alerts []internalgtfs.ExportAlert, req gtfsRealtimeExportRequest) *gtfsrt.FeedMessage {
	feed := newGtfsRealtimeFeed(req.Time)
	usedIDs := make(map[string]bool, len(alerts))
	for _, alert := range alerts {
		if alert.Alert.ID != "" {
			usedIDs[alertEntityID(alert)] = true
		}
	}
	for _, alert := range alerts {
		entityID := alertEntityID(alert)
		if entityID == "" {
			entityID = nextFallbackAlertID(len(feed.Entity), usedIDs)
			usedIDs[entityID] = true
		}
		feed.Entity = append(feed.Entity, &gtfsrt.FeedEntity{
			Id:    proto.String(entityID),
			Alert: exportAlertPayload(alert, req.RemoveAgencyIDs),
		})
	}
	return feed
}

func alertEntityID(alert internalgtfs.ExportAlert) string {
	if alert.Alert.ID == "" {
		return ""
	}
	return utils.FormCombinedID(alert.RecordAgencyID, alert.Alert.ID)
}

// nextFallbackAlertID returns the first unused decimal ID from entityCount.
func nextFallbackAlertID(entityCount int, usedIDs map[string]bool) string {
	for candidate := entityCount; ; candidate++ {
		if id := strconv.Itoa(candidate); !usedIDs[id] {
			return id
		}
	}
}

func exportAlertPayload(alert internalgtfs.ExportAlert, removeAgencyIDs bool) *gtfsrt.Alert {
	payload := &gtfsrt.Alert{
		HeaderText:      exportTranslatedString(alert.Alert.Header),
		DescriptionText: exportTranslatedString(alert.Alert.Description),
		Url:             exportTranslatedString(alert.Alert.URL),
	}
	// go-gtfs stores cause and effect as plain values, so the UNKNOWN
	// defaults cannot be told apart from absent; do not emit them.
	if alert.Alert.Cause != gtfsrt.Alert_UNKNOWN_CAUSE {
		payload.Cause = alert.Alert.Cause.Enum()
	}
	if alert.Alert.Effect != gtfsrt.Alert_UNKNOWN_EFFECT {
		payload.Effect = alert.Alert.Effect.Enum()
	}
	for _, period := range alert.Alert.ActivePeriods {
		payload.ActivePeriod = append(payload.ActivePeriod, &gtfsrt.TimeRange{
			Start: optionalEpochSeconds(period.StartsAt),
			End:   optionalEpochSeconds(period.EndsAt),
		})
	}
	for _, selector := range alert.Selectors {
		payload.InformedEntity = append(payload.InformedEntity, exportAlertSelector(selector, removeAgencyIDs))
	}
	return payload
}

func exportTranslatedString(texts []gtfs.AlertText) *gtfsrt.TranslatedString {
	if len(texts) == 0 {
		return nil
	}
	translated := &gtfsrt.TranslatedString{}
	for _, text := range texts {
		translation := &gtfsrt.TranslatedString_Translation{Text: proto.String(text.Text)}
		if text.Language != "" {
			translation.Language = proto.String(text.Language)
		}
		translated.Translation = append(translated.Translation, translation)
	}
	return translated
}

func optionalEpochSeconds(t *time.Time) *uint64 {
	if t == nil {
		return nil
	}
	return proto.Uint64(uint64(t.Unix()))
}

// exportAlertSelector keeps every field of the informed entity. A missing
// agency ID is filled only when ownership is unambiguous, and route, trip
// and stop IDs are qualified with their own owners, staying raw when that
// owner is unknown.
func exportAlertSelector(selector internalgtfs.ExportAlertSelector, removeAgencyIDs bool) *gtfsrt.EntitySelector {
	entity := selector.Entity
	exported := &gtfsrt.EntitySelector{}
	if agencyID, ok := selector.AgencyID(); ok {
		exported.AgencyId = proto.String(agencyID)
	}
	if entity.RouteID != nil {
		exported.RouteId = proto.String(exportPayloadID(*entity.RouteID, selector.RouteOwner, removeAgencyIDs))
	}
	if entity.RouteType != gtfs.RouteType_Unknown {
		exported.RouteType = proto.Int32(int32(entity.RouteType))
	}
	exported.DirectionId = exportDirectionID(entity.DirectionID)
	if entity.TripID != nil {
		exported.Trip = alertTripDescriptor(*entity.TripID, selector.TripOwner, removeAgencyIDs)
	}
	if entity.StopID != nil {
		exported.StopId = proto.String(exportPayloadID(*entity.StopID, singleStopOwner(selector), removeAgencyIDs))
	}
	return exported
}

func exportDirectionID(direction gtfs.DirectionID) *uint32 {
	switch direction {
	case gtfs.DirectionID_True:
		return proto.Uint32(1)
	case gtfs.DirectionID_False:
		return proto.Uint32(0)
	}
	return nil
}

func alertTripDescriptor(tripID gtfs.TripID, owner string, removeAgencyIDs bool) *gtfsrt.TripDescriptor {
	descriptor := &gtfsrt.TripDescriptor{}
	if tripID.ID != "" {
		descriptor.TripId = proto.String(exportPayloadID(tripID.ID, owner, removeAgencyIDs))
	}
	if tripID.RouteID != "" {
		descriptor.RouteId = proto.String(exportPayloadID(tripID.RouteID, owner, removeAgencyIDs))
	}
	applyFeedTripFields(descriptor, tripID)
	return descriptor
}

func singleStopOwner(selector internalgtfs.ExportAlertSelector) string {
	if len(selector.StopOwners) != 1 {
		return ""
	}
	return selector.StopOwners[0]
}
