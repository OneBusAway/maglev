package restapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

func TestCompileOnDemandCalendars(t *testing.T) {
	weekday := gtfsdb.Calendar{ID: "wk", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1, StartDate: "20240101", EndDate: "20271231"}
	datesOnly := gtfsdb.Calendar{ID: "ev", StartDate: "20260704", EndDate: "20260704"}

	tests := []struct {
		name       string
		base       *gtfsdb.Calendar
		exceptions []gtfsdb.CalendarDate
		want       []models.OnDemandCalendar
	}{
		{
			name: "base calendar with removed dates sorted",
			base: &weekday,
			exceptions: []gtfsdb.CalendarDate{
				{ServiceID: "wk", Date: "20251225", ExceptionType: 2},
				{ServiceID: "wk", Date: "20250101", ExceptionType: 2},
			},
			want: []models.OnDemandCalendar{{
				ID: "MC_wk", Days: []string{"mon", "tue", "wed", "thu", "fri"},
				StartDate: "2024-01-01", EndDate: "2027-12-31", ExceptedDates: []string{"2025-01-01", "2025-12-25"},
			}},
		},
		{
			name:       "added date becomes a single-day calendar next to the base",
			base:       &weekday,
			exceptions: []gtfsdb.CalendarDate{{ServiceID: "wk", Date: "20260704", ExceptionType: 1}},
			want: []models.OnDemandCalendar{
				{ID: "MC_wk", Days: []string{"mon", "tue", "wed", "thu", "fri"}, StartDate: "2024-01-01", EndDate: "2027-12-31", ExceptedDates: []string{}},
				{ID: "MC_wk_added_20260704", Days: []string{"sat"}, StartDate: "2026-07-04", EndDate: "2026-07-04", ExceptedDates: []string{}},
			},
		},
		{
			name:       "no calendar row emits only added-date calendars",
			base:       nil,
			exceptions: []gtfsdb.CalendarDate{{ServiceID: "ev", Date: "20260704", ExceptionType: 1}, {ServiceID: "ev", Date: "20260705", ExceptionType: 2}},
			want: []models.OnDemandCalendar{
				{ID: "MC_ev_added_20260704", Days: []string{"sat"}, StartDate: "2026-07-04", EndDate: "2026-07-04", ExceptedDates: []string{}},
			},
		},
		{
			name:       "calendar row with no service days counts as absent",
			base:       &datesOnly,
			exceptions: []gtfsdb.CalendarDate{{ServiceID: "ev", Date: "20260704", ExceptionType: 1}},
			want: []models.OnDemandCalendar{
				{ID: "MC_ev_added_20260704", Days: []string{"sat"}, StartDate: "2026-07-04", EndDate: "2026-07-04", ExceptedDates: []string{}},
			},
		},
		{
			name: "no rows at all yields nothing",
			base: nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceID := "wk"
			if tt.base != nil {
				serviceID = tt.base.ID
			} else if len(tt.exceptions) > 0 {
				serviceID = tt.exceptions[0].ServiceID
			}
			assert.Equal(t, tt.want, compileOnDemandCalendars("MC", serviceID, tt.base, tt.exceptions))
		})
	}
}
