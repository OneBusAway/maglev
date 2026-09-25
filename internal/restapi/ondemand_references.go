package restapi

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// geoPoint is the services-for-location query point in point mode.
type geoPoint struct {
	Lat float64
	Lon float64
}

// onDemandBuildOptions controls what a builder run embeds in references.
type onDemandBuildOptions struct {
	GeometryDetail GeometryDetail
	// QueryPoint, when set, fills distanceToArea and nearestPointOnBoundary.
	QueryPoint *geoPoint
}

// agencyScopedID pairs a bare id with the agency that prefixes it on the wire.
// Zone, group, booking-rule and calendar ids take the agency of the service
// whose rules reference them, so the same bare id can appear under two agencies.
// Stop ids are collected the same way, then re-keyed by each stop's own /where
// agency (stopAgencyIDs.rescope).
type agencyScopedID struct {
	AgencyID string
	ID       string
}

func (id agencyScopedID) combined() string { return utils.FormCombinedID(id.AgencyID, id.ID) }

// onDemandReferenceIDs is everything a batch of services references.
type onDemandReferenceIDs struct {
	locations    map[agencyScopedID]struct{}
	groups       map[agencyScopedID]struct{}
	bookingRules map[agencyScopedID]struct{}
	gtfsServices map[agencyScopedID]struct{}
	stops        map[agencyScopedID]struct{}
}

func newOnDemandReferenceIDs() *onDemandReferenceIDs {
	return &onDemandReferenceIDs{
		locations:    map[agencyScopedID]struct{}{},
		groups:       map[agencyScopedID]struct{}{},
		bookingRules: map[agencyScopedID]struct{}{},
		gtfsServices: map[agencyScopedID]struct{}{},
		stops:        map[agencyScopedID]struct{}{},
	}
}

func (ids *onDemandReferenceIDs) addRule(agencyID string, row gtfsdb.OndemandRule) {
	ids.addEndpoint(agencyID, row.FromID, gtfsdb.EndpointKind(row.FromKind))
	ids.addEndpoint(agencyID, row.ToID, gtfsdb.EndpointKind(row.ToKind))
	addScoped(ids.bookingRules, agencyID, nulls.StringOrEmpty(row.PickupBookingRuleID))
	addScoped(ids.bookingRules, agencyID, nulls.StringOrEmpty(row.DropOffBookingRuleID))
	addScoped(ids.gtfsServices, agencyID, row.GtfsServiceID)
}

func (ids *onDemandReferenceIDs) addEndpoint(agencyID, id string, kind gtfsdb.EndpointKind) {
	switch kind {
	case gtfsdb.EndpointStop:
		addScoped(ids.stops, agencyID, id)
	case gtfsdb.EndpointLocation:
		addScoped(ids.locations, agencyID, id)
	case gtfsdb.EndpointGroup:
		addScoped(ids.groups, agencyID, id)
	}
}

// addRecord adds what a flex stop_time record references, so a zero-rule
// service still carries its zones, groups and booking rules.
func (ids *onDemandReferenceIDs) addRecord(agencyID string, row gtfsdb.GetFlexRecordReferencesForRoutesRow) {
	addScoped(ids.locations, agencyID, nulls.StringOrEmpty(row.LocationID))
	addScoped(ids.groups, agencyID, nulls.StringOrEmpty(row.LocationGroupID))
	addScoped(ids.bookingRules, agencyID, nulls.StringOrEmpty(row.PickupBookingRuleID))
	addScoped(ids.bookingRules, agencyID, nulls.StringOrEmpty(row.DropOffBookingRuleID))
}

func addScoped(set map[agencyScopedID]struct{}, agencyID, id string) {
	if id != "" {
		set[agencyScopedID{AgencyID: agencyID, ID: id}] = struct{}{}
	}
}

// bareIDs returns the distinct bare ids of a scoped set, for one batched query.
func bareIDs(set map[agencyScopedID]struct{}) []string {
	ids := make([]string, 0, len(set))
	for scoped := range set {
		ids = append(ids, scoped.ID)
	}
	return utils.SortedUnique(ids)
}

// buildOnDemandServices turns service rows into wire services with their rules
// and the complete /ondemand references block. Services and every reference
// array come back sorted by id.
func (api *RestAPI) buildOnDemandServices(ctx context.Context, services []gtfsdb.OndemandService, opts onDemandBuildOptions) ([]models.OnDemandService, *models.OnDemandReferences, error) {
	references := models.NewEmptyOnDemandReferences()
	if len(services) == 0 {
		return []models.OnDemandService{}, references, nil
	}

	rulesByService, err := api.loadOnDemandRules(ctx, services)
	if err != nil {
		return nil, nil, err
	}
	recordsByRoute, err := api.loadFlexRecordReferences(ctx, services)
	if err != nil {
		return nil, nil, err
	}
	ids := newOnDemandReferenceIDs()
	for _, service := range services {
		for _, row := range rulesByService[service.ID] {
			ids.addRule(service.AgencyID, row)
		}
		for _, row := range recordsByRoute[service.RouteID] {
			ids.addRecord(service.AgencyID, row)
		}
	}

	groups, err := api.loadOnDemandLocationGroups(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	stopAgencies, err := api.loadStopAgencyIDs(ctx, ids.stops)
	if err != nil {
		return nil, nil, err
	}
	ids.stops = stopAgencies.rescope(ids.stops)
	references.LocationGroups = locationGroupReferences(groups, stopAgencies)

	// Booking rules may name a prior-notice service, whose calendar joins the block.
	references.BookingRules, err = api.onDemandBookingRules(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	calendarIDs, calendars, err := api.onDemandCalendars(ctx, ids.gtfsServices)
	if err != nil {
		return nil, nil, err
	}
	references.Calendars = calendars
	dropDanglingPriorNoticeCalendars(references.BookingRules, calendars)

	routes, err := api.loadRoutesByBareID(ctx, services)
	if err != nil {
		return nil, nil, err
	}
	serviceModels := make([]models.OnDemandService, 0, len(services))
	for _, service := range services {
		scope := serviceIDScope{agencyID: service.AgencyID, stopAgencies: stopAgencies}
		rules := buildAvailabilityRules(rulesByService[service.ID], scope, calendarIDsForAgency(calendarIDs, service.AgencyID))
		serviceModels = append(serviceModels, onDemandServiceModel(service, routes[service.RouteID], rules))
	}
	utils.SortByKey(serviceModels, func(s models.OnDemandService) string { return s.ID })

	if err := api.fillOnDemandReferences(ctx, references, services, routes, ids, opts); err != nil {
		return nil, nil, err
	}
	sortOnDemandReferences(references)
	return serviceModels, references, nil
}

func (api *RestAPI) loadOnDemandRules(ctx context.Context, services []gtfsdb.OndemandService) (map[string][]gtfsdb.OndemandRule, error) {
	serviceIDs := make([]string, 0, len(services))
	for _, service := range services {
		serviceIDs = append(serviceIDs, service.ID)
	}
	rows, err := queryInBatches(ctx, serviceIDs, api.GtfsManager.GtfsDB.Queries.GetOnDemandRulesForServices)
	if err != nil {
		return nil, fmt.Errorf("load on-demand rules: %w", err)
	}
	byService := make(map[string][]gtfsdb.OndemandRule, len(services))
	for _, row := range rows {
		byService[row.ServiceID] = append(byService[row.ServiceID], row)
	}
	return byService, nil
}

func (api *RestAPI) loadFlexRecordReferences(ctx context.Context, services []gtfsdb.OndemandService) (map[string][]gtfsdb.GetFlexRecordReferencesForRoutesRow, error) {
	routeIDs := make([]string, 0, len(services))
	for _, service := range services {
		routeIDs = append(routeIDs, service.RouteID)
	}
	rows, err := queryInBatches(ctx, routeIDs, api.GtfsManager.GtfsDB.Queries.GetFlexRecordReferencesForRoutes)
	if err != nil {
		return nil, fmt.Errorf("load flex record references: %w", err)
	}
	byRoute := make(map[string][]gtfsdb.GetFlexRecordReferencesForRoutesRow, len(services))
	for _, row := range rows {
		byRoute[row.RouteID] = append(byRoute[row.RouteID], row)
	}
	return byRoute, nil
}

func (api *RestAPI) loadRoutesByBareID(ctx context.Context, services []gtfsdb.OndemandService) (map[string]gtfsdb.Route, error) {
	routeIDs := make([]string, 0, len(services))
	for _, service := range services {
		routeIDs = append(routeIDs, service.RouteID)
	}
	rows, err := queryInBatches(ctx, utils.SortedUnique(routeIDs), api.GtfsManager.GtfsDB.Queries.GetRoutesByIDs)
	if err != nil {
		return nil, fmt.Errorf("load on-demand routes: %w", err)
	}
	routes := make(map[string]gtfsdb.Route, len(rows))
	for _, row := range rows {
		routes[row.ID] = row
	}
	return routes, nil
}

// onDemandServiceModel applies the wiki §2.1 field rules: name is the route's
// long name, else short name, else the bare route id; empty strings become null.
func onDemandServiceModel(service gtfsdb.OndemandService, route gtfsdb.Route, rules []models.AvailabilityRule) models.OnDemandService {
	name := nulls.StringOrEmpty(route.LongName)
	if name == "" {
		name = nulls.StringOrEmpty(route.ShortName)
	}
	if name == "" {
		name = service.RouteID
	}
	routeID := utils.FormCombinedID(service.AgencyID, service.RouteID)
	return models.OnDemandService{
		ID:          utils.FormCombinedID(service.AgencyID, service.ID),
		AgencyID:    service.AgencyID,
		RouteID:     &routeID,
		Name:        name,
		ServiceKind: service.ServiceKind,
		Description: models.NullableString(nulls.StringOrEmpty(route.Desc)),
		URL:         models.NullableString(nulls.StringOrEmpty(route.Url)),
		Rules:       rules,
	}
}

// onDemandBookingRules loads the referenced booking rules and registers each
// prior_notice_service_id as a calendar to compile.
// Extends ids.gtfsServices, so it must run before onDemandCalendars.
func (api *RestAPI) onDemandBookingRules(ctx context.Context, ids *onDemandReferenceIDs) ([]models.BookingRule, error) {
	rows, err := queryInBatches(ctx, bareIDs(ids.bookingRules), api.GtfsManager.GtfsDB.Queries.GetBookingRulesByIDs)
	if err != nil {
		return nil, fmt.Errorf("load booking rules: %w", err)
	}
	byID := make(map[string]gtfsdb.BookingRule, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	bookingRules := make([]models.BookingRule, 0, len(ids.bookingRules))
	for scoped := range ids.bookingRules {
		row, ok := byID[scoped.ID]
		if !ok {
			continue
		}
		if priorNotice := nulls.StringOrEmpty(row.PriorNoticeServiceID); priorNotice != "" {
			addScoped(ids.gtfsServices, scoped.AgencyID, priorNotice)
		}
		bookingRules = append(bookingRules, bookingRuleReference(row, scoped.AgencyID))
	}
	return bookingRules, nil
}

func bookingRuleReference(rule gtfsdb.BookingRule, agencyID string) models.BookingRule {
	return models.BookingRule{
		ID:                     utils.FormCombinedID(agencyID, rule.ID),
		BookingType:            int(rule.BookingType),
		PriorNoticeDurationMin: nulls.IntOrNil(rule.PriorNoticeDurationMin),
		PriorNoticeDurationMax: nulls.IntOrNil(rule.PriorNoticeDurationMax),
		PriorNoticeLastDay:     nulls.IntOrNil(rule.PriorNoticeLastDay),
		PriorNoticeLastTime:    timeOfDayOrNil(rule.PriorNoticeLastTime),
		PriorNoticeStartDay:    nulls.IntOrNil(rule.PriorNoticeStartDay),
		PriorNoticeStartTime:   timeOfDayOrNil(rule.PriorNoticeStartTime),
		PriorNoticeCalendarId:  combinedIDOrNil(agencyID, rule.PriorNoticeServiceID),
		Message:                models.NullableString(nulls.StringOrEmpty(rule.Message)),
		PickupMessage:          models.NullableString(nulls.StringOrEmpty(rule.PickupMessage)),
		DropOffMessage:         models.NullableString(nulls.StringOrEmpty(rule.DropOffMessage)),
		PhoneNumber:            models.NullableString(nulls.StringOrEmpty(rule.PhoneNumber)),
		InfoUrl:                models.NullableString(nulls.StringOrEmpty(rule.InfoUrl)),
		BookingUrl:             models.NullableString(nulls.StringOrEmpty(rule.BookingUrl)),
	}
}

// onDemandCalendars compiles every referenced GTFS service into calendars and
// returns, per scoped service, the combined calendar ids a rule should carry.
func (api *RestAPI) onDemandCalendars(ctx context.Context, gtfsServices map[agencyScopedID]struct{}) (map[agencyScopedID][]string, []models.OnDemandCalendar, error) {
	serviceIDs := bareIDs(gtfsServices)
	calendarRows, err := queryInBatches(ctx, serviceIDs, api.GtfsManager.GtfsDB.Queries.GetCalendarsByIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load calendars: %w", err)
	}
	baseByService := make(map[string]gtfsdb.Calendar, len(calendarRows))
	for _, row := range calendarRows {
		baseByService[row.ID] = row
	}
	dateRows, err := queryInBatches(ctx, serviceIDs, api.GtfsManager.GtfsDB.Queries.GetCalendarDatesForServiceIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("load calendar dates: %w", err)
	}
	datesByService := make(map[string][]gtfsdb.CalendarDate)
	for _, row := range dateRows {
		datesByService[row.ServiceID] = append(datesByService[row.ServiceID], row)
	}

	calendarIDs := make(map[agencyScopedID][]string, len(gtfsServices))
	calendars := make([]models.OnDemandCalendar, 0, len(gtfsServices))
	for scoped := range gtfsServices {
		var base *gtfsdb.Calendar
		if row, ok := baseByService[scoped.ID]; ok {
			base = &row
		}
		compiled := compileOnDemandCalendars(scoped.AgencyID, scoped.ID, base, datesByService[scoped.ID])
		ids := make([]string, 0, len(compiled))
		for _, calendar := range compiled {
			ids = append(ids, calendar.ID)
		}
		calendarIDs[scoped] = ids
		calendars = append(calendars, compiled...)
	}
	return calendarIDs, calendars, nil
}

// dropDanglingPriorNoticeCalendars nulls any priorNoticeCalendarId that names
// no emitted calendar. The compiler emits no base calendar for a service with
// no usable calendar row (for example one defined only by calendar_dates), and
// every calendar id on the wire must resolve in references.calendars.
// buildFlexIndex logs these once per reload, so this stays silent.
func dropDanglingPriorNoticeCalendars(bookingRules []models.BookingRule, calendars []models.OnDemandCalendar) {
	emitted := make(map[string]struct{}, len(calendars))
	for _, calendar := range calendars {
		emitted[calendar.ID] = struct{}{}
	}
	for i := range bookingRules {
		calendarID := bookingRules[i].PriorNoticeCalendarId
		if calendarID == nil {
			continue
		}
		if _, ok := emitted[*calendarID]; !ok {
			bookingRules[i].PriorNoticeCalendarId = nil
		}
	}
}

// calendarIDsForAgency narrows the scoped calendar map to one agency, keyed by
// bare gtfs service id as buildAvailabilityRules expects.
func calendarIDsForAgency(calendarIDs map[agencyScopedID][]string, agencyID string) map[string][]string {
	byService := make(map[string][]string)
	for scoped, ids := range calendarIDs {
		if scoped.AgencyID == agencyID {
			byService[scoped.ID] = ids
		}
	}
	return byService
}

// fillOnDemandReferences resolves areas, rule-referenced and group-member
// stops with the routes serving them, the services' own routes, and every
// agency involved.
func (api *RestAPI) fillOnDemandReferences(ctx context.Context, references *models.OnDemandReferences, services []gtfsdb.OndemandService, routes map[string]gtfsdb.Route, ids *onDemandReferenceIDs, opts onDemandBuildOptions) error {
	var err error
	if references.ServiceAreas, err = api.onDemandServiceAreas(ctx, ids.locations, opts); err != nil {
		return err
	}

	agencyIDs := make(map[string]struct{})
	for _, service := range services {
		agencyIDs[service.AgencyID] = struct{}{}
		route, ok := routes[service.RouteID]
		if !ok {
			continue
		}
		routeModels, err := buildRouteModels(ctx, service.AgencyID, []gtfsdb.Route{route})
		if err != nil {
			return err
		}
		references.Routes = append(references.Routes, routeModels...)
	}

	if err := api.appendOnDemandStopReferences(ctx, references, ids.stops, agencyIDs); err != nil {
		return err
	}

	agencies, err := api.GtfsManager.GtfsDB.Queries.GetAgenciesByIDs(ctx, utils.SortedUnique(slices.Collect(maps.Keys(agencyIDs))))
	if err != nil {
		return fmt.Errorf("load on-demand agencies: %w", err)
	}
	references.Agencies = buildAgencyReferences(agencies)
	return nil
}

func (api *RestAPI) onDemandServiceAreas(ctx context.Context, locations map[agencyScopedID]struct{}, opts onDemandBuildOptions) ([]models.ServiceArea, error) {
	rows, err := queryInBatches(ctx, bareIDs(locations), api.GtfsManager.GtfsDB.Queries.GetLocationsByIDs)
	if err != nil {
		return nil, fmt.Errorf("load locations: %w", err)
	}
	byID := make(map[string]gtfsdb.Location, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	areas := make([]models.ServiceArea, 0, len(locations))
	for scoped := range locations {
		location, ok := byID[scoped.ID]
		if !ok {
			continue
		}
		areas = append(areas, api.serviceAreaReference(ctx, location, scoped.AgencyID, opts))
	}
	return areas, nil
}

func (api *RestAPI) serviceAreaReference(ctx context.Context, location gtfsdb.Location, agencyID string, opts onDemandBuildOptions) models.ServiceArea {
	area := models.ServiceArea{
		ID:          utils.FormCombinedID(agencyID, location.ID),
		Name:        models.NullableString(nulls.StringOrEmpty(location.Name)),
		Description: models.NullableString(nulls.StringOrEmpty(location.Description)),
		BBox:        [4]float64{location.MinLon, location.MinLat, location.MaxLon, location.MaxLat},
	}
	switch opts.GeometryDetail {
	case GeometryDetailFull:
		area.Geometry = validStoredGeometry(ctx, location.ID, location.Geometry)
	case GeometryDetailSimplified:
		// NULL means the feed geometry already met the display target.
		area.Geometry = validStoredGeometry(ctx, location.ID, nulls.StringOrDefault(location.GeometrySimplified, location.Geometry))
	}
	if opts.QueryPoint != nil {
		api.applyAreaDistance(&area, location.ID, *opts.QueryPoint)
	}
	return area
}

// validStoredGeometry embeds stored GeoJSON verbatim, or returns nil when the
// row is not valid JSON: a raw message that fails to marshal would fail the
// whole response, not just this area.
func validStoredGeometry(ctx context.Context, locationID, geometry string) json.RawMessage {
	if !json.Valid([]byte(geometry)) {
		logging.ForComponent(ctx, "ondemand_references").Warn("omitting invalid stored geometry", "location_id", locationID)
		return nil
	}
	return json.RawMessage(geometry)
}

// applyAreaDistance fills distanceToArea (0 inside) and, outside, the nearest
// boundary point, always against the full geometry.
func (api *RestAPI) applyAreaDistance(area *models.ServiceArea, locationID string, point geoPoint) {
	flexArea := api.GtfsManager.FlexIndex().FlexArea(locationID)
	if flexArea == nil {
		return
	}
	if utils.PointInPolygon(point.Lat, point.Lon, flexArea.Polygons) {
		zero := 0.0
		area.DistanceToArea = &zero
		return
	}
	distance, lon, lat := utils.NearestPointOnBoundary(point.Lat, point.Lon, flexArea.Polygons)
	area.DistanceToArea = &distance
	area.NearestPointOnBoundary = &[2]float64{lon, lat}
}

// onDemandLocationGroup is a referenced group with its member stop ids.
type onDemandLocationGroup struct {
	scoped  agencyScopedID
	group   gtfsdb.LocationGroup
	members []string
}

// loadOnDemandLocationGroups resolves groups and adds their members to
// ids.stops so the standard stop references cover them.
// Extends ids.stops, so it must run before loadStopAgencyIDs.
func (api *RestAPI) loadOnDemandLocationGroups(ctx context.Context, ids *onDemandReferenceIDs) ([]onDemandLocationGroup, error) {
	groupIDs := bareIDs(ids.groups)
	rows, err := queryInBatches(ctx, groupIDs, api.GtfsManager.GtfsDB.Queries.GetLocationGroupsByIDs)
	if err != nil {
		return nil, fmt.Errorf("load location groups: %w", err)
	}
	memberRows, err := queryInBatches(ctx, groupIDs, api.GtfsManager.GtfsDB.Queries.GetLocationGroupStopsForGroups)
	if err != nil {
		return nil, fmt.Errorf("load location group stops: %w", err)
	}
	membersByGroup := make(map[string][]string)
	for _, member := range memberRows {
		membersByGroup[member.LocationGroupID] = append(membersByGroup[member.LocationGroupID], member.StopID)
	}
	byID := make(map[string]gtfsdb.LocationGroup, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	groups := make([]onDemandLocationGroup, 0, len(ids.groups))
	for scoped := range ids.groups {
		group, ok := byID[scoped.ID]
		if !ok {
			continue
		}
		members := membersByGroup[group.ID]
		for _, stopID := range members {
			addScoped(ids.stops, scoped.AgencyID, stopID)
		}
		groups = append(groups, onDemandLocationGroup{scoped: scoped, group: group, members: members})
	}
	return groups, nil
}

// locationGroupReferences builds the group references; member stop ids carry
// each stop's own /where agency, the group id its service's agency.
func locationGroupReferences(groups []onDemandLocationGroup, stopAgencies stopAgencyIDs) []models.LocationGroupReference {
	references := make([]models.LocationGroupReference, 0, len(groups))
	for _, group := range groups {
		stopIDs := make([]string, 0, len(group.members))
		for _, stopID := range group.members {
			stopIDs = append(stopIDs, stopAgencies.combinedStopID(group.scoped.AgencyID, stopID))
		}
		references = append(references, models.LocationGroupReference{
			ID:      group.scoped.combined(),
			Name:    models.NullableString(nulls.StringOrEmpty(group.group.Name)),
			StopIds: utils.SortedUnique(stopIDs),
		})
	}
	return references
}

// stopAgencyIDs maps a bare stop id to the agency its /where id carries.
type stopAgencyIDs map[string]string

// loadStopAgencyIDs resolves the /where agency of every referenced stop with
// the MIN rule stop search uses, so /ondemand and /where agree on stop ids.
func (api *RestAPI) loadStopAgencyIDs(ctx context.Context, stops map[agencyScopedID]struct{}) (stopAgencyIDs, error) {
	rows, err := queryInBatches(ctx, bareIDs(stops), api.GtfsManager.GtfsDB.Queries.GetWhereAgencyIDsForStops)
	if err != nil {
		return nil, fmt.Errorf("load stop agencies: %w", err)
	}
	agencies := make(stopAgencyIDs, len(rows))
	for _, row := range rows {
		agencies[row.StopID] = row.AgencyID
	}
	return agencies, nil
}

// agencyFor returns the stop's /where agency, falling back to the referencing
// service's agency for a stop with no stop_agencies row at all.
func (agencies stopAgencyIDs) agencyFor(serviceAgencyID, stopID string) string {
	if agencyID, ok := agencies[stopID]; ok {
		return agencyID
	}
	return serviceAgencyID
}

func (agencies stopAgencyIDs) combinedStopID(serviceAgencyID, stopID string) string {
	return utils.FormCombinedID(agencies.agencyFor(serviceAgencyID, stopID), stopID)
}

// rescope re-keys a stop set collected under service agencies by each stop's
// own /where agency.
func (agencies stopAgencyIDs) rescope(stops map[agencyScopedID]struct{}) map[agencyScopedID]struct{} {
	rescoped := make(map[agencyScopedID]struct{}, len(stops))
	for scoped := range stops {
		addScoped(rescoped, agencies.agencyFor(scoped.AgencyID, scoped.ID), scoped.ID)
	}
	return rescoped
}

// appendOnDemandStopReferences builds standard stop references per agency and
// pulls in the routes (and their agencies) serving those stops so every routeId
// on a stop resolves within the block.
// Extends agencyIDs, so the agency lookup must run after it.
func (api *RestAPI) appendOnDemandStopReferences(ctx context.Context, references *models.OnDemandReferences, stops map[agencyScopedID]struct{}, agencyIDs map[string]struct{}) error {
	stopIDsByAgency := make(map[string][]string)
	for scoped := range stops {
		stopIDsByAgency[scoped.AgencyID] = append(stopIDsByAgency[scoped.AgencyID], scoped.ID)
	}
	seenRoutes := make(map[string]struct{}, len(references.Routes))
	for _, route := range references.Routes {
		seenRoutes[route.ID] = struct{}{}
	}

	for _, agencyID := range utils.SortedUnique(slices.Collect(maps.Keys(stopIDsByAgency))) {
		stopModels, routeRows, err := BuildStopReferencesAndRouteIDsForStops(api, ctx, agencyID, utils.SortedUnique(stopIDsByAgency[agencyID]))
		if err != nil {
			return err
		}
		references.Stops = append(references.Stops, stopModels...)
		for combinedRouteID, row := range routeRows {
			if _, ok := seenRoutes[combinedRouteID]; ok {
				continue
			}
			seenRoutes[combinedRouteID] = struct{}{}
			references.Routes = append(references.Routes, routeReferenceFromStopRow(row))
			agencyIDs[row.AgencyID] = struct{}{}
		}
	}
	return nil
}

func sortOnDemandReferences(references *models.OnDemandReferences) {
	utils.SortByKey(references.Agencies, func(a models.AgencyReference) string { return a.ID })
	utils.SortByKey(references.Routes, func(r models.Route) string { return r.ID })
	utils.SortByKey(references.Stops, func(s models.Stop) string { return s.ID })
	utils.SortByKey(references.ServiceAreas, func(a models.ServiceArea) string { return a.ID })
	utils.SortByKey(references.LocationGroups, func(g models.LocationGroupReference) string { return g.ID })
	utils.SortByKey(references.BookingRules, func(b models.BookingRule) string { return b.ID })
	utils.SortByKey(references.Calendars, func(c models.OnDemandCalendar) string { return c.ID })
}
