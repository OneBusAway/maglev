package gtfs

import (
	"cmp"
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/utils"
)

// ExportAlert is an alert retained for the GTFS-RT alert export, with the
// record agency that namespaces its upstream ID. Unlike the JSON view it
// includes alerts the agency filter removed, and it outlives stale-feed
// clearing, as legacy OneBusAway alerts do.
type ExportAlert struct {
	RecordAgencyID string
	Alert          gtfs.Alert
	// Selectors pairs each informed entity of Alert with its static owners.
	Selectors []ExportAlertSelector
}

// ExportAlertSelector is one informed entity with the agencies that own
// what it names, resolved from static data when the alert was applied.
type ExportAlertSelector struct {
	Entity     gtfs.AlertInformedEntity
	RouteOwner string
	TripOwner  string
	// StopOwners lists every agency whose trips serve the stop.
	StopOwners []string
}

// AffectsAgency reports whether the agency owns the scope the selector
// names.
func (selector ExportAlertSelector) AffectsAgency(agencyID string) bool {
	return slices.Contains(selector.scopeAgencies(), agencyID)
}

// AgencyID returns the selector's explicit agency, or else the single
// agency that owns its scope. A stop served by several agencies is
// ambiguous, so no agency is guessed.
func (selector ExportAlertSelector) AgencyID() (string, bool) {
	owners := selector.scopeAgencies()
	if len(owners) != 1 {
		return "", false
	}
	return owners[0], true
}

// scopeAgencies returns the agencies owning the one scope the selector
// names. Its fields apply together, so the most specific field decides: an
// explicit agency, else the route, else the trip, else the stop. A stop
// owner never stands in for a route or trip, since raw stop IDs can
// collide across agencies in consolidated static data; a route or trip
// with no known owner leaves the scope unresolved.
func (selector ExportAlertSelector) scopeAgencies() []string {
	entity := selector.Entity
	switch {
	case entity.AgencyID != nil:
		return []string{*entity.AgencyID}
	case entity.RouteID != nil:
		return knownOwner(selector.RouteOwner)
	case entity.TripID != nil:
		return knownOwner(selector.TripOwner)
	default:
		return selector.StopOwners
	}
}

func knownOwner(agencyID string) []string {
	if agencyID == "" {
		return nil
	}
	return []string{agencyID}
}

// ExportAlerts returns the alerts retained for the GTFS-RT alert export:
// identified alerts in the order they were last applied, then anonymous
// alerts by feed. Callers must not mutate the result.
func (manager *Manager) ExportAlerts() []ExportAlert {
	return manager.mergedRealtime().exportAlerts
}

// exportAlertKey is the legacy identity of an alert: record agency plus raw
// upstream ID, independent of the feed that supplied it.
type exportAlertKey struct {
	recordAgencyID string
	rawID          string
}

// appliedExportAlert is the current record for a key, with the feed that
// last applied it and when, so omission cleanup only touches that feed's
// records and the export keeps application order.
type appliedExportAlert struct {
	alert        ExportAlert
	sourceFeedID string
	sequence     uint64
}

// exportAlertRefresh returns the alerts a successful alert fetch applies to
// export state, with their selector owners. Like legacy, a feed without a
// header timestamp is treated as a connection problem and changes nothing,
// and a feed with no record agency has no identity to key its alerts by.
// A failed owner lookup also changes nothing rather than store alerts that
// would select no agency. It queries static data, so the caller must not
// hold realTimeMutex.
func (manager *Manager) exportAlertRefresh(ctx context.Context, feedCfg RTFeedConfig, alertData *gtfs.Realtime, alertErr error) ([]ExportAlert, bool) {
	if alertData == nil || alertErr != nil || alertData.CreatedAt.IsZero() {
		return nil, false
	}
	recordAgencyID, ok := feedCfg.alertRecordAgencyID()
	if !ok {
		return nil, false
	}
	owners, err := manager.resolveAlertSelectorOwners(ctx, alertData.Alerts)
	if err != nil {
		logging.LogError(logging.ForComponent(ctx, "gtfs_realtime"), "Error resolving alert owners for export", err,
			slog.String("feed", feedCfg.ID))
		return nil, false
	}
	alerts := make([]ExportAlert, 0, len(alertData.Alerts))
	for _, alert := range alertData.Alerts {
		alerts = append(alerts, ExportAlert{
			RecordAgencyID: recordAgencyID,
			Alert:          alert,
			Selectors:      owners.selectors(alert.InformedEntities),
		})
	}
	return alerts, true
}

// alertSelectorOwners holds the owning agencies of the routes, trips and
// stops one alert feed names.
type alertSelectorOwners struct {
	routeAgencies map[string]string
	tripAgencies  map[string]string
	stopAgencies  map[string][]string
}

// resolveAlertSelectorOwners looks up, in batches, who owns every route,
// trip and stop the alerts name. A trip is owned through its descriptor's
// route, else through its static route.
func (manager *Manager) resolveAlertSelectorOwners(ctx context.Context, alerts []gtfs.Alert) (alertSelectorOwners, error) {
	routeIDs := make(map[string]struct{})
	collectAlertRouteIDs(routeIDs, alerts)
	routeAgencies, err := manager.buildRouteAgencyMap(ctx, routeIDs)
	if err != nil {
		return alertSelectorOwners{}, err
	}
	tripAgencies, err := manager.tripAgencyIDs(ctx, alertSelectorTrips(alerts), routeAgencies)
	if err != nil {
		return alertSelectorOwners{}, err
	}
	stopAgencies, err := manager.stopAgencyIDs(ctx, alertSelectorStopIDs(alerts))
	if err != nil {
		return alertSelectorOwners{}, err
	}
	return alertSelectorOwners{routeAgencies: routeAgencies, tripAgencies: tripAgencies, stopAgencies: stopAgencies}, nil
}

func (owners alertSelectorOwners) selectors(entities []gtfs.AlertInformedEntity) []ExportAlertSelector {
	selectors := make([]ExportAlertSelector, 0, len(entities))
	for _, entity := range entities {
		selector := ExportAlertSelector{Entity: entity}
		if entity.RouteID != nil {
			selector.RouteOwner = owners.routeAgencies[*entity.RouteID]
		}
		if entity.TripID != nil {
			selector.TripOwner = owners.tripOwner(*entity.TripID)
		}
		if entity.StopID != nil {
			selector.StopOwners = owners.stopAgencies[*entity.StopID]
		}
		selectors = append(selectors, selector)
	}
	return selectors
}

func (owners alertSelectorOwners) tripOwner(tripID gtfs.TripID) string {
	if agencyID, ok := owners.routeAgencies[tripID.RouteID]; ok {
		return agencyID
	}
	if tripID.ID == "" {
		return ""
	}
	return owners.tripAgencies[tripID.ID]
}

// alertSelectorTrips returns the trips alert selectors name by trip ID, in
// the shape tripAgencyIDs resolves.
func alertSelectorTrips(alerts []gtfs.Alert) []gtfs.Trip {
	var trips []gtfs.Trip
	for _, alert := range alerts {
		for _, entity := range alert.InformedEntities {
			if entity.TripID != nil && entity.TripID.ID != "" {
				trips = append(trips, gtfs.Trip{ID: *entity.TripID})
			}
		}
	}
	return trips
}

func alertSelectorStopIDs(alerts []gtfs.Alert) []string {
	stopIDs := make(map[string]struct{})
	for _, alert := range alerts {
		for _, entity := range alert.InformedEntities {
			if entity.StopID != nil && *entity.StopID != "" {
				stopIDs[*entity.StopID] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(stopIDs))
}

// stopAgencyIDs maps each stop to every agency whose trips serve it, in
// agency ID order. Stops without service are left out.
func (manager *Manager) stopAgencyIDs(ctx context.Context, stopIDs []string) (map[string][]string, error) {
	stopAgencies := make(map[string][]string, len(stopIDs))
	if len(stopIDs) == 0 {
		return stopAgencies, nil
	}
	rows, err := utils.QueryInBatches(ctx, stopIDs, manager.GtfsDB.Queries.GetAgenciesForStops)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		stopAgencies[row.StopID] = append(stopAgencies[row.StopID], row.ID)
	}
	return stopAgencies, nil
}

// applyExportAlertRefreshLocked applies one feed's refresh in feed order.
// A record replaces the whole record for its key, whichever feed applied it
// before; a deletion removes the key globally; keys this feed applied
// earlier and now omits are removed, and older copies from other feeds are
// never revived. Anonymous alerts are replaced per feed. The caller holds
// realTimeMutex.
func (manager *Manager) applyExportAlertRefreshLocked(feedID string, alerts []ExportAlert) {
	if manager.exportAlertsByKey == nil {
		manager.exportAlertsByKey = make(map[exportAlertKey]appliedExportAlert)
		manager.feedAnonymousExportAlerts = make(map[string][]ExportAlert)
	}
	var anonymous []ExportAlert
	applied := make(map[exportAlertKey]bool, len(alerts))
	for _, alert := range alerts {
		if alert.Alert.ID == "" {
			if !alert.Alert.IsDeleted {
				anonymous = append(anonymous, alert)
			}
			continue
		}
		key := exportAlertKey{recordAgencyID: alert.RecordAgencyID, rawID: alert.Alert.ID}
		if alert.Alert.IsDeleted {
			delete(manager.exportAlertsByKey, key)
			continue
		}
		manager.exportAlertSequence++
		manager.exportAlertsByKey[key] = appliedExportAlert{alert: alert, sourceFeedID: feedID, sequence: manager.exportAlertSequence}
		applied[key] = true
	}
	for key, current := range manager.exportAlertsByKey {
		if current.sourceFeedID == feedID && !applied[key] {
			delete(manager.exportAlertsByKey, key)
		}
	}
	manager.feedAnonymousExportAlerts[feedID] = anonymous
}

// buildExportAlertsLocked assembles the export view. The caller holds
// realTimeMutex.
func (manager *Manager) buildExportAlertsLocked() []ExportAlert {
	identified := make([]appliedExportAlert, 0, len(manager.exportAlertsByKey))
	for _, current := range manager.exportAlertsByKey {
		identified = append(identified, current)
	}
	slices.SortFunc(identified, func(a, b appliedExportAlert) int {
		return cmp.Compare(a.sequence, b.sequence)
	})

	alerts := make([]ExportAlert, 0, len(identified))
	for _, current := range identified {
		alerts = append(alerts, current.alert)
	}
	feedIDs := make([]string, 0, len(manager.feedAnonymousExportAlerts))
	for feedID := range manager.feedAnonymousExportAlerts {
		feedIDs = append(feedIDs, feedID)
	}
	slices.Sort(feedIDs)
	for _, feedID := range feedIDs {
		alerts = append(alerts, manager.feedAnonymousExportAlerts[feedID]...)
	}
	return alerts
}
