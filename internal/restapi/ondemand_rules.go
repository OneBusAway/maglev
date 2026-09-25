package restapi

import (
	"cmp"
	"database/sql"
	"slices"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// ruleGroupKey is an ondemand_rules row minus its calendar: rows that share it
// merge their calendarIds into one availabilityRule (wiki §2.3 step 4).
type ruleGroupKey struct {
	fromID, toID                  string
	start, end, endDropOff        sql.NullInt64
	pickupType, dropOffType       int64
	pickupBooking, dropOffBooking sql.NullString
	safeFactor, safeOffset        sql.NullFloat64
}

func ruleGroupKeyOf(row gtfsdb.OndemandRule) ruleGroupKey {
	return ruleGroupKey{
		fromID: row.FromID, toID: row.ToID,
		start: row.StartPickupTime, end: row.EndPickupTime, endDropOff: row.EndDropOffTime,
		pickupType: row.PickupType, dropOffType: row.DropOffType,
		pickupBooking: row.PickupBookingRuleID, dropOffBooking: row.DropOffBookingRuleID,
		safeFactor: row.SafeDurationFactor, safeOffset: row.SafeDurationOffset,
	}
}

// serviceIDScope prefixes the ids one service's rules emit: a stop takes its
// own /where agency, every other id the service's agency.
type serviceIDScope struct {
	agencyID     string
	stopAgencies stopAgencyIDs
}

func (scope serviceIDScope) endpointID(id string, kind int64) string {
	if gtfsdb.EndpointKind(kind) == gtfsdb.EndpointStop {
		return scope.stopAgencies.combinedStopID(scope.agencyID, id)
	}
	return utils.FormCombinedID(scope.agencyID, id)
}

// buildAvailabilityRules groups a service's rows by tuple-minus-calendar,
// prefixes every id through scope, resolves calendarIds through
// calendarIDsByService (bare gtfs service id → combined calendar ids) and
// returns the rules in the wiki §3.4 total order. Never nil.
func buildAvailabilityRules(rows []gtfsdb.OndemandRule, scope serviceIDScope, calendarIDsByService map[string][]string) []models.AvailabilityRule {
	groups := make(map[ruleGroupKey]*models.AvailabilityRule)
	var order []ruleGroupKey
	for _, row := range rows {
		key := ruleGroupKeyOf(row)
		rule, ok := groups[key]
		if !ok {
			rule = availabilityRuleFromRow(row, scope)
			groups[key] = rule
			order = append(order, key)
		}
		rule.CalendarIds = append(rule.CalendarIds, calendarIDsByService[row.GtfsServiceID]...)
	}

	rules := make([]models.AvailabilityRule, 0, len(order))
	for _, key := range order {
		rule := groups[key]
		rule.CalendarIds = utils.SortedUnique(rule.CalendarIds)
		rules = append(rules, *rule)
	}
	sortAvailabilityRules(rules)
	return rules
}

func availabilityRuleFromRow(row gtfsdb.OndemandRule, scope serviceIDScope) *models.AvailabilityRule {
	agencyID := scope.agencyID
	return &models.AvailabilityRule{
		FromIds:              []string{scope.endpointID(row.FromID, row.FromKind)},
		ToIds:                []string{scope.endpointID(row.ToID, row.ToKind)},
		StartPickupTime:      timeOfDayOrNil(row.StartPickupTime),
		EndPickupTime:        timeOfDayOrNil(row.EndPickupTime),
		EndDropOffTime:       timeOfDayOrNil(row.EndDropOffTime),
		CalendarIds:          []string{},
		PickupType:           int(row.PickupType),
		DropOffType:          int(row.DropOffType),
		PickupBookingRuleId:  combinedIDOrNil(agencyID, row.PickupBookingRuleID),
		DropOffBookingRuleId: combinedIDOrNil(agencyID, row.DropOffBookingRuleID),
		SafeDurationFactor:   nulls.Float64OrNil(row.SafeDurationFactor),
		SafeDurationOffset:   nulls.Float64OrNil(row.SafeDurationOffset),
	}
}

// sortAvailabilityRules sorts fromIds, toIds and calendarIds within each rule,
// then orders rules by startPickupTime, endPickupTime, calendarIds[0], fromIds,
// toIds, endDropOffTime, pickupType, dropOffType, pickupBookingRuleId and
// dropOffBookingRuleId, nulls first throughout (wiki §3.4, spec §4).
func sortAvailabilityRules(rules []models.AvailabilityRule) {
	for i := range rules {
		slices.Sort(rules[i].FromIds)
		slices.Sort(rules[i].ToIds)
		slices.Sort(rules[i].CalendarIds)
	}
	slices.SortStableFunc(rules, compareAvailabilityRules)
}

func compareAvailabilityRules(a, b models.AvailabilityRule) int {
	return cmp.Or(
		compareNullableString(a.StartPickupTime, b.StartPickupTime),
		compareNullableString(a.EndPickupTime, b.EndPickupTime),
		cmp.Compare(firstOrEmpty(a.CalendarIds), firstOrEmpty(b.CalendarIds)),
		cmp.Compare(strings.Join(a.FromIds, "\x00"), strings.Join(b.FromIds, "\x00")),
		cmp.Compare(strings.Join(a.ToIds, "\x00"), strings.Join(b.ToIds, "\x00")),
		compareNullableString(a.EndDropOffTime, b.EndDropOffTime),
		cmp.Compare(a.PickupType, b.PickupType),
		cmp.Compare(a.DropOffType, b.DropOffType),
		compareNullableString(a.PickupBookingRuleId, b.PickupBookingRuleId),
		compareNullableString(a.DropOffBookingRuleId, b.DropOffBookingRuleId),
	)
}

// compareNullableString orders nil before any value. "HH:MM:SS" strings with
// two-digit hours compare the same as their numeric value.
func compareNullableString(a, b *string) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return cmp.Compare(*a, *b)
	}
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func timeOfDayOrNil(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	formatted := models.FormatGTFSTimeOfDay(value.Int64)
	return &formatted
}

func combinedIDOrNil(agencyID string, value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	combined := utils.FormCombinedID(agencyID, value.String)
	return &combined
}
