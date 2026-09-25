package utils

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	Calendars []BookingCalendar `json:"calendars"`
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
	calendars := make(map[string]BookingCalendar, len(file.Calendars))
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

			var rule *BookingRuleInput
			if vector.BookingRule != nil {
				rule = &BookingRuleInput{
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
			window := BookingWindow{StartPickupTime: vector.Rule.StartPickupTime, EndPickupTime: vector.Rule.EndPickupTime, CalendarIDs: vector.Rule.CalendarIds}

			evaluation, err := EvaluateBookingDeadline(window, rule, civilDate(t, vector.TravelDate), now, loc, calendars)
			require.NoError(t, err)
			assert.Equal(t, BookingState(vector.Expected.State), evaluation.State)
			assertSameInstant(t, vector.Expected.CutoffInstant, evaluation.CutoffInstant, "cutoffInstant")
			assertSameInstant(t, vector.Expected.OpenInstant, evaluation.OpenInstant, "openInstant")

			next, err := NextBookableServiceDate(window, rule, now, loc, calendars)
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

	instant, err := ServiceDayInstant(civilDate(t, "2026-03-08"), "18:00:00", detroit)
	require.NoError(t, err)
	assert.Equal(t, "2026-03-08T18:00:00-04:00", instant.Format(time.RFC3339), "noon anchor keeps 18:00 at 18:00 across spring-forward")

	instant, err = ServiceDayInstant(civilDate(t, "2026-11-01"), "18:00:00", detroit)
	require.NoError(t, err)
	assert.Equal(t, "2026-11-01T18:00:00-05:00", instant.Format(time.RFC3339), "and across fall-back")

	_, err = ServiceDayInstant(civilDate(t, "2026-03-08"), "6pm", detroit)
	assert.Error(t, err)
}

func TestCountBackServiceDays(t *testing.T) {
	weekday := BookingCalendar{ID: "wk", Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartDate: "2024-01-01", EndDate: "2027-12-31", ExceptedDates: []string{"2026-03-13"}}
	monday := civilDate(t, "2026-03-16")

	tests := []struct {
		name     string
		days     int
		calendar *BookingCalendar
		want     string
	}{
		{"no calendar counts civil days", 1, nil, "2026-03-15"},
		{"skips the weekend and the excepted Friday", 1, &weekday, "2026-03-12"},
		{"zero days is the date itself", 0, &weekday, "2026-03-16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CountBackServiceDays(monday, tt.days, tt.calendar)
			require.True(t, ok)
			assert.Equal(t, civilDate(t, tt.want), got)
		})
	}
	assert.False(t, CalendarActiveOn(weekday, civilDate(t, "2028-01-03")), "outside the date range")
}

func TestCountBackServiceDays_ReportsCountsThatCannotComplete(t *testing.T) {
	weekdays := []string{"mon", "tue", "wed", "thu", "fri"}
	tests := []struct {
		name     string
		calendar BookingCalendar
		days     int
	}{
		{"reaches the calendar start date first", BookingCalendar{Days: weekdays, StartDate: "2026-03-12", EndDate: "2027-12-31"}, 3},
		{"empty start date", BookingCalendar{Days: weekdays, EndDate: "2027-12-31"}, 1},
		{"unparseable start date", BookingCalendar{Days: weekdays, StartDate: "March 2026", EndDate: "2027-12-31"}, 1},
		{"no active weekdays", BookingCalendar{Days: []string{"monday"}, StartDate: "2024-01-01", EndDate: "2027-12-31"}, 1},
		{"walk exceeds the backstop cap", BookingCalendar{Days: []string{"mon"}, StartDate: "2000-01-01", EndDate: "2027-12-31"}, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := CountBackServiceDays(civilDate(t, "2026-03-16"), tt.days, &tt.calendar)
			assert.False(t, ok)
		})
	}
}

func intPtr(value int) *int { return &value }

func stringPtr(value string) *string { return &value }

func TestEvaluateBookingDeadline_BranchesOutsideTheVectors(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)
	weekdays := BookingCalendar{ID: "wk", Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartDate: "2024-01-01", EndDate: "2027-12-31"}
	calendars := map[string]BookingCalendar{weekdays.ID: weekdays}
	window := BookingWindow{StartPickupTime: stringPtr("05:30:00"), EndPickupTime: stringPtr("18:00:00"), CalendarIDs: []string{weekdays.ID}}

	tests := []struct {
		name       string
		window     BookingWindow
		rule       BookingRuleInput
		travelDate string
		now        string
		wantState  BookingState
		wantCutoff *string
		wantOpen   *string
	}{
		{
			name:       "type 1 durationMax opens that many minutes before the earliest pickup",
			window:     window,
			rule:       BookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeDurationMax: intPtr(120)},
			travelDate: "2026-03-11",
			now:        "2026-03-11T03:00:00-04:00",
			wantState:  BookingNotYetOpen,
			wantCutoff: stringPtr("2026-03-11T17:00:00-04:00"),
			wantOpen:   stringPtr("2026-03-11T03:30:00-04:00"),
		},
		{
			name:       "type 2 prior-notice calendar missing from the references counts civil days",
			window:     window,
			rule:       BookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeLastTime: stringPtr("15:00:00"), PriorNoticeCalendarID: stringPtr("absent")},
			travelDate: "2026-03-16",
			now:        "2026-03-15T09:00:00-04:00",
			wantState:  BookingOpen,
			wantCutoff: stringPtr("2026-03-15T15:00:00-04:00"),
		},
		{
			name:       "unsupported booking type is unknown",
			window:     window,
			rule:       BookingRuleInput{BookingType: 3},
			travelDate: "2026-03-11",
			now:        "2026-03-10T09:00:00-04:00",
			wantState:  BookingUnknown,
		},
		{
			name:       "missing end pickup time defaults to 24:00:00",
			window:     BookingWindow{CalendarIDs: []string{weekdays.ID}},
			rule:       BookingRuleInput{BookingType: 0},
			travelDate: "2026-03-11",
			now:        "2026-03-11T23:30:00-04:00",
			wantState:  BookingOpen,
			wantCutoff: stringPtr("2026-03-12T00:00:00-04:00"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tt.now)
			require.NoError(t, err)

			evaluation, err := EvaluateBookingDeadline(tt.window, &tt.rule, civilDate(t, tt.travelDate), now, detroit, calendars)

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
	goodWindow := BookingWindow{StartPickupTime: stringPtr("05:30:00"), EndPickupTime: stringPtr("18:00:00")}

	tests := []struct {
		name   string
		window BookingWindow
		rule   BookingRuleInput
	}{
		{"end pickup time", BookingWindow{EndPickupTime: stringPtr("6pm")}, BookingRuleInput{BookingType: 0}},
		{"start pickup time under durationMax", BookingWindow{StartPickupTime: stringPtr("6am")}, BookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeDurationMax: intPtr(120)}},
		{"type 1 start time", goodWindow, BookingRuleInput{BookingType: 1, PriorNoticeDurationMin: intPtr(60), PriorNoticeStartDay: intPtr(1), PriorNoticeStartTime: stringPtr("8am")}},
		{"type 2 last time", goodWindow, BookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeLastTime: stringPtr("3pm")}},
		{"type 2 start time", goodWindow, BookingRuleInput{BookingType: 2, PriorNoticeLastDay: intPtr(1), PriorNoticeStartDay: intPtr(3), PriorNoticeStartTime: stringPtr("9am")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := EvaluateBookingDeadline(tt.window, &tt.rule, civilDate(t, "2026-03-11"), time.Now(), detroit, nil)
			assert.Error(t, err)
		})
	}
}

func TestNextBookableServiceDate_Edges(t *testing.T) {
	detroit, err := time.LoadLocation("America/Detroit")
	require.NoError(t, err)
	now, err := time.Parse(time.RFC3339, "2026-03-11T12:00:00-04:00")
	require.NoError(t, err)
	realTime := &BookingRuleInput{BookingType: 0}

	next, err := NextBookableServiceDate(BookingWindow{CalendarIDs: []string{"absent"}}, realTime, now, detroit, map[string]BookingCalendar{})
	require.NoError(t, err)
	assert.Nil(t, next, "no referenced calendar is known")

	malformedEnd := map[string]BookingCalendar{"bad": {ID: "bad", Days: []string{"wed"}, StartDate: "2026-01-01", EndDate: "Dec 2026"}}
	_, err = NextBookableServiceDate(BookingWindow{CalendarIDs: []string{"bad"}}, realTime, now, detroit, malformedEnd)
	assert.Error(t, err, "calendar end date is not YYYY-MM-DD")

	daily := map[string]BookingCalendar{"daily": {ID: "daily", Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}, StartDate: "2026-01-01", EndDate: "2026-12-31"}}
	_, err = NextBookableServiceDate(BookingWindow{EndPickupTime: stringPtr("6pm"), CalendarIDs: []string{"daily"}}, realTime, now, detroit, daily)
	assert.Error(t, err, "evaluation errors propagate")
}
