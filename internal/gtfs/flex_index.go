package gtfs

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// FlexArea is one zone's full geometry, parsed once at reload for containment
// and distance tests. Display geometry is read from the database when needed.
type FlexArea struct {
	ID       string // bare location id
	Bounds   geo.CoordinateBounds
	Polygons [][][][2]float64
}

// FlexStopPoint is a stop referenced by a service's rules (directly or as a
// location-group member), with its coordinates for radius/viewport tests.
type FlexStopPoint struct {
	StopID string
	Lat    float64
	Lon    float64
}

// FlexIndex is the in-memory view of the on-demand tables, rebuilt in
// ReloadStatic and read without locks. Every field and every value an
// accessor returns must be treated as immutable.
//
// Combined service ids are built from ondemand_services.agency_id — the
// service's own agency — never from the requesting stop's or route's agency.
type FlexIndex struct {
	Areas           map[string]*FlexArea            // bare location id → area
	StopServiceIDs  map[string][]string             // bare stop id → sorted combined service ids
	RouteServiceIDs map[string][]string             // bare route id → sorted combined service ids
	ServiceBounds   map[string]geo.CoordinateBounds // combined service id → union of area bboxes and stop points
	ServiceAreaIDs  map[string][]string             // combined service id → sorted bare location ids from its records, each with an entry in Areas
	ServiceStops    map[string][]FlexStopPoint      // combined service id → rule-referenced stops, ordered by stop id
	BareServiceIDs  map[string]string               // combined service id → bare service id
}

// NewEmptyFlexIndex returns an index with no services; every lookup misses.
func NewEmptyFlexIndex() *FlexIndex {
	return &FlexIndex{
		Areas:           map[string]*FlexArea{},
		StopServiceIDs:  map[string][]string{},
		RouteServiceIDs: map[string][]string{},
		ServiceBounds:   map[string]geo.CoordinateBounds{},
		ServiceAreaIDs:  map[string][]string{},
		ServiceStops:    map[string][]FlexStopPoint{},
		BareServiceIDs:  map[string]string{},
	}
}

// buildFlexIndex reads the on-demand tables into a fresh index.
func buildFlexIndex(ctx context.Context, gtfsDB *gtfsdb.Client, logger *slog.Logger) (*FlexIndex, error) {
	idx := NewEmptyFlexIndex()

	services, err := gtfsDB.Queries.ListOnDemandServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list on-demand services: %w", err)
	}
	if len(services) == 0 {
		return idx, nil
	}

	combinedByBareService := make(map[string]string, len(services))
	for _, service := range services {
		combinedID := utils.FormCombinedID(service.AgencyID, service.ID)
		combinedByBareService[service.ID] = combinedID
		idx.BareServiceIDs[combinedID] = service.ID
		idx.RouteServiceIDs[service.RouteID] = append(idx.RouteServiceIDs[service.RouteID], combinedID)
	}

	if err := idx.loadAreas(ctx, gtfsDB, logger); err != nil {
		return nil, err
	}
	if err := idx.loadServiceAreas(ctx, gtfsDB, combinedByBareService); err != nil {
		return nil, err
	}
	if err := idx.loadStopPointers(ctx, gtfsDB, combinedByBareService); err != nil {
		return nil, err
	}

	idx.computeServiceBounds()
	idx.sortLists()
	warnInertRuleCalendars(ctx, gtfsDB, logger)
	warnDanglingPriorNoticeCalendars(ctx, gtfsDB, logger)
	return idx, nil
}

// warnInertRuleCalendars logs, once per reload rather than per request, each
// rule calendar the /ondemand builder drops because it compiles to no calendar.
// It is diagnostic only, so a failed query is logged and never fails the build.
func warnInertRuleCalendars(ctx context.Context, gtfsDB *gtfsdb.Client, logger *slog.Logger) {
	rows, err := gtfsDB.Queries.ListInertOnDemandRuleCalendars(ctx)
	if err != nil {
		logger.Warn("could not check on-demand rule calendars", slog.String("error", err.Error()))
		return
	}
	for _, row := range rows {
		logger.Warn("dropping on-demand rules whose calendar has no service days or added dates",
			slog.String("service_id", row.ServiceID), slog.String("gtfs_service_id", row.GtfsServiceID))
	}
}

// warnDanglingPriorNoticeCalendars logs, once per reload, each booking rule
// whose prior-notice service emits no base calendar; the /ondemand builder
// nulls its priorNoticeCalendarId. Diagnostic only, like warnInertRuleCalendars.
func warnDanglingPriorNoticeCalendars(ctx context.Context, gtfsDB *gtfsdb.Client, logger *slog.Logger) {
	rows, err := gtfsDB.Queries.ListBookingRulesWithoutPriorNoticeCalendar(ctx)
	if err != nil {
		logger.Warn("could not check prior-notice calendars", slog.String("error", err.Error()))
		return
	}
	for _, row := range rows {
		logger.Warn("dropping prior-notice calendar with no base calendar",
			slog.String("booking_rule_id", row.ID),
			slog.String("prior_notice_service_id", nulls.StringOrEmpty(row.PriorNoticeServiceID)))
	}
}

func (idx *FlexIndex) loadAreas(ctx context.Context, gtfsDB *gtfsdb.Client, logger *slog.Logger) error {
	locations, err := gtfsDB.Queries.ListLocations(ctx)
	if err != nil {
		return fmt.Errorf("list locations: %w", err)
	}
	for _, location := range locations {
		_, polygons, err := geo.ParseGeoJSONPolygons([]byte(location.Geometry))
		if err != nil {
			// Skip rather than fail: one bad zone must not wedge every reload.
			logger.Warn("skipping on-demand zone with unparseable geometry",
				slog.String("location_id", location.ID), slog.String("error", err.Error()))
			continue
		}
		idx.Areas[location.ID] = &FlexArea{
			ID:       location.ID,
			Bounds:   geo.CoordinateBounds{MinLat: location.MinLat, MaxLat: location.MaxLat, MinLon: location.MinLon, MaxLon: location.MaxLon},
			Polygons: polygons,
		}
	}
	return nil
}

// loadServiceAreas must run after loadAreas: it keeps only ids with an area.
func (idx *FlexIndex) loadServiceAreas(ctx context.Context, gtfsDB *gtfsdb.Client, combinedByBareService map[string]string) error {
	rows, err := gtfsDB.Queries.ListOnDemandServiceLocationIDs(ctx)
	if err != nil {
		return fmt.Errorf("list on-demand service locations: %w", err)
	}
	for _, row := range rows {
		combinedID, isKnownService := combinedByBareService[row.ServiceID]
		locationID := nulls.StringOrEmpty(row.LocationID)
		// A zone skipped at import for bad geometry is still referenced by
		// flex_stop_times; leave it out so every listed id has a FlexArea.
		_, hasArea := idx.Areas[locationID]
		if !isKnownService || !hasArea {
			continue
		}
		idx.ServiceAreaIDs[combinedID] = append(idx.ServiceAreaIDs[combinedID], locationID)
	}
	return nil
}

func (idx *FlexIndex) loadStopPointers(ctx context.Context, gtfsDB *gtfsdb.Client, combinedByBareService map[string]string) error {
	pointers, err := gtfsDB.Queries.ListOnDemandStopServices(ctx)
	if err != nil {
		return fmt.Errorf("list on-demand stop services: %w", err)
	}
	for _, pointer := range pointers {
		if combinedID, ok := combinedByBareService[pointer.ServiceID]; ok {
			idx.StopServiceIDs[pointer.StopID] = append(idx.StopServiceIDs[pointer.StopID], combinedID)
		}
	}

	points, err := gtfsDB.Queries.ListOnDemandServiceStopPoints(ctx)
	if err != nil {
		return fmt.Errorf("list on-demand service stop points: %w", err)
	}
	for _, point := range points {
		if combinedID, ok := combinedByBareService[point.ServiceID]; ok {
			idx.ServiceStops[combinedID] = append(idx.ServiceStops[combinedID], FlexStopPoint{StopID: point.StopID, Lat: point.Lat, Lon: point.Lon})
		}
	}
	return nil
}

// computeServiceBounds unions each service's area boxes and stop points. A
// service with neither (a zero-rule service whose zones failed to parse) gets
// no entry and is never a location candidate. ServiceAreaIDs only lists ids
// present in Areas, so the area lookup cannot miss.
func (idx *FlexIndex) computeServiceBounds() {
	for serviceID, areaIDs := range idx.ServiceAreaIDs {
		for _, areaID := range areaIDs {
			idx.addServiceBounds(serviceID, idx.Areas[areaID].Bounds)
		}
	}
	for serviceID, stops := range idx.ServiceStops {
		for _, stop := range stops {
			idx.addServiceBounds(serviceID, geo.CoordinateBounds{MinLat: stop.Lat, MaxLat: stop.Lat, MinLon: stop.Lon, MaxLon: stop.Lon})
		}
	}
}

func (idx *FlexIndex) addServiceBounds(serviceID string, bounds geo.CoordinateBounds) {
	if existing, ok := idx.ServiceBounds[serviceID]; ok {
		bounds = geo.UnionBounds(existing, bounds)
	}
	idx.ServiceBounds[serviceID] = bounds
}

func (idx *FlexIndex) sortLists() {
	for _, ids := range idx.StopServiceIDs {
		slices.Sort(ids)
	}
	for _, ids := range idx.RouteServiceIDs {
		slices.Sort(ids)
	}
	for _, ids := range idx.ServiceAreaIDs {
		slices.Sort(ids)
	}
}

// OnDemandServiceIDsForStop returns the combined ids of the services whose
// rules reference the bare stop id, or nil.
func (idx *FlexIndex) OnDemandServiceIDsForStop(stopID string) []string {
	return slices.Clone(idx.StopServiceIDs[stopID])
}

// OnDemandServiceIDsForRoute returns the combined ids of the services built
// from the bare route id, or nil.
func (idx *FlexIndex) OnDemandServiceIDsForRoute(routeID string) []string {
	return slices.Clone(idx.RouteServiceIDs[routeID])
}

// FlexArea returns the parsed zone for a bare location id, or nil.
func (idx *FlexIndex) FlexArea(locationID string) *FlexArea {
	return idx.Areas[locationID]
}

// ServiceBoundsOverlapping returns, sorted, the combined ids of every service
// whose bounds overlap the given box.
func (idx *FlexIndex) ServiceBoundsOverlapping(bounds geo.CoordinateBounds) []string {
	var matches []string
	for serviceID, serviceBounds := range idx.ServiceBounds {
		if !geo.IsOutOfBounds(bounds, serviceBounds) {
			matches = append(matches, serviceID)
		}
	}
	slices.Sort(matches)
	return matches
}

// IsFlexEmpty reports whether the loaded feed has no on-demand services.
func (idx *FlexIndex) IsFlexEmpty() bool {
	return len(idx.RouteServiceIDs) == 0
}

// FlexIndex returns the current on-demand index; never nil.
func (manager *Manager) FlexIndex() *FlexIndex {
	manager.staticMutex.RLock()
	defer manager.staticMutex.RUnlock()
	if manager.flexIndex == nil {
		return NewEmptyFlexIndex()
	}
	return manager.flexIndex
}
