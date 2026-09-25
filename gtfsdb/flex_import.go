package gtfsdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/OneBusAway/go-gtfs"
	"github.com/OneBusAway/go-gtfs/warnings"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/nulls"
)

// maxLoggedStaticWarnings caps per-row warning lines so a badly malformed feed
// cannot flood the log; the remainder is summarised in one line.
const maxLoggedStaticWarnings = 200

// errLocationHasNoPolygons marks a location with no area to store a bbox for.
var errLocationHasNoPolygons = errors.New("location has no polygons")

// logStaticWarnings logs each parser warning (kind, file, row) up to the cap.
// The importer already logs the total count; this makes individual skipped
// rows visible, which is how a dropped flex record gets diagnosed.
func logStaticWarnings(logger *slog.Logger, staticWarnings []warnings.StaticWarning) {
	for i, warning := range staticWarnings {
		if i == maxLoggedStaticWarnings {
			logger.Warn("gtfs_static_warnings_truncated",
				slog.Int("remaining", len(staticWarnings)-maxLoggedStaticWarnings))
			return
		}
		kind := "unknown"
		if warning.Kind != nil {
			kind = warning.Kind.Error()
		}
		logger.Warn("gtfs_static_warning",
			slog.String("kind", kind),
			slog.String("file", string(warning.File)),
			slog.Int("row", warning.RowNumber))
	}
}

// storeFlexEntities inserts the flex tables that stop_times and rules reference:
// booking rules, locations, location groups and their members. It runs after
// stops (location_group_stops references stops) and before trips.
func (c *Client) storeFlexEntities(ctx context.Context, staticData *gtfs.Static, insertedStopIDs map[string]struct{}, qtx *Queries) error {
	if err := storeBookingRules(ctx, staticData.BookingRules, qtx); err != nil {
		return err
	}
	if err := storeLocations(ctx, staticData.Locations, qtx); err != nil {
		return err
	}
	return storeLocationGroups(ctx, staticData.LocationGroups, insertedStopIDs, qtx)
}

func storeBookingRules(ctx context.Context, rules []gtfs.BookingRule, qtx *Queries) error {
	for _, rule := range lastByID(rules, func(rule gtfs.BookingRule) string { return rule.Id }) {
		if err := qtx.CreateBookingRule(ctx, bookingRuleParams(rule)); err != nil {
			return fmt.Errorf("unable to create booking rule %s: %w", rule.Id, err)
		}
	}
	return nil
}

// storeLocations skips, with a warning, any location whose geometry cannot be
// stored; flex_stop_times.location_id has no foreign key, so the import goes on.
func storeLocations(ctx context.Context, locations []gtfs.Location, qtx *Queries) error {
	for _, location := range lastByID(locations, func(location gtfs.Location) string { return location.Id }) {
		params, err := locationParams(location)
		if err != nil {
			slog.Default().With(slog.String("component", "gtfs_importer")).Warn("gtfs_flex_location_skipped",
				slog.String("location_id", location.Id),
				slog.String("reason", err.Error()))
			continue
		}
		if err := qtx.CreateLocation(ctx, params); err != nil {
			return fmt.Errorf("unable to create location %s: %w", location.Id, err)
		}
	}
	return nil
}

func storeLocationGroups(ctx context.Context, groups []gtfs.LocationGroup, insertedStopIDs map[string]struct{}, qtx *Queries) error {
	for _, group := range lastByID(groups, func(group gtfs.LocationGroup) string { return group.Id }) {
		if err := qtx.CreateLocationGroup(ctx, CreateLocationGroupParams{
			ID:   group.Id,
			Name: nulls.NonEmptyString(group.Name),
		}); err != nil {
			return fmt.Errorf("unable to create location group %s: %w", group.Id, err)
		}
		if err := storeLocationGroupStops(ctx, group, insertedStopIDs, qtx); err != nil {
			return err
		}
	}
	return nil
}

func storeLocationGroupStops(ctx context.Context, group gtfs.LocationGroup, insertedStopIDs map[string]struct{}, qtx *Queries) error {
	storedStopIDs := make(map[string]struct{}, len(group.Stops))
	for _, stop := range group.Stops {
		// A member without coordinates was not inserted into stops (see the
		// lat/lon skip in StoreGtfsData) and would fail the foreign key.
		if _, ok := insertedStopIDs[stop.Id]; !ok {
			continue
		}
		if _, duplicate := storedStopIDs[stop.Id]; duplicate {
			continue
		}
		storedStopIDs[stop.Id] = struct{}{}
		if err := qtx.CreateLocationGroupStop(ctx, CreateLocationGroupStopParams{
			LocationGroupID: group.Id,
			StopID:          stop.Id,
		}); err != nil {
			return fmt.Errorf("unable to create location group stop %s/%s: %w", group.Id, stop.Id, err)
		}
	}
	return nil
}

// lastByID keeps only the last item for each id, in the order those last
// items appear. go-gtfs does not reject duplicate ids and resolves references
// to the last one, so the database must agree and the primary keys must hold.
func lastByID[T any](items []T, id func(T) string) []T {
	lastIndex := make(map[string]int, len(items))
	for i, item := range items {
		lastIndex[id(item)] = i
	}
	unique := make([]T, 0, len(lastIndex))
	for i, item := range items {
		if lastIndex[id(item)] == i {
			unique = append(unique, item)
		}
	}
	return unique
}

// storeFlexStopTimes writes every windowed record to flex_stop_times. It runs
// after trips (foreign key) and mirrors the timed-only stop_times insert.
func (c *Client) storeFlexStopTimes(ctx context.Context, staticData *gtfs.Static, qtx *Queries) error {
	for i := range staticData.Trips {
		trip := &staticData.Trips[i]
		for _, st := range trip.StopTimes {
			if !st.IsWindowed() {
				continue
			}
			if err := qtx.CreateFlexStopTime(ctx, flexStopTimeParams(trip.ID, st)); err != nil {
				return fmt.Errorf("unable to create flex stop time %s/%d: %w", trip.ID, st.StopSequence, err)
			}
		}
	}
	return nil
}

func bookingRuleParams(rule gtfs.BookingRule) CreateBookingRuleParams {
	return CreateBookingRuleParams{
		ID:                     rule.Id,
		BookingType:            int64(rule.Type),
		PriorNoticeDurationMin: nullInt64FromPtr(rule.PriorNoticeDurationMin),
		PriorNoticeDurationMax: nullInt64FromPtr(rule.PriorNoticeDurationMax),
		PriorNoticeLastDay:     nullInt64FromPtr(rule.PriorNoticeLastDay),
		PriorNoticeLastTime:    nullInt64FromPtr(rule.PriorNoticeLastTime),
		PriorNoticeStartDay:    nullInt64FromPtr(rule.PriorNoticeStartDay),
		PriorNoticeStartTime:   nullInt64FromPtr(rule.PriorNoticeStartTime),
		PriorNoticeServiceID:   nulls.NonEmptyString(rule.PriorNoticeServiceId),
		Message:                nulls.NonEmptyString(rule.Message),
		PickupMessage:          nulls.NonEmptyString(rule.PickupMessage),
		DropOffMessage:         nulls.NonEmptyString(rule.DropOffMessage),
		PhoneNumber:            nulls.NonEmptyString(rule.PhoneNumber),
		InfoUrl:                nulls.NonEmptyString(rule.InfoUrl),
		BookingUrl:             nulls.NonEmptyString(rule.BookingUrl),
	}
}

// locationParams stores the feed geometry verbatim, its bounding box, and the
// display geometry (NULL when simplification changed nothing).
func locationParams(location gtfs.Location) (CreateLocationParams, error) {
	if len(location.Geometry.Polygons) == 0 {
		return CreateLocationParams{}, errLocationHasNoPolygons
	}
	bounds := geo.PolygonsBounds(location.Geometry.Polygons)
	params := CreateLocationParams{
		ID:          location.Id,
		Name:        nulls.NonEmptyString(location.Name),
		Description: nulls.NonEmptyString(location.Description),
		Geometry:    string(location.Geometry.Raw),
		MinLat:      bounds.MinLat,
		MaxLat:      bounds.MaxLat,
		MinLon:      bounds.MinLon,
		MaxLon:      bounds.MaxLon,
	}

	simplified := geo.SimplifyPolygons(location.Geometry.Polygons)
	if !simplified.Changed {
		return params, nil
	}
	encoded, err := geo.EncodeGeoJSONGeometry(location.Geometry.Type, simplified.Polygons)
	if err != nil {
		return CreateLocationParams{}, err
	}
	params.GeometrySimplified = nulls.String(string(encoded))
	return params, nil
}

func flexStopTimeParams(tripID string, st gtfs.ScheduledStopTime) CreateFlexStopTimeParams {
	params := CreateFlexStopTimeParams{
		TripID:                   tripID,
		StopSequence:             int64(st.StopSequence),
		StartPickupDropOffWindow: int64(*st.StartPickupDropOffWindow),
		EndPickupDropOffWindow:   int64(*st.EndPickupDropOffWindow),
		PickupType:               int64(st.PickupType),
		DropOffType:              int64(st.DropOffType),
		SafeDurationFactor:       nullFloat64FromPtr(st.SafeDurationFactor),
		SafeDurationOffset:       nullFloat64FromPtr(st.SafeDurationOffset),
	}
	if st.Stop != nil {
		params.StopID = nulls.String(st.Stop.Id)
	}
	if st.Location != nil {
		params.LocationID = nulls.String(st.Location.Id)
	}
	if st.LocationGroup != nil {
		params.LocationGroupID = nulls.String(st.LocationGroup.Id)
	}
	if st.PickupBookingRule != nil {
		params.PickupBookingRuleID = nulls.String(st.PickupBookingRule.Id)
	}
	if st.DropOffBookingRule != nil {
		params.DropOffBookingRuleID = nulls.String(st.DropOffBookingRule.Id)
	}
	return params
}

// nullInt64FromPtr maps an optional integer-like value (int32 counts, or a
// time.Duration in nanoseconds since midnight) to its nullable column.
func nullInt64FromPtr[T ~int32 | ~int64](value *T) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return nulls.Int64(int64(*value))
}

func nullFloat64FromPtr(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

// storeOnDemand writes the compiled services, rules and stop pointers. It runs
// after trips and flex_stop_times and before the stop agency index, which reads
// ondemand_stop_services.
func (c *Client) storeOnDemand(ctx context.Context, compiled CompiledOnDemand, qtx *Queries) error {
	if err := storeOnDemandServices(ctx, compiled.Services, qtx); err != nil {
		return err
	}
	if err := storeOnDemandRules(ctx, compiled.Rules, qtx); err != nil {
		return err
	}
	return storeOnDemandStopServices(ctx, compiled.StopServices, qtx)
}

func storeOnDemandServices(ctx context.Context, services []CompiledService, qtx *Queries) error {
	for _, service := range services {
		if err := qtx.CreateOnDemandService(ctx, CreateOnDemandServiceParams{
			ID:          service.ID,
			AgencyID:    service.AgencyID,
			RouteID:     service.RouteID,
			ServiceKind: service.Kind,
		}); err != nil {
			return fmt.Errorf("unable to create on-demand service %s: %w", service.ID, err)
		}
	}
	return nil
}

func storeOnDemandRules(ctx context.Context, rules []CompiledRule, qtx *Queries) error {
	for _, rule := range rules {
		if err := qtx.CreateOnDemandRule(ctx, onDemandRuleParams(rule)); err != nil {
			return fmt.Errorf("unable to create on-demand rule for %s: %w", rule.ServiceID, err)
		}
	}
	return nil
}

func storeOnDemandStopServices(ctx context.Context, stopServices map[string][]string, qtx *Queries) error {
	for stopID, serviceIDs := range stopServices {
		for _, serviceID := range serviceIDs {
			if err := qtx.CreateOnDemandStopService(ctx, CreateOnDemandStopServiceParams{
				StopID:    stopID,
				ServiceID: serviceID,
			}); err != nil {
				return fmt.Errorf("unable to create on-demand stop service %s/%s: %w", stopID, serviceID, err)
			}
		}
	}
	return nil
}

func onDemandRuleParams(rule CompiledRule) CreateOnDemandRuleParams {
	return CreateOnDemandRuleParams{
		ServiceID:            rule.ServiceID,
		TripID:               rule.TripID,
		FromID:               rule.FromID,
		FromKind:             int64(rule.FromKind),
		ToID:                 rule.ToID,
		ToKind:               int64(rule.ToKind),
		StartPickupTime:      nullInt64FromPtr(rule.StartPickupTime),
		EndPickupTime:        nullInt64FromPtr(rule.EndPickupTime),
		EndDropOffTime:       nullInt64FromPtr(rule.EndDropOffTime),
		GtfsServiceID:        rule.GTFSServiceID,
		PickupType:           rule.PickupType,
		DropOffType:          rule.DropOffType,
		PickupBookingRuleID:  nullStringFromPtr(rule.PickupBookingRuleID),
		DropOffBookingRuleID: nullStringFromPtr(rule.DropOffBookingRuleID),
		SafeDurationFactor:   nullFloat64FromPtr(rule.SafeDurationFactor),
		SafeDurationOffset:   nullFloat64FromPtr(rule.SafeDurationOffset),
	}
}

func nullStringFromPtr(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return nulls.String(*value)
}
