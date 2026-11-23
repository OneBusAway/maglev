package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func TestGetActiveServiceIDsForDate(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	weekdays := CreateCalendarParams{Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1}
	calendars := []CreateCalendarParams{
		withCalendar(weekdays, "weekdays", "20250101", "20251231"),
		withCalendar(CreateCalendarParams{Sunday: 1}, "sundays", "20250101", "20251231"),
		withCalendar(weekdays, "ended", "20250101", "20250612"),
		withCalendar(weekdays, "starts-on-date", "20250613", "20251231"),
		withCalendar(weekdays, "ends-on-date", "20250101", "20250613"),
		withCalendar(weekdays, "removed", "20250101", "20251231"),
		withCalendar(weekdays, "added-while-running", "20250101", "20251231"),
		withCalendar(CreateCalendarParams{}, "added-only", "20250101", "20251231"),
	}
	for _, calendar := range calendars {
		_, err := client.Queries.CreateCalendar(ctx, calendar)
		require.NoError(t, err)
	}

	const (
		added   = 1
		removed = 2
	)
	exceptions := []CreateCalendarDateParams{
		{ServiceID: "removed", Date: "20250613", ExceptionType: removed},
		{ServiceID: "weekdays", Date: "20250612", ExceptionType: removed},
		{ServiceID: "added-only", Date: "20250613", ExceptionType: added},
		{ServiceID: "added-while-running", Date: "20250613", ExceptionType: added},
		{ServiceID: "added-only", Date: "20250615", ExceptionType: added},
	}
	for _, exception := range exceptions {
		_, err := client.Queries.CreateCalendarDate(ctx, exception)
		require.NoError(t, err)
	}

	tests := []struct {
		name string
		date string
		want []string
	}{
		{
			name: "friday with range bounds and exceptions",
			date: "20250613",
			want: []string{"added-only", "added-while-running", "ends-on-date", "starts-on-date", "weekdays"},
		},
		{
			name: "sunday",
			date: "20250615",
			want: []string{"added-only", "sundays"},
		},
		{
			name: "removal on another date only removes that date",
			date: "20250612",
			want: []string{"added-while-running", "ended", "ends-on-date", "removed"},
		},
		{
			name: "outside every calendar",
			date: "20260105",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := client.Queries.GetActiveServiceIDsForDate(ctx, tc.date)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func withCalendar(days CreateCalendarParams, id, startDate, endDate string) CreateCalendarParams {
	days.ID = id
	days.StartDate = startDate
	days.EndDate = endDate
	return days
}
