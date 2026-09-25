package gtfsdb

import (
	"cmp"
	"slices"

	"github.com/OneBusAway/go-gtfs"
)

// Service kinds classified from a route's flex records (wiki §2.3).
const (
	ServiceKindZone          = "zone"
	ServiceKindZoneToZone    = "zoneToZone"
	ServiceKindStopGroup     = "stopGroup"
	ServiceKindDeviatedRoute = "deviatedRoute"
	ServiceKindUnknown       = "unknown"
)

// EndpointKind tags a rule endpoint within the shared stop/location/group id namespace.
type EndpointKind int64

// Endpoint kinds, stored in ondemand_rules.from_kind and to_kind.
const (
	EndpointStop EndpointKind = iota
	EndpointLocation
	EndpointGroup
)

// CompiledService is one on-demand service: one per route with at least one
// flex record, even when compilation yields no rules.
type CompiledService struct {
	ID       string // the bare route id
	AgencyID string
	RouteID  string
	Kind     string
}

// CompiledRule is one availability rule row: one per distinct
// (from, to, windows, types, booking rules, safe duration, gtfs service id).
type CompiledRule struct {
	ServiceID            string
	TripID               string // representative trip, for traceability only
	FromID               string
	FromKind             EndpointKind
	ToID                 string
	ToKind               EndpointKind
	StartPickupTime      *int64 // ns since midnight
	EndPickupTime        *int64
	EndDropOffTime       *int64 // nil when equal to EndPickupTime
	GTFSServiceID        string
	PickupType           int64
	DropOffType          int64
	PickupBookingRuleID  *string
	DropOffBookingRuleID *string
	SafeDurationFactor   *float64
	SafeDurationOffset   *float64
}

// CompiledOnDemand is the output of CompileOnDemand.
type CompiledOnDemand struct {
	Services []CompiledService // sorted by ID
	Rules    []CompiledRule    // deterministic order, see compareRules
	// StopServices maps a bare stop id to the sorted bare service ids whose rules
	// reference it directly or through a location group.
	StopServices map[string][]string
}

// CompileOnDemand implements wiki §2.3 over parsed static data: for every
// flex-involved trip, records in stop_sequence order, every pickup-capable
// record paired with every later drop-off-capable record (except timed→timed),
// pickup-side fields from the pickup record and drop-off-side fields from the
// drop-off record, deduplicated by tuple and calendar, then serviceKind
// classified from the records rather than the rules.
func CompileOnDemand(static *gtfs.Static) CompiledOnDemand {
	compiler := newOnDemandCompiler(soleAgencyID(static.Agencies))
	for i := range static.Trips {
		compiler.addTrip(&static.Trips[i])
	}
	return compiler.result()
}

// onDemandCompiler accumulates services, unique rules and stop pointers
// across trips.
type onDemandCompiler struct {
	singleAgencyID string
	routeRecords   map[string]*routeFlexRecords
	seenRules      map[ruleKey]struct{}
	rules          []CompiledRule
	stopServices   map[string]map[string]struct{}
}

func newOnDemandCompiler(singleAgencyID string) *onDemandCompiler {
	return &onDemandCompiler{
		singleAgencyID: singleAgencyID,
		routeRecords:   make(map[string]*routeFlexRecords),
		seenRules:      make(map[ruleKey]struct{}),
		stopServices:   make(map[string]map[string]struct{}),
	}
}

func (c *onDemandCompiler) addTrip(trip *gtfs.ScheduledTrip) {
	if !tripHasFlexRecord(trip) {
		return
	}
	ordered := recordsBySequence(trip)
	c.recordsForRoute(trip.Route).add(ordered)
	for _, candidate := range compileTripRules(trip, ordered) {
		c.addRule(candidate)
	}
}

func (c *onDemandCompiler) recordsForRoute(route *gtfs.Route) *routeFlexRecords {
	records, ok := c.routeRecords[route.Id]
	if !ok {
		records = &routeFlexRecords{
			agencyID:    pickFirstAvailable(route.Agency.Id, c.singleAgencyID),
			locationIDs: make(map[string]struct{}),
		}
		c.routeRecords[route.Id] = records
	}
	return records
}

// addRule keeps the first rule of each distinct tuple, so its trip becomes
// the representative, and points the rule's stops at its service.
func (c *onDemandCompiler) addRule(candidate tripRule) {
	key := ruleKeyOf(candidate.rule)
	if _, seen := c.seenRules[key]; seen {
		return
	}
	c.seenRules[key] = struct{}{}
	c.rules = append(c.rules, candidate.rule)
	for _, stopID := range candidate.stopIDs {
		c.addStopService(stopID, candidate.rule.ServiceID)
	}
}

func (c *onDemandCompiler) addStopService(stopID, serviceID string) {
	if c.stopServices[stopID] == nil {
		c.stopServices[stopID] = make(map[string]struct{})
	}
	c.stopServices[stopID][serviceID] = struct{}{}
}

func (c *onDemandCompiler) result() CompiledOnDemand {
	return CompiledOnDemand{
		Services:     c.sortedServices(),
		Rules:        c.sortedRules(),
		StopServices: c.sortedStopServices(),
	}
}

func (c *onDemandCompiler) sortedServices() []CompiledService {
	services := make([]CompiledService, 0, len(c.routeRecords))
	for routeID, records := range c.routeRecords {
		services = append(services, CompiledService{
			ID:       routeID,
			AgencyID: records.agencyID,
			RouteID:  routeID,
			Kind:     records.kind(),
		})
	}
	slices.SortFunc(services, func(a, b CompiledService) int { return cmp.Compare(a.ID, b.ID) })
	return services
}

func (c *onDemandCompiler) sortedRules() []CompiledRule {
	rules := slices.Clone(c.rules)
	if rules == nil {
		return []CompiledRule{}
	}
	slices.SortFunc(rules, compareRules)
	return rules
}

func (c *onDemandCompiler) sortedStopServices() map[string][]string {
	result := make(map[string][]string, len(c.stopServices))
	for stopID, services := range c.stopServices {
		serviceIDs := make([]string, 0, len(services))
		for serviceID := range services {
			serviceIDs = append(serviceIDs, serviceID)
		}
		slices.Sort(serviceIDs)
		result[stopID] = serviceIDs
	}
	return result
}

// routeFlexRecords accumulates a route's flex-trip records for classification.
type routeFlexRecords struct {
	agencyID          string
	hasGroup          bool
	locationIDs       map[string]struct{}
	mixesTimedAndFlex bool // some trip has both timed-stop and location/group records
}

func (r *routeFlexRecords) add(records []gtfs.ScheduledStopTime) {
	hasTimed, hasZoneOrGroup := false, false
	for _, st := range records {
		switch {
		case st.LocationGroup != nil:
			r.hasGroup = true
			hasZoneOrGroup = true
		case st.Location != nil:
			r.locationIDs[st.Location.Id] = struct{}{}
			hasZoneOrGroup = true
		case !st.IsWindowed():
			hasTimed = true
		}
	}
	if hasTimed && hasZoneOrGroup {
		r.mixesTimedAndFlex = true
	}
}

// kind applies the wiki §2.3 classification, first match wins.
func (r *routeFlexRecords) kind() string {
	switch {
	case r.mixesTimedAndFlex:
		return ServiceKindDeviatedRoute
	case r.hasGroup:
		return ServiceKindStopGroup
	case len(r.locationIDs) >= 2:
		return ServiceKindZoneToZone
	case len(r.locationIDs) == 1:
		return ServiceKindZone
	default:
		return ServiceKindUnknown
	}
}

func tripHasFlexRecord(trip *gtfs.ScheduledTrip) bool {
	return slices.ContainsFunc(trip.StopTimes, gtfs.ScheduledStopTime.IsFlex)
}

func recordsBySequence(trip *gtfs.ScheduledTrip) []gtfs.ScheduledStopTime {
	ordered := slices.Clone(trip.StopTimes)
	slices.SortStableFunc(ordered, func(a, b gtfs.ScheduledStopTime) int {
		return cmp.Compare(a.StopSequence, b.StopSequence)
	})
	return ordered
}

// tripRule pairs a compiled rule with the stops it references (directly or as
// group members), which feed the stop → service pointer set.
type tripRule struct {
	rule    CompiledRule
	stopIDs []string
}

// compileTripRules pairs every pickup-capable record with every later
// drop-off-capable record, the GTFS-Flex reachability model.
func compileTripRules(trip *gtfs.ScheduledTrip, ordered []gtfs.ScheduledStopTime) []tripRule {
	var rules []tripRule
	for i, pickupRecord := range ordered {
		if !pickupCapable(pickupRecord) {
			continue
		}
		for _, dropOffRecord := range ordered[i+1:] {
			if !isOnDemandPair(pickupRecord, dropOffRecord) {
				continue
			}
			rules = append(rules, tripRule{
				rule:    compileRule(trip, pickupRecord, dropOffRecord),
				stopIDs: append(endpointStopIDs(pickupRecord), endpointStopIDs(dropOffRecord)...),
			})
		}
	}
	return rules
}

// isOnDemandPair reports whether a later record can end a trip begun at the
// pickup record. Timed→timed pairs are ordinary fixed-route travel, not
// on-demand rules.
func isOnDemandPair(pickupRecord, dropOffRecord gtfs.ScheduledStopTime) bool {
	bothTimed := !pickupRecord.IsWindowed() && !dropOffRecord.IsWindowed()
	return dropOffCapable(dropOffRecord) && !bothTimed
}

// pickupCapable: windowed records need pickup_type 2 (1 = no pickup; 0 and 3
// are forbidden with windows); timed records permit boarding unless type 1.
func pickupCapable(st gtfs.ScheduledStopTime) bool {
	if st.IsWindowed() {
		return st.PickupType == gtfs.PickupDropOffPolicy_PhoneAgency
	}
	return st.PickupType != gtfs.PickupDropOffPolicy_No
}

// dropOffCapable: windowed records need drop_off_type 2 or 3; timed records
// permit alighting unless type 1.
func dropOffCapable(st gtfs.ScheduledStopTime) bool {
	if st.IsWindowed() {
		return st.DropOffType == gtfs.PickupDropOffPolicy_PhoneAgency ||
			st.DropOffType == gtfs.PickupDropOffPolicy_CoordinateWithDriver
	}
	return st.DropOffType != gtfs.PickupDropOffPolicy_No
}

// compileRule builds one rule, taking pickup-side fields from the pickup
// record and drop-off-side fields from the drop-off record.
func compileRule(trip *gtfs.ScheduledTrip, pickupRecord, dropOffRecord gtfs.ScheduledStopTime) CompiledRule {
	fromID, fromKind := endpoint(pickupRecord)
	toID, toKind := endpoint(dropOffRecord)
	startPickup, endPickup := pickupWindow(pickupRecord)

	return CompiledRule{
		ServiceID:            trip.Route.Id,
		TripID:               trip.ID,
		FromID:               fromID,
		FromKind:             fromKind,
		ToID:                 toID,
		ToKind:               toKind,
		StartPickupTime:      startPickup,
		EndPickupTime:        endPickup,
		EndDropOffTime:       dropOffEndUnlessEqual(dropOffRecord, *endPickup),
		GTFSServiceID:        trip.Service.Id,
		PickupType:           int64(pickupRecord.PickupType),
		DropOffType:          int64(dropOffRecord.DropOffType),
		PickupBookingRuleID:  bookingRuleID(pickupRecord.PickupBookingRule),
		DropOffBookingRuleID: bookingRuleID(dropOffRecord.DropOffBookingRule),
		SafeDurationFactor:   firstPresent(trip.SafeDurationFactor, pickupRecord.SafeDurationFactor, dropOffRecord.SafeDurationFactor),
		SafeDurationOffset:   firstPresent(trip.SafeDurationOffset, pickupRecord.SafeDurationOffset, dropOffRecord.SafeDurationOffset),
	}
}

func endpoint(st gtfs.ScheduledStopTime) (string, EndpointKind) {
	switch {
	case st.LocationGroup != nil:
		return st.LocationGroup.Id, EndpointGroup
	case st.Location != nil:
		return st.Location.Id, EndpointLocation
	default:
		return st.Stop.Id, EndpointStop
	}
}

// endpointStopIDs returns the stops a record references: the stop itself, or
// every member of its location group. Zones reference no stops.
func endpointStopIDs(st gtfs.ScheduledStopTime) []string {
	switch {
	case st.LocationGroup != nil:
		ids := make([]string, 0, len(st.LocationGroup.Stops))
		for _, stop := range st.LocationGroup.Stops {
			ids = append(ids, stop.Id)
		}
		return ids
	case st.Stop != nil:
		return []string{st.Stop.Id}
	default:
		return nil
	}
}

// pickupWindow is the record's window, or a point window at departure time for
// a timed fixed stop.
func pickupWindow(st gtfs.ScheduledStopTime) (start, end *int64) {
	if st.IsWindowed() {
		return int64Of(*st.StartPickupDropOffWindow), int64Of(*st.EndPickupDropOffWindow)
	}
	return int64Of(st.DepartureTime), int64Of(st.DepartureTime)
}

// dropOffEndUnlessEqual is the window end, or the arrival time for a timed
// fixed stop; nil when it adds nothing beyond the pickup window's end.
func dropOffEndUnlessEqual(st gtfs.ScheduledStopTime, endPickup int64) *int64 {
	end := int64(st.ArrivalTime)
	if st.IsWindowed() {
		end = int64(*st.EndPickupDropOffWindow)
	}
	if end == endPickup {
		return nil
	}
	return &end
}

func int64Of[T ~int64](value T) *int64 {
	v := int64(value)
	return &v
}

func bookingRuleID(rule *gtfs.BookingRule) *string {
	if rule == nil {
		return nil
	}
	id := rule.Id
	return &id
}

// firstPresent copies the first non-nil candidate, implementing the safe
// duration precedence: trip, then pickup record, then drop-off record.
func firstPresent[T any](candidates ...*T) *T {
	for _, candidate := range candidates {
		if candidate != nil {
			value := *candidate
			return &value
		}
	}
	return nil
}

// optional flattens a pointer into a comparable value for map keys.
type optional[T cmp.Ordered] struct {
	value T
	valid bool
}

func optionalOf[T cmp.Ordered](p *T) optional[T] {
	if p == nil {
		return optional[T]{}
	}
	return optional[T]{value: *p, valid: true}
}

// compareOptional orders absent values first.
func compareOptional[T cmp.Ordered](a, b optional[T]) int {
	if a.valid != b.valid {
		if !a.valid {
			return -1
		}
		return 1
	}
	return cmp.Compare(a.value, b.value)
}

// ruleKey is CompiledRule minus the representative trip, with pointers
// flattened so it can be a map key.
type ruleKey struct {
	serviceID, gtfsServiceID string
	fromID                   string
	fromKind                 EndpointKind
	toID                     string
	toKind                   EndpointKind
	start, end, endDropOff   optional[int64]
	pickupType, dropOffType  int64
	pickupBooking            optional[string]
	dropOffBooking           optional[string]
	safeFactor, safeOffset   optional[float64]
}

func ruleKeyOf(rule CompiledRule) ruleKey {
	return ruleKey{
		serviceID:      rule.ServiceID,
		gtfsServiceID:  rule.GTFSServiceID,
		fromID:         rule.FromID,
		fromKind:       rule.FromKind,
		toID:           rule.ToID,
		toKind:         rule.ToKind,
		start:          optionalOf(rule.StartPickupTime),
		end:            optionalOf(rule.EndPickupTime),
		endDropOff:     optionalOf(rule.EndDropOffTime),
		pickupType:     rule.PickupType,
		dropOffType:    rule.DropOffType,
		pickupBooking:  optionalOf(rule.PickupBookingRuleID),
		dropOffBooking: optionalOf(rule.DropOffBookingRuleID),
		safeFactor:     optionalOf(rule.SafeDurationFactor),
		safeOffset:     optionalOf(rule.SafeDurationOffset),
	}
}

// compareRules orders rules by service, calendar, endpoints, windows, types,
// booking rules and safe duration. Rules are unique by that key, so this is a
// total order independent of trip order.
func compareRules(a, b CompiledRule) int {
	ka, kb := ruleKeyOf(a), ruleKeyOf(b)
	return cmp.Or(
		cmp.Compare(ka.serviceID, kb.serviceID),
		cmp.Compare(ka.gtfsServiceID, kb.gtfsServiceID),
		cmp.Compare(ka.fromKind, kb.fromKind),
		cmp.Compare(ka.fromID, kb.fromID),
		cmp.Compare(ka.toKind, kb.toKind),
		cmp.Compare(ka.toID, kb.toID),
		compareOptional(ka.start, kb.start),
		compareOptional(ka.end, kb.end),
		compareOptional(ka.endDropOff, kb.endDropOff),
		cmp.Compare(ka.pickupType, kb.pickupType),
		cmp.Compare(ka.dropOffType, kb.dropOffType),
		compareOptional(ka.pickupBooking, kb.pickupBooking),
		compareOptional(ka.dropOffBooking, kb.dropOffBooking),
		compareOptional(ka.safeFactor, kb.safeFactor),
		compareOptional(ka.safeOffset, kb.safeOffset),
	)
}
