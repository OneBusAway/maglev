package restapi

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

func nsOf(d time.Duration) sql.NullInt64 { return nulls.Int64(int64(d)) }

func ruleRow(service, from, to string, start, end time.Duration, booking string) gtfsdb.OndemandRule {
	return gtfsdb.OndemandRule{
		ServiceID: "CC1", TripID: "t", FromID: from, FromKind: 1, ToID: to, ToKind: 1,
		StartPickupTime: nsOf(start), EndPickupTime: nsOf(end),
		GtfsServiceID: service, PickupType: 2, DropOffType: 2,
		PickupBookingRuleID: nulls.String(booking), DropOffBookingRuleID: nulls.String(booking),
		SafeDurationFactor: sql.NullFloat64{Float64: 2, Valid: true},
	}
}

func TestBuildAvailabilityRules_MergesCalendarsAcrossRows(t *testing.T) {
	calendars := map[string][]string{
		"sat":                    {"CC_sat"},
		"mon-tues-wed-thurs-fri": {"CC_mon-tues-wed-thurs-fri", "CC_mon-tues-wed-thurs-fri_added_20260704"},
	}
	rows := []gtfsdb.OndemandRule{
		ruleRow("sat", "charlevoix_county", "charlevoix_county", 7*time.Hour+20*time.Minute, 16*time.Hour+40*time.Minute, "booking_rule_CC1"),
		ruleRow("mon-tues-wed-thurs-fri", "charlevoix_county", "charlevoix_county", 7*time.Hour+20*time.Minute, 16*time.Hour+40*time.Minute, "booking_rule_CC1"),
	}

	rules := buildAvailabilityRules(rows, "CC", calendars)
	require.Len(t, rules, 1, "rows identical except for calendar merge")

	rule := rules[0]
	assert.Equal(t, []string{"CC_charlevoix_county"}, rule.FromIds)
	assert.Equal(t, []string{"CC_charlevoix_county"}, rule.ToIds)
	assert.Equal(t, "07:20:00", *rule.StartPickupTime)
	assert.Equal(t, "16:40:00", *rule.EndPickupTime)
	assert.Nil(t, rule.EndDropOffTime)
	assert.Equal(t, []string{"CC_mon-tues-wed-thurs-fri", "CC_mon-tues-wed-thurs-fri_added_20260704", "CC_sat"}, rule.CalendarIds)
	assert.Equal(t, 2, rule.PickupType)
	assert.Equal(t, "CC_booking_rule_CC1", *rule.PickupBookingRuleId)
	assert.Equal(t, 2.0, *rule.SafeDurationFactor)
	assert.Nil(t, rule.SafeDurationOffset)
}

func TestBuildAvailabilityRules_TotalOrder(t *testing.T) {
	calendars := map[string][]string{"a": {"X_a"}, "b": {"X_b"}}
	rows := []gtfsdb.OndemandRule{
		ruleRow("b", "z1", "z2", 10*time.Hour, 17*time.Hour, "br"),
		ruleRow("a", "z1", "z2", 10*time.Hour, 17*time.Hour, "br"),
		ruleRow("a", "z1", "z1", 5*time.Hour, 18*time.Hour, "br"),
		ruleRow("a", "z1", "z3", 5*time.Hour, 18*time.Hour, "br"),
		ruleRow("a", "z1", "z1", 5*time.Hour, 12*time.Hour, "br"),
	}
	rows = append(rows, gtfsdb.OndemandRule{ServiceID: "CC1", FromID: "z9", ToID: "z9", FromKind: 1, ToKind: 1, GtfsServiceID: "a", PickupType: 2, DropOffType: 2})

	rules := buildAvailabilityRules(rows, "X", calendars)
	require.Len(t, rules, 5)
	assert.Nil(t, rules[0].StartPickupTime, "null windows sort first")
	assert.Equal(t, "05:00:00", *rules[1].StartPickupTime)
	assert.Equal(t, "12:00:00", *rules[1].EndPickupTime, "then endPickupTime")
	assert.Equal(t, "18:00:00", *rules[2].EndPickupTime)
	assert.Equal(t, []string{"X_z1"}, rules[2].ToIds, "then toIds")
	assert.Equal(t, []string{"X_z3"}, rules[3].ToIds)
	assert.Equal(t, "10:00:00", *rules[4].StartPickupTime)
	assert.Equal(t, []string{"X_a", "X_b"}, rules[4].CalendarIds)
}

func TestBuildAvailabilityRules_UnknownCalendarYieldsEmptyIDs(t *testing.T) {
	rules := buildAvailabilityRules([]gtfsdb.OndemandRule{ruleRow("ghost", "z1", "z1", time.Hour, 2*time.Hour, "br")}, "X", map[string][]string{})
	require.Len(t, rules, 1)
	assert.Equal(t, []string{}, rules[0].CalendarIds)
	assert.Empty(t, buildAvailabilityRules(nil, "X", nil))
	assert.NotNil(t, buildAvailabilityRules(nil, "X", nil), "rules is never null on the wire")
}

func TestSortAvailabilityRules_UsesSortedIDs(t *testing.T) {
	rules := []models.AvailabilityRule{
		{FromIds: []string{"X_b", "X_a"}, ToIds: []string{"X_a"}, CalendarIds: []string{"X_c2", "X_c1"}},
		{FromIds: []string{"X_a", "X_b"}, ToIds: []string{"X_a"}, CalendarIds: []string{"X_c0"}},
		{FromIds: []string{"X_z"}, ToIds: []string{"X_z"}, CalendarIds: []string{}},
	}
	sortAvailabilityRules(rules)
	assert.Equal(t, []string{}, rules[0].CalendarIds, "a rule with no calendars sorts first")
	assert.Equal(t, []string{"X_c0"}, rules[1].CalendarIds)
	assert.Equal(t, []string{"X_a", "X_b"}, rules[2].FromIds, "fromIds are sorted before rules are ordered")
	assert.Equal(t, []string{"X_c1", "X_c2"}, rules[2].CalendarIds)
}
