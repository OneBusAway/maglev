package utils

import (
	"fmt"
	"time"
)

// This file is the reference implementation of the client-side booking
// deadline evaluation (wiki §2.5 with the spec §6 corrections). Maglev never
// computes deadlines on the wire; the implementation exists so the shared
// testdata/flex-booking-vectors.json is proven consistent before iOS and
// Android implement the same algorithm against it.

// BookingState is the evaluation outcome. unknown is client-only: a rule whose
// conditionally required fields are missing cannot yield a deadline.
type BookingState string

const (
	BookingNotYetOpen    BookingState = "notYetOpen"
	BookingOpen          BookingState = "open"
	BookingClosedForDate BookingState = "closedForDate"
	BookingUnknown       BookingState = "unknown"
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

// BookingCalendar is the wire calendar shape (wiki §2.4).
type BookingCalendar struct {
	ID            string   `json:"id"`
	Days          []string `json:"days"`
	StartDate     string   `json:"startDate"`
	EndDate       string   `json:"endDate"`
	ExceptedDates []string `json:"exceptedDates"`
}

// BookingRuleInput is the subset of the wire bookingRule the evaluator reads.
type BookingRuleInput struct {
	BookingType            int
	PriorNoticeDurationMin *int
	PriorNoticeDurationMax *int
	PriorNoticeLastDay     *int
	PriorNoticeStartDay    *int
	PriorNoticeLastTime    *string
	PriorNoticeStartTime   *string
	PriorNoticeCalendarID  *string
}

// BookingWindow is the pickup side of an availabilityRule.
type BookingWindow struct {
	StartPickupTime *string
	EndPickupTime   *string
	CalendarIDs     []string
}

// BookingEvaluation is the result for one (rule, travel date) pair.
type BookingEvaluation struct {
	State         BookingState
	CutoffInstant *time.Time
	OpenInstant   *time.Time
}

// ServiceDayInstant is GTFS's DST-safe convention: local noon of the civil date
// minus twelve hours, plus the time of day (which may exceed 24:00:00).
func ServiceDayInstant(civilDate time.Time, timeOfDay string, loc *time.Location) (time.Time, error) {
	offset, err := ParseGTFSTimeOfDay(timeOfDay)
	if err != nil {
		return time.Time{}, err
	}
	noon := time.Date(civilDate.Year(), civilDate.Month(), civilDate.Day(), 12, 0, 0, 0, loc)
	return noon.Add(-12 * time.Hour).Add(offset), nil
}

// CalendarActiveOn reports whether the calendar runs on the civil date.
func CalendarActiveOn(calendar BookingCalendar, civilDate time.Time) bool {
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

// maxCountBackDays caps the civil days CountBackServiceDays will walk. It is a
// backstop against calendars too sparse for any real prior-notice window.
const maxCountBackDays = 400

// CountBackServiceDays steps back from the civil date one day at a time. With a
// calendar, only days the calendar is active on are counted; without one,
// every civil day counts. Zero days returns the date itself. It reports false
// when the count cannot complete, which the evaluator maps to unknown.
func CountBackServiceDays(civilDate time.Time, days int, calendar *BookingCalendar) (time.Time, bool) {
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
		if CalendarActiveOn(*calendar, current) {
			counted++
		}
		if counted == days {
			return current, true
		}
	}
	return time.Time{}, false
}

func hasActiveWeekday(calendar BookingCalendar) bool {
	for _, day := range calendar.Days {
		for _, weekday := range calendarWeekdays {
			if day == weekday {
				return true
			}
		}
	}
	return false
}

// EvaluateBookingDeadline implements evaluate(rule, bookingRule, D, now, tz, calendars).
// A nil rule means no notice is required: open with no cutoff.
func EvaluateBookingDeadline(window BookingWindow, rule *BookingRuleInput, travelDate, now time.Time, loc *time.Location, calendars map[string]BookingCalendar) (BookingEvaluation, error) {
	if rule == nil {
		return BookingEvaluation{State: BookingOpen}, nil
	}
	latestPickup, err := ServiceDayInstant(travelDate, stringOr(window.EndPickupTime, defaultLatestTime), loc)
	if err != nil {
		return BookingEvaluation{}, err
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
		return BookingEvaluation{State: BookingUnknown}, nil
	}
	if err != nil {
		return BookingEvaluation{}, err
	}
	if cutoff == nil {
		return BookingEvaluation{State: BookingUnknown}, nil
	}

	state := BookingOpen
	switch {
	case open != nil && now.Before(*open):
		state = BookingNotYetOpen
	case now.After(*cutoff):
		state = BookingClosedForDate
	}
	return BookingEvaluation{State: state, CutoffInstant: cutoff, OpenInstant: open}, nil
}

// sameDayBounds: cutoff = latest pickup − durationMin; open from durationMax
// (before the window start) or startDay/startTime; a null durationMin makes
// the rule unknown rather than inventing a zero-minute notice.
func sameDayBounds(window BookingWindow, rule *BookingRuleInput, travelDate, latestPickup time.Time, loc *time.Location) (*time.Time, *time.Time, error) {
	if rule.PriorNoticeDurationMin == nil {
		return nil, nil, nil
	}
	cutoff := latestPickup.Add(-time.Duration(*rule.PriorNoticeDurationMin) * time.Minute)

	switch {
	case rule.PriorNoticeDurationMax != nil:
		earliestPickup, err := ServiceDayInstant(travelDate, stringOr(window.StartPickupTime, defaultEarliest), loc)
		if err != nil {
			return nil, nil, err
		}
		open := earliestPickup.Add(-time.Duration(*rule.PriorNoticeDurationMax) * time.Minute)
		return &cutoff, &open, nil
	case rule.PriorNoticeStartDay != nil:
		open, err := ServiceDayInstant(travelDate.AddDate(0, 0, -*rule.PriorNoticeStartDay), stringOr(rule.PriorNoticeStartTime, defaultEarliest), loc)
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
func priorDaysBounds(rule *BookingRuleInput, travelDate time.Time, loc *time.Location, calendars map[string]BookingCalendar) (*time.Time, *time.Time, error) {
	if rule.PriorNoticeLastDay == nil {
		return nil, nil, nil
	}
	var countingCalendar *BookingCalendar
	if rule.PriorNoticeCalendarID != nil {
		if calendar, ok := calendars[*rule.PriorNoticeCalendarID]; ok {
			countingCalendar = &calendar
		}
	}

	lastDayDate, ok := CountBackServiceDays(travelDate, *rule.PriorNoticeLastDay, countingCalendar)
	if !ok {
		return nil, nil, nil
	}
	cutoff, err := ServiceDayInstant(lastDayDate, stringOr(rule.PriorNoticeLastTime, defaultEarliest), loc)
	if err != nil {
		return nil, nil, err
	}
	if rule.PriorNoticeStartDay == nil {
		return &cutoff, nil, nil
	}
	startDayDate, ok := CountBackServiceDays(travelDate, *rule.PriorNoticeStartDay, countingCalendar)
	if !ok {
		return nil, nil, nil
	}
	open, err := ServiceDayInstant(startDayDate, stringOr(rule.PriorNoticeStartTime, defaultEarliest), loc)
	if err != nil {
		return nil, nil, err
	}
	return &cutoff, &open, nil
}

// NextBookableServiceDate is the earliest active service day of the window's
// calendars, from the agency-local today up to the latest calendar end date,
// whose evaluation is open. Nil when there is none.
func NextBookableServiceDate(window BookingWindow, rule *BookingRuleInput, now time.Time, loc *time.Location, calendars map[string]BookingCalendar) (*time.Time, error) {
	var ruleCalendars []BookingCalendar
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
		evaluation, err := EvaluateBookingDeadline(window, rule, date, now, loc, calendars)
		if err != nil {
			return nil, err
		}
		if evaluation.State == BookingOpen {
			return &date, nil
		}
	}
	return nil, nil
}

func anyCalendarActive(ruleCalendars []BookingCalendar, date time.Time) bool {
	for _, calendar := range ruleCalendars {
		if CalendarActiveOn(calendar, date) {
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
