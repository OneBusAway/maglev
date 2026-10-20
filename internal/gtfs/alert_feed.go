package gtfs

import (
	"bytes"
	"cmp"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/logging"
)

// GtfsRealtimeVersion is the GTFS-RT version the exports declare.
const GtfsRealtimeVersion = "2.0"

// agencyAlertFeed is one agency's serialized GTFS-RT alerts feed and the
// build time at which its alerts last changed, which is its header time.
type agencyAlertFeed struct {
	body      []byte
	changedAt time.Time
}

// AlertFeedForAgency returns the current snapshot's serialized GTFS-RT
// alerts feed for an agency. An agency with no alerts since the server
// started gets a header-only feed dated at the first snapshot, so its bytes
// stay the same across rebuilds. The bytes are shared with every other
// reader and must not be modified.
func (manager *Manager) AlertFeedForAgency(agencyID string) ([]byte, error) {
	merged := manager.mergedRealtime()
	if feed, ok := merged.alertFeedsByAgency[agencyID]; ok {
		return feed.body, nil
	}
	since := merged.alertsSince
	// No snapshot is built on a server without realtime feeds.
	if since.IsZero() {
		since = time.Now()
	}
	return encodeAlertFeed(nil, since)
}

// buildAlertFeedsByAgency serializes every agency's alerts for the export.
// An agency whose alerts match the previous snapshot's keeps its bytes and
// change time; any other agency is dated builtAt. Agencies that had alerts
// before keep a feed, empty if need be, so their change is published.
// feedIDs must be sorted. Caller must hold realTimeMutex.
func (manager *Manager) buildAlertFeedsByAgency(feedIDs []string, previous *mergedRealtime, builtAt time.Time) map[string]agencyAlertFeed {
	entitiesByAgency := manager.collectAlertEntitiesByAgency(feedIDs)
	for agencyID := range previous.alertFeedsByAgency {
		if _, ok := entitiesByAgency[agencyID]; !ok {
			entitiesByAgency[agencyID] = nil
		}
	}

	feeds := make(map[string]agencyAlertFeed, len(entitiesByAgency))
	for agencyID, entities := range entitiesByAgency {
		slices.SortFunc(entities, func(a, b *gtfsrt.FeedEntity) int {
			return strings.Compare(a.GetId(), b.GetId())
		})
		feed, err := nextAgencyAlertFeed(entities, previous.alertFeedsByAgency[agencyID], builtAt)
		if err != nil {
			logging.LogError(slog.Default(), "Error encoding GTFS-RT alerts feed", err,
				slog.String("agency_id", agencyID))
			continue
		}
		feeds[agencyID] = feed
	}
	return feeds
}

// collectAlertEntitiesByAgency attributes each alert to one agency and
// converts it to a feed entity keyed by its combined situation ID. Feeds are
// read in configuration order, so when two feeds publish the same alert the
// one listed first wins, as does the first copy within one feed.
func (manager *Manager) collectAlertEntitiesByAgency(feedIDs []string) map[string][]*gtfsrt.FeedEntity {
	feedRanks := manager.feedConfigRanks(feedIDs)
	orderedFeedIDs := slices.Clone(feedIDs)
	slices.SortFunc(orderedFeedIDs, func(a, b string) int {
		return feedRanks[a] - feedRanks[b]
	})
	fallbackAgencies := manager.feedFallbackAgencies()

	collector := alertEntityCollector{
		entitiesByAgency: make(map[string][]*gtfsrt.FeedEntity),
		seen:             make(map[agencyEntityKey]bool),
	}
	for _, feedID := range orderedFeedIDs {
		for _, alert := range manager.feedAlerts[feedID] {
			collector.add(alert, exportAgencyID(alert, fallbackAgencies[feedID]))
		}
	}
	return collector.entitiesByAgency
}

// exportAgencyID is the one agency an alert is exported under: the first
// agency_id among its informed entities, else its feed's first agency-ids
// entry, else "" for none.
func exportAgencyID(alert gtfs.Alert, fallbackAgencyID string) string {
	if agencyID := AlertAgencyID(alert); agencyID != "" {
		return agencyID
	}
	return fallbackAgencyID
}

type agencyEntityKey struct {
	agencyID string
	entityID string
}

// alertEntityCollector files feed entities by agency, keeping the first
// entity with each ID.
type alertEntityCollector struct {
	entitiesByAgency map[string][]*gtfsrt.FeedEntity
	seen             map[agencyEntityKey]bool
}

func (collector *alertEntityCollector) add(alert gtfs.Alert, agencyID string) {
	if alert.ID == "" || agencyID == "" {
		return
	}
	key := agencyEntityKey{agencyID: agencyID, entityID: SituationID(alert.ID, agencyID)}
	if collector.seen[key] {
		return
	}
	collector.seen[key] = true
	collector.entitiesByAgency[agencyID] = append(collector.entitiesByAgency[agencyID], &gtfsrt.FeedEntity{
		Id:    proto.String(key.entityID),
		Alert: exportAlert(alert),
	})
}

// feedFallbackAgencies maps each configured feed with agency-ids to its
// first entry, the agency of alerts that name none.
func (manager *Manager) feedFallbackAgencies() map[string]string {
	fallbacks := make(map[string]string)
	for _, feed := range manager.config.enabledFeeds() {
		if len(feed.AgencyIDs) > 0 {
			fallbacks[feed.ID] = feed.AgencyIDs[0]
		}
	}
	return fallbacks
}

// nextAgencyAlertFeed encodes the agency's entities with the previous change
// time first: equal bytes mean the alerts didn't change, so the previous
// feed is kept as it is.
func nextAgencyAlertFeed(entities []*gtfsrt.FeedEntity, previous agencyAlertFeed, builtAt time.Time) (agencyAlertFeed, error) {
	if !previous.changedAt.IsZero() {
		body, err := encodeAlertFeed(entities, previous.changedAt)
		if err != nil {
			return agencyAlertFeed{}, err
		}
		if bytes.Equal(body, previous.body) {
			return previous, nil
		}
	}
	body, err := encodeAlertFeed(entities, builtAt)
	if err != nil {
		return agencyAlertFeed{}, err
	}
	return agencyAlertFeed{body: body, changedAt: builtAt}, nil
}

// encodeAlertFeed serializes a full-dataset feed deterministically, so the
// same alerts always give the same bytes and ETag.
func encodeAlertFeed(entities []*gtfsrt.FeedEntity, timestamp time.Time) ([]byte, error) {
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{
			GtfsRealtimeVersion: proto.String(GtfsRealtimeVersion),
			Timestamp:           proto.Uint64(uint64(timestamp.Unix())),
		},
		Entity: entities,
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(feed)
}

// exportAlert republishes the alert fields Java sends: texts, active periods
// and informed entities. Cause and effect are left out, as Java does.
func exportAlert(alert gtfs.Alert) *gtfsrt.Alert {
	return &gtfsrt.Alert{
		ActivePeriod:    exportActivePeriods(alert.ActivePeriods),
		InformedEntity:  exportInformedEntities(alert.InformedEntities),
		HeaderText:      exportTranslatedString(alert.Header),
		DescriptionText: exportTranslatedString(alert.Description),
		Url:             exportTranslatedString(alert.URL),
	}
}

// exportTranslatedString keeps every translation, and returns nil for a
// field the source doesn't have, so it stays absent rather than empty.
func exportTranslatedString(texts []gtfs.AlertText) *gtfsrt.TranslatedString {
	if len(texts) == 0 {
		return nil
	}
	translated := &gtfsrt.TranslatedString{}
	for _, text := range texts {
		translated.Translation = append(translated.Translation, &gtfsrt.TranslatedString_Translation{
			Text:     proto.String(text.Text),
			Language: nonEmptyString(text.Language),
		})
	}
	return translated
}

// exportActivePeriods keeps each period's set bounds, in seconds. A period
// with neither bound is dropped.
func exportActivePeriods(periods []gtfs.AlertActivePeriod) []*gtfsrt.TimeRange {
	var ranges []*gtfsrt.TimeRange
	for _, period := range periods {
		timeRange := &gtfsrt.TimeRange{
			Start: exportPeriodBound(period.StartsAt),
			End:   exportPeriodBound(period.EndsAt),
		}
		if timeRange.Start != nil || timeRange.End != nil {
			ranges = append(ranges, timeRange)
		}
	}
	return ranges
}

func exportPeriodBound(bound *time.Time) *uint64 {
	if bound == nil || bound.Unix() <= 0 {
		return nil
	}
	return proto.Uint64(uint64(bound.Unix()))
}

// exportInformedEntities keeps agency_id, route_id, trip_id and stop_id.
// Empty IDs are left out, because iOS reads agency_id "" as region-wide, and
// a selector left with no field is dropped. The selectors keep the source
// order, except for the trailing ones without an agency, which are sorted:
// go-gtfs appends a route selector for each route-only trip descriptor in
// map order, and the bytes must not change while the alert doesn't.
func exportInformedEntities(entities []gtfs.AlertInformedEntity) []*gtfsrt.EntitySelector {
	var selectors []*gtfsrt.EntitySelector
	for _, entity := range entities {
		selector := &gtfsrt.EntitySelector{
			AgencyId: nonEmptyStringPtr(entity.AgencyID),
			RouteId:  nonEmptyStringPtr(entity.RouteID),
			StopId:   nonEmptyStringPtr(entity.StopID),
		}
		if entity.TripID != nil && entity.TripID.ID != "" {
			selector.Trip = &gtfsrt.TripDescriptor{TripId: proto.String(entity.TripID.ID)}
		}
		hasField := selector.AgencyId != nil ||
			selector.RouteId != nil ||
			selector.StopId != nil ||
			selector.Trip != nil
		if hasField {
			selectors = append(selectors, selector)
		}
	}
	sortTrailingAgencylessSelectors(selectors)
	return selectors
}

// sortTrailingAgencylessSelectors sorts the selectors after the last one
// with an agency_id, so a selector naming the agency is never moved.
func sortTrailingAgencylessSelectors(selectors []*gtfsrt.EntitySelector) {
	tailStart := len(selectors)
	for tailStart > 0 && selectors[tailStart-1].AgencyId == nil {
		tailStart--
	}
	slices.SortStableFunc(selectors[tailStart:], func(a, b *gtfsrt.EntitySelector) int {
		return cmp.Or(
			strings.Compare(a.GetRouteId(), b.GetRouteId()),
			strings.Compare(a.GetTrip().GetTripId(), b.GetTrip().GetTripId()),
			strings.Compare(a.GetStopId(), b.GetStopId()),
		)
	})
}

func nonEmptyString(value string) *string {
	if value == "" {
		return nil
	}
	return proto.String(value)
}

func nonEmptyStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	return nonEmptyString(*value)
}
