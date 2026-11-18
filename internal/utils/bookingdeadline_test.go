package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds a test-only reference implementation of the client-side
// booking deadline evaluation (wiki §2.5 with the spec §6 corrections) and the
// tests that run it. Maglev never computes deadlines on the wire; the
// implementation exists so the shared testdata/flex-booking-vectors.json is
// proven consistent before iOS and Android implement the same algorithm
// against it, and it lives in a _test.go file so it never ships.

// bookingState is the evaluation outcome. unknown is client-only: a rule whose
// conditionally required fields are missing cannot yield a deadline.
type bookingState string

const (
	bookingNotYetOpen    bookingState = "notYetOpen"
	bookingOpen          bookingState = "open"
	bookingClosedForDate bookingState = "closedForDate"
	bookingUnknown       bookingState = "unknown"
)

// Booking types from booking_rules.txt.
const (
	bookingTypeRealTime  = 0
	bookingTypeSameDay   = 1
	bookingTypePriorDays = 2
)

const (
	civilDateLayout   = "2006-01-02"
	defaultLatestTime = "24:00:00"
	defaultEarliest   = "00:00:00"
)

// calendarWeekdays are the wire day names, indexed by time.Weekday.
var calendarWeekdays = [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// bookingCalendar is the wire calendar shape (wiki §2.4).
type bookingCalendar struct {
	ID            string   `json:"id"`
	Days          []string `json:"days"`
	StartDate     string   `json:"startDate"`
	EndDate       string   `json:"endDate"`
	ExceptedDates []string `json:"exceptedDates"`
}

// bookingRuleInput is the subset of the wire bookingRule the evaluator reads.
type bookingRuleInput struct {
	BookingType            int
	PriorNoticeDurationMin *int
	PriorNoticeDurationMax *int
	PriorNoticeLastDay     *int
	PriorNoticeStartDay    *int
	PriorNoticeLastTime    *string
	PriorNoticeStartTime   *string
	PriorNoticeCalendarID  *string
}

// bookingWindow is the pickup side of an availabilityRule.
type bookingWindow struct {
	StartPickupTime *string
	EndPickupTime   *string
	CalendarIDs     []string
}

// bookingEvaluation is the result for one (rule, travel date) pair.
type bookingEvaluation struct {
	State         bookingState
	CutoffInstant *time.Time
	OpenInstant   *time.Time
}

// serviceDayInstant is GTFS's DST-safe convention: local noon of the civil date
// minus twelve hours, plus the time of day (which may exceed 24:00:00).
func serviceDayInstant(civilDate time.Time, timeOfDay string, loc *time.Location) (time.Time, error) {
	offset, err := parseGTFSTimeOfDay(timeOfDay)
	if err != nil {
		return time.Time{}, err
	}
	noon := time.Date(civilDate.Year(), civilDate.Month(), civilDate.Day(), 12, 0, 0, 0, loc)
	return noon.Add(-12 * time.Hour).Add(offset), nil
}

// calendarActiveOn reports whether the calendar runs on the civil date.
func calendarActiveOn(calendar bookingCalendar, civilDate time.Time) bool {
	date := civilDate.Format(civilDateLayout)
	if date < calendar.StartDate || date > calendar.EndDate {
		return false
	}
	for _, excepted := range calendar.ExceptedDates {
		if excepted == date {
			return false
		}
	}
	weekday := calendarWeekdays[civilDate.Weekday()]
	for _, day := range calendar.Days {
		if day == weekday {
			return true
		}
	}
	return false
}

// maxCountBackDays caps the civil days countBackServiceDays will walk. It is a
// backstop against calendars too sparse for any real prior-notice window.
const maxCountBackDays = 400

// countBackServiceDays steps back from the civil date one day at a time. With a
// calendar, only days the calendar is active on are counted; without one,
// every civil day counts. Zero days returns the date itself. It reports false
// when the count cannot complete, which the evaluator maps to unknown.
func countBackServiceDays(civilDate time.Time, days int, calendar *bookingCalendar) (time.Time, bool) {
	if calendar == nil {
		return civilDate.AddDate(0, 0, -days), true
	}
	if days == 0 {
		return civilDate, true
	}
	startDate, err := time.Parse(civilDateLayout, calendar.StartDate)
	if err != nil || !hasActiveWeekday(*calendar) {
		return time.Time{}, false
	}

	current := civilDate
	counted := 0
	for walked := 0; walked < maxCountBackDays; walked++ {
		current = current.AddDate(0, 0, -1)
		// No service day precedes the calendar's start, so a count that reaches
		// it cannot be consumed; the deadline is unknown rather than guessed.
		if current.Before(startDate) {
			return time.Time{}, false
		}
		if calendarActiveOn(*calendar, current) {
			counted++
		}
		if counted == days {
			return current, true
		}
	}
	return time.Time{}, false
}

func hasActiveWeekday(calendar bookingCalendar) bool {
	for _, day := range calendar.Days {
		for _, weekday := range calendarWeekdays {
			if day == weekday {
				return true
			}
		}
	}
	return false
}

// evaluateBookingDeadline implements evaluate(rule, bookingRule, D, now, tz, calendars).
// A nil rule means no notice is required: open with no cutoff.
func evaluateBookingDeadline(window bookingWindow, rule *bookingRuleInput, travelDate, now time.Time, loc *time.Location, calendars map[string]bookingCalendar) (bookingEvaluation, error) {
	if rule == nil {
		return bookingEvaluation{State: bookingOpen}, nil
	}
	latestPickup, err := serviceDayInstant(travelDate, stringOr(window.EndPickupTime, defaultLatestTime), loc)
	if err != nil {
		return bookingEvaluation{}, err
	}

	var cutoff, open *time.Time
	switch rule.BookingType {
	case bookingTypeRealTime:
		cutoff = &latestPickup
	case bookingTypeSameDay:
		cutoff, open, err = sameDayBounds(window, rule, travelDate, latestPickup, loc)
	case bookingTypePriorDays:
		cutoff, open, err = priorDaysBounds(rule, travelDate, loc, calendars)
	default:
		return bookingEvaluation{State: bookingUnknown}, nil
	}
	if err != nil {
		return bookingEvaluation{}, err
	}
	if cutoff == nil {
		return bookingEvaluation{State: bookingUnknown}, nil
	}

	state := bookingOpen
	switch {
	case open != nil && now.Before(*open):
		state = bookingNotYetOpen
	case now.After(*cutoff):
		state = bookingClosedForDate
	}
	return bookingEvaluation{State: state, CutoffInstant: cutoff, OpenInstant: open}, nil
}

// sameDayBounds: cutoff = latest pickup − durationMin; open from durationMax
// (before the window start) or startDay/startTime; a null durationMin makes
// the rule unknown rather than inventing a zero-minute notice.
func sameDayBounds(window bookingWindow, rule *bookingRuleInput, travelDate, latestPickup time.Time, loc *time.Location) (*time.Time, *time.Time, error) {
	if rule.PriorNoticeDurationMin == nil {
		return nil, nil, nil
	}
	cutoff := latestPickup.Add(-time.Duration(*rule.PriorNoticeDurationMin) * time.Minute)

	switch {
	case rule.PriorNoticeDurationMax != nil:
		earliestPickup, err := serviceDayInstant(travelDate, stringOr(window.StartPickupTime, defaultEarliest), loc)
		if err != nil {
			return nil, nil, err
		}
		open := earliestPickup.Add(-time.Duration(*rule.PriorNoticeDurationMax) * time.Minute)
		return &cutoff, &open, nil
	case rule.PriorNoticeStartDay != nil:
		open, err := serviceDayInstant(travelDate.AddDate(0, 0, -*rule.PriorNoticeStartDay), stringOr(rule.PriorNoticeStartTime, defaultEarliest), loc)
		if err != nil {
			return nil, nil, err
		}
		return &cutoff, &open, nil
	default:
		return &cutoff, nil, nil
	}
}

// priorDaysBounds: last day and start day are both counted on the prior-notice
// calendar when one is named (spec §6.1); a null last time means midnight of
// the last day (conservative); a null last day, or a count-back on either day
// that cannot complete, makes the rule unknown.
func priorDaysBounds(rule *bookingRuleInput, travelDate time.Time, loc *time.Location, calendars map[string]bookingCalendar) (*time.Time, *time.Time, error) {
	if rule.PriorNoticeLastDay == nil {
		return nil, nil, nil
	}
	var countingCalendar *bookingCalendar
	if rule.PriorNoticeCalendarID != nil {
		if calendar, ok := calendars[*rule.PriorNoticeCalendarID]; ok {
			countingCalendar = &calendar
		}
	}

	lastDayDate, ok := countBackServiceDays(travelDate, *rule.PriorNoticeLastDay, countingCalendar)
	if !ok {
		return nil, nil, nil
	}
	cutoff, err := serviceDayInstant(lastDayDate, stringOr(rule.PriorNoticeLastTime, defaultEarliest), loc)
	if err != nil {
		return nil, nil, err
	}
	if rule.PriorNoticeStartDay == nil {
		return &cutoff, nil, nil
	}
	startDayDate, ok := countBackServiceDays(travelDate, *rule.PriorNoticeStartDay, countingCalendar)
	if !ok {
		return nil, nil, nil
	}
	open, err := serviceDayInstant(startDayDate, stringOr(rule.PriorNoticeStartTime, defaultEarliest), loc)
	if err != nil {
		return nil, nil, err
	}
	return &cutoff, &open, nil
}

// nextBookableServiceDate is the earliest active service day of the window's
// calendars, from the agency-local today up to the latest calendar end date,
// whose evaluation is open. Nil when there is none.
func nextBookableServiceDate(window bookingWindow, rule *bookingRuleInput, now time.Time, loc *time.Location, calendars map[string]bookingCalendar) (*time.Time, error) {
	var ruleCalendars []bookingCalendar
	lastDate := ""
	for _, id := range window.CalendarIDs {
		calendar, ok := calendars[id]
		if !ok {
			continue
		}
		ruleCalendars = append(ruleCalendars, calendar)
		if calendar.EndDate > lastDate {
			lastDate = calendar.EndDate
		}
	}
	if lastDate == "" {
		return nil, nil
	}
	end, err := time.Parse(civilDateLayout, lastDate)
	if err != nil {
		return nil, fmt.Errorf("invalid calendar end date %q: %w", lastDate, err)
	}

	local := now.In(loc)
	for date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC); !date.After(end); date = date.AddDate(0, 0, 1) {
		if !anyCalendarActive(ruleCalendars, date) {
			continue
		}
		evaluation, err := evaluateBookingDeadline(window, rule, date, now, loc, calendars)
		if err != nil {
			return nil, err
		}
		if evaluation.State == bookingOpen {
			return &date, nil
		}
	}
	return nil, nil
}

func anyCalendarActive(ruleCalendars []bookingCalendar, date time.Time) bool {
	for _, calendar := range ruleCalendars {
		if calendarActiveOn(calendar, date) {
			return true
		}
	}
	return false
}

func stringOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

// parseGTFSTimeOfDay parses a GTFS "HH:MM:SS" time of day, where hours may
// exceed 24 for service days that run past midnight.
func parseGTFSTimeOfDay(value string) (time.Duration, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid time of day %q: want HH:MM:SS", value)
	}
	fields := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || (i > 0 && n > 59) {
			return 0, fmt.Errorf("invalid time of day %q", value)
		}
		fields[i] = n
	}
	return time.Duration(fields[0])*time.Hour + time.Duration(fields[1])*time.Minute + time.Duration(fields[2])*time.Second, nil
}

type bookingVectorRule struct {
	StartPickupTime *string  `json:"startPickupTime"`
	EndPickupTime   *string  `json:"endPickupTime"`
	CalendarIds     []string `json:"calendarIds"`
}

type bookingVectorBookingRule struct {
	ID                     string  `json:"id"`
	BookingType            int     `json:"bookingType"`
	PriorNoticeDurationMin *int    `json:"priorNoticeDurationMin"`
	PriorNoticeDurationMax *int    `json:"priorNoticeDurationMax"`
	PriorNoticeLastDay     *int    `json:"priorNoticeLastDay"`
	PriorNoticeLastTime    *string `json:"priorNoticeLastTime"`
	PriorNoticeStartDay    *int    `json:"priorNoticeStartDay"`
	PriorNoticeStartTime   *string `json:"priorNoticeStartTime"`
	PriorNoticeCalendarId  *string `json:"priorNoticeCalendarId"`
}

type bookingVector struct {
	Name        string                    `json:"name"`
	Timezone    string                    `json:"timezone"`
	BookingRule *bookingVectorBookingRule `json:"bookingRule"`
	Rule        bookingVectorRule         `json:"rule"`
	TravelDate  string                    `json:"travelDate"`
	Now         string                    `json:"now"`
	Expected    struct {
		State                   string  `json:"state"`
		CutoffInstant           *string `json:"cutoffInstant"`
		OpenInstant             *string `json:"openInstant"`
		NextBookableServiceDate *string `json:"nextBookableServiceDate"`
	} `json:"expected"`
}

type bookingVectorsFile struct {
	Timezone  string            `json:"timezone"`
	Calendars []bookingCalendar `json:"calendars"`
	Vectors   []bookingVector   `json:"vectors"`
}

func loadBookingVectors(t *testing.T) bookingVectorsFile {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/flex-booking-vectors.json")
	require.NoError(t, err)
	var file bookingVectorsFile
	require.NoError(t, json.Unmarshal(raw, &file))
	require.NotEmpty(t, file.Vectors)
	return file
}

func civilDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	require.NoError(t, err)
	return parsed
}

func assertSameInstant(t *testing.T, want *string, got *time.Time, label string) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got, label)
		return
	}
	require.NotNil(t, got, label)
	wantInstant, err := time.Parse(time.RFC3339, *want)
	require.NoError(t, err)
	assert.True(t, wantInstant.Equal(*got), "%s: want %s got %s", label, *want, got.Format(time.RFC3339))
	assert.Equal(t, *want, got.Format(time.RFC3339), "%s must be expressed in the agency offset", label)
}

func TestBookingVectors_MatchReferenceEvaluator(t *testing.T) {
	file := loadBookingVectors(t)
	calendars := make(map[string]bookingCalendar, len(file.Calendars))
	for _, calendar := range file.Calendars {
		calendars[calendar.ID] = calendar
	}

	for _, vector := range file.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			timezone := vector.Timezone
			if timezone == "" {
				timezone = file.Timezone
			}
			loc, err := time.LoadLocation(timezone)
			require.NoError(t, err)
			now, err := time.Parse(time.RFC3339, vector.Now)
			require.NoError(t, err)

			var rule *bookingRuleInput
			if vector.BookingRule != nil {
				rule = &bookingRuleInput{
					BookingType:            vector.BookingRule.BookingType,
					PriorNoticeDurationMin: vector.BookingRule.PriorNoticeDurationMin,
					PriorNoticeDurationMax: vector.BookingRule.PriorNoticeDurationMax,
					PriorNoticeLastDay:     vector.BookingRule.PriorNoticeLastDay,
					PriorNoticeStartDay:    vector.BookingRule.PriorNoticeStartDay,
					PriorNoticeLastTime:    vector.BookingRule.PriorNoticeLastTime,
					PriorNoticeStartTime:   vector.BookingRule.PriorNoticeStartTime,
					PriorNoticeCalendarID:  vector.BookingRule.PriorNoticeCalendarId,
				}
			}
			window := bookingWindow{StartPickupTime: vector.Rule.StartPickupTime, EndPickupTime: vector.Rule.EndPickupTime, CalendarIDs: vector.Rule.CalendarIds}

			evaluation, err := evaluateBookingDeadline(window, rule, civilDate(t, vector.TravelDate), now, loc, calendars)
			require.NoError(t, err)
			assert.Equal(t, bookingState(vector.Expected.State), evaluation.State)
			assertSameInstant(t, vector.Expected.CutoffInstant, evaluation.CutoffInstant, "cutoffInstant")
			assertSameInstant(t, vector.Expected.OpenInstant, evaluation.OpenInstant, "openInstant")

			next, err := nextBookableServiceDate(window, rule, now, loc, calendars)
			require.NoError(t, err)
			if vector.Expected.NextBookableServiceDate == nil {
				assert.Nil(t, next, "nextBookableServiceDate")
			} else {
				require.NotNil(t, next, "nextBookableServiceDate")
				assert.Equal(t, *vector.Expected.NextBookableServiceDate, next.Format("2006-01-02"))
			}
		})
	}
}

func TestBookingVectors_CoverEveryRequiredCase(t *testing.T) {
	file := loadBookingVectors(t)
	states := map[string]int{}
	for _, vector := range file.Vectors {
		states[vector.Expected.State]++
	}
	for _, state := range []string{"open", "closedForDate", "notYetOpen", "unknown"} {
		assert.Greater(t, states[state], 0, "at least one vector must reach state %s", state)
	}
	assert.GreaterOrEqual(t, len(file.Vectors), 21)
}

func TestServiceDayInstant_DSTAnchor(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)

	instant, err := serviceDayInstant(civilDate(t, "2026-03-08"), "18:00:00", detroit)
	require.NoError(t, err)
	assert.Equal(t, "2026-03-08T18:00:00-04:00", instant.Format(time.RFC3339), "noon anchor keeps 18:00 at 18:00 across spring-forward")

	instant, err = serviceDayInstant(civilDate(t, "2026-11-01"), "18:00:00", detroit)
	require.NoError(t, err)
	assert.Equal(t, "2026-11-01T18:00:00-05:00", instant.Format(time.RFC3339), "and across fall-back")

	_, err = serviceDayInstant(civilDate(t, "2026-03-08"), "6pm", detroit)
	assert.Error(t, err)
}

func TestCountBackServiceDays(t *testing.T) {
	weekday := bookingCalendar{ID: "wk", Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartDate: "2024-01-01", EndDate: "2027-12-31", ExceptedDates: []string{"2026-03-13"}}
	monday := civilDate(t, "2026-03-16")

	tests := []struct {
		name     string
		days     int
		calendar *bookingCalendar
		want     string
	}{
		{"no calendar counts civil days", 1, nil, "2026-03-15"},
		{"skips the weekend and the excepted Friday", 1, &weekday, "2026-03-12"},
		{"zero days is the date itself", 0, &weekday, "2026-03-16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := countBackServiceDays(monday, tt.days, tt.calendar)
			require.True(t, ok)
			assert.Equal(t, civilDate(t, tt.want), got)
		})
	}
	assert.False(t, calendarActiveOn(weekday, civilDate(t, "2028-01-03")), "outside the date range")
}

func TestCountBackServiceDays_ReportsCountsThatCannotComplete(t *testing.T) {
	weekdays := []string{"mon", "tue", "wed", "thu", "fri"}
	tests := []struct {
		name     string
		calendar bookingCalendar
		days     int
	}{
		{"reaches the calendar start date first", bookingCalendar{Days: weekdays, StartDate: "2026-03-12", EndDate: "2027-12-31"}, 3},
		{"empty start date", bookingCalendar{Days: weekdays, EndDate: "2027-12-31"}, 1},
		{"unparseable start date", bookingCalendar{Days: weekdays, StartDate: "March 2026", EndDate: "2027-12-31"}, 1},
		{"no active weekdays", bookingCalendar{Days: []string{"monday"}, StartDate: "2024-01-01", EndDate: "2027-12-31"}, 1},
		{"walk exceeds the backstop cap", bookingCalendar{Days: []string{"mon"}, StartDate: "2000-01-01", EndDate: "2027-12-31"}, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := countBackServiceDays(civilDate(t, "2026-03-16"), tt.days, &tt.calendar)
			assert.False(t, ok)
		})
	}
}

func intPtr(value int) *int { return &value }

func stringPtr(value string) *string { return &value }

func TestEvaluateBookingDeadline_BranchesOutsideTheVectors(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)
	weekdays := bookingCalendar{ID: "wk", Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartDate: "2024-01-01", EndDate: "2027-12-31"}
	calendars := map[string]bookingCalendar{weekdays.ID: weekdays}
	window := bookingWindow{StartPickupTime: stringPtr("05:30:00"), EndPickupTime: stringPtr("18:00:00"), CalendarIDs: []string{weekdays.ID}}

	tests := []struct {
		name       string
		window     bookingWindow
		rule       bookingRuleInput
		travelDate string
		now        string
		wantState  bookingState
		wantCutoff *string
		wantOpen   *string
	}{
		{
			name:       "type 1 durationMax opens that many minutes before the earliest pickup",
			window:     window,
			rule:       bookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeDurationMax: intPtr(120)},
			travelDate: "2026-03-11",
			now:        "2026-03-11T03:00:00-04:00",
			wantState:  bookingNotYetOpen,
			wantCutoff: stringPtr("2026-03-11T17:00:00-04:00"),
			wantOpen:   stringPtr("2026-03-11T03:30:00-04:00"),
		},
		{
			name:       "type 2 prior-notice calendar missing from the references counts civil days",
			window:     window,
			rule:       bookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeLastTime: stringPtr("15:00:00"), PriorNoticeCalendarID: stringPtr("absent")},
			travelDate: "2026-03-16",
			now:        "2026-03-15T09:00:00-04:00",
			wantState:  bookingOpen,
			wantCutoff: stringPtr("2026-03-15T15:00:00-04:00"),
		},
		{
			name:       "unsupported booking type is unknown",
			window:     window,
			rule:       bookingRuleInput{BookingType: 3},
			travelDate: "2026-03-11",
			now:        "2026-03-10T09:00:00-04:00",
			wantState:  bookingUnknown,
		},
		{
			name:       "missing end pickup time defaults to 24:00:00",
			window:     bookingWindow{CalendarIDs: []string{weekdays.ID}},
			rule:       bookingRuleInput{BookingType: 0},
			travelDate: "2026-03-11",
			now:        "2026-03-11T23:30:00-04:00",
			wantState:  bookingOpen,
			wantCutoff: stringPtr("2026-03-12T00:00:00-04:00"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			require.NoError(t, err)

			evaluation, err := evaluateBookingDeadline(tt.window, &tt.rule, civilDate(t, tt.travelDate), now, detroit, calendars)

			require.NoError(t, err)
			assert.Equal(t, tt.wantState, evaluation.State)
			assertSameInstant(t, tt.wantCutoff, evaluation.CutoffInstant, "cutoffInstant")
			assertSameInstant(t, tt.wantOpen, evaluation.OpenInstant, "openInstant")
		})
	}
}

func TestEvaluateBookingDeadline_RejectsMalformedTimes(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)
	goodWindow := bookingWindow{StartPickupTime: stringPtr("05:30:00"), EndPickupTime: stringPtr("18:00:00")}

	tests := []struct {
		name   string
		window bookingWindow
		rule   bookingRuleInput
	}{
		{"end pickup time", bookingWindow{EndPickupTime: stringPtr("6pm")}, bookingRuleInput{BookingType: 0}},
		{"start pickup time under durationMax", bookingWindow{StartPickupTime: stringPtr("6am")}, bookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeDurationMax: intPtr(120)}},
		{"type 1 start time", goodWindow, bookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeStartDay: intPtr(1), PriorNoticeStartTime: stringPtr("8am")}},
		{"type 2 last time", goodWindow, bookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeLastTime: stringPtr("3pm")}},
		{"type 2 start time", goodWindow, bookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeStartDay: intPtr(3), PriorNoticeStartTime: stringPtr("9am")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := evaluateBookingDeadline(tt.window, &tt.rule, civilDate(t, "2026-03-11"), time.Now(), detroit, nil)
			assert.Error(t, err)
		})
	}
}

func TestNextBookableServiceDate_Edges(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)
	now, err := time.Parse(time.RFC3339, "2026-03-11T12:00:00-04:00")
	require.NoError(t, err)
	realTime := &bookingRuleInput{BookingType: 0}

	next, err := nextBookableServiceDate(bookingWindow{CalendarIDs: []string{"absent"}}, realTime, now, detroit, map[string]bookingCalendar{})
	require.NoError(t, err)
	assert.Nil(t, next, "no referenced calendar is known")

	malformedEnd := map[string]bookingCalendar{"bad": {ID: "bad", Days: []string{"wed"}, StartDate: "2026-01-01", EndDate: "Dec 2026"}}
	_, err = nextBookableServiceDate(bookingWindow{CalendarIDs: []string{"bad"}}, realTime, now, detroit, malformedEnd)
	assert.Error(t, err, "calendar end date is not YYYY-MM-DD")

	daily := map[string]bookingCalendar{"daily": {ID: "daily", Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, StartDate: "2026-01-01", EndDate: "2026-12-31"}}
	_, err = nextBookableServiceDate(bookingWindow{EndPickupTime: stringPtr("6pm"), CalendarIDs: []string{"daily"}}, realTime, now, detroit, daily)
	assert.Error(t, err, "evaluation errors propagate")
}

func TestParseGTFSTimeOfDay(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"00:00:00", 0, false},
		{"05:30:00", 5*time.Hour + 30*time.Minute, false},
		{"24:50:00", 24*time.Hour + 50*time.Minute, false},
		{"25:00:05", 25*time.Hour + 5*time.Second, false},
		{"5:30", 0, true},
		{"aa:00:00", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parseGTFSTimeOfDay(tt.value)
		if tt.wantErr {
			assert.Error(t, err, tt.value)
			continue
		}
		require.NoError(t, err, tt.value)
		assert.Equal(t, tt.want, got, tt.value)
	}
}
