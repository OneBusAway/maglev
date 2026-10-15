package restapi

import (
	"log/slog"
	"slices"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

const (
	addedDateCalendarSuffix = "_added_"
	gtfsDateLayout          = "20060102"
	isoDateLayout           = "2006-01-02"
	calendarDateAdded       = 1
	calendarDateRemoved     = 2
)

// compileOnDemandCalendars turns a GTFS service into GOFS-shaped calendars: the
// base calendar (when the row exists and has at least one service day) with
// removed dates as exceptedDates, plus one single-day calendar per added date.
// A service with no usable base emits only the added-date calendars.
func compileOnDemandCalendars(agencyID, serviceID string, base *gtfsdb.Calendar, exceptions []gtfsdb.CalendarDate) []models.OnDemandCalendar {
	var calendars []models.OnDemandCalendar

	if base != nil && calendarHasServiceDays(*base) {
		calendar := models.OnDemandCalendar{
			ID:            utils.FormCombinedID(agencyID, serviceID),
			Days:          calendarDays(*base),
			StartDate:     isoDate(base.StartDate),
			EndDate:       isoDate(base.EndDate),
			ExceptedDates: []string{},
		}
		for _, exception := range exceptions {
			if exception.ExceptionType == calendarDateRemoved {
				calendar.ExceptedDates = append(calendar.ExceptedDates, isoDate(exception.Date))
			}
		}
		slices.Sort(calendar.ExceptedDates)
		calendars = append(calendars, calendar)
	}

	for _, exception := range exceptions {
		if exception.ExceptionType != calendarDateAdded {
			continue
		}
		date := isoDate(exception.Date)
		calendars = append(calendars, models.OnDemandCalendar{
			ID:            utils.FormCombinedID(agencyID, serviceID+addedDateCalendarSuffix+exception.Date),
			Days:          []string{weekdayName(exception.Date)},
			StartDate:     date,
			EndDate:       date,
			ExceptedDates: []string{},
		})
	}
	return calendars
}

// wellFormedCalendarDates drops, with a warning, any row whose date is not
// YYYYMMDD. Such a date has no weekday or ISO form, so it would otherwise go
// out as a calendar clients cannot parse. The importer writes every date from
// a parsed time, so this only guards against a hand-edited database.
func wellFormedCalendarDates(logger *slog.Logger, rows []gtfsdb.CalendarDate) []gtfsdb.CalendarDate {
	wellFormed := make([]gtfsdb.CalendarDate, 0, len(rows))
	for _, row := range rows {
		if _, err := time.Parse(gtfsDateLayout, row.Date); err != nil {
			logging.LogError(logger, "Skipping malformed calendar date", err,
				slog.String("service_id", row.ServiceID))
			continue
		}
		wellFormed = append(wellFormed, row)
	}
	return wellFormed
}

func calendarHasServiceDays(calendar gtfsdb.Calendar) bool {
	return len(calendarDays(calendar)) > 0
}

// calendarDays lists the active weekdays in GOFS order, Monday first.
func calendarDays(calendar gtfsdb.Calendar) []string {
	flags := []struct {
		name   string
		active int64
	}{
		{"mon", calendar.Monday}, {"tue", calendar.Tuesday}, {"wed", calendar.Wednesday},
		{"thu", calendar.Thursday}, {"fri", calendar.Friday}, {"sat", calendar.Saturday}, {"sun", calendar.Sunday},
	}
	days := make([]string, 0, len(flags))
	for _, flag := range flags {
		if flag.active == 1 {
			days = append(days, flag.name)
		}
	}
	return days
}

// isoDate converts a GTFS YYYYMMDD date to YYYY-MM-DD; malformed input is returned unchanged.
func isoDate(gtfsDate string) string {
	parsed, err := time.Parse(gtfsDateLayout, gtfsDate)
	if err != nil {
		return gtfsDate
	}
	return parsed.Format(isoDateLayout)
}

// weekdayName maps a GTFS date to the GOFS day token of its weekday.
func weekdayName(gtfsDate string) string {
	parsed, err := time.Parse(gtfsDateLayout, gtfsDate)
	if err != nil {
		return ""
	}
	return [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}[parsed.Weekday()]
}
