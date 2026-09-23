package servicedate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/servicedate"
)

func losAngeles(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

// The expected values are gtfs-modules' ServiceDateTest.testGetAsDateWithTimezoneB and C.
func TestStart_MatchesJavaServiceDate(t *testing.T) {
	la := losAngeles(t)

	for _, tc := range []struct {
		name string
		date servicedate.Date
		want time.Time
	}{
		{name: "ordinary day, 00:00 PDT", date: servicedate.New(2010, time.June, 15), want: time.Date(2010, time.June, 15, 7, 0, 0, 0, time.UTC)},
		{name: "spring forward, 23:00 PST the day before", date: servicedate.New(2010, time.March, 14), want: time.Date(2010, time.March, 14, 7, 0, 0, 0, time.UTC)},
		{name: "fall back, 01:00 PDT", date: servicedate.New(2010, time.November, 7), want: time.Date(2010, time.November, 7, 8, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, tc.want.Equal(tc.date.Start(la)), "got %s", tc.date.Start(la))
		})
	}
}

func TestStart_HoldsStopTimesAtLocalClockTime(t *testing.T) {
	la := losAngeles(t)

	for _, date := range []servicedate.Date{
		servicedate.New(2026, time.November, 2),
		servicedate.New(2026, time.November, 1),
		servicedate.New(2026, time.March, 8),
	} {
		t.Run(date.String(), func(t *testing.T) {
			stopTime := date.Start(la).Add(8 * time.Hour).In(la)

			assert.Equal(t, 8, stopTime.Hour())
			assert.Equal(t, date, servicedate.Of(stopTime))
		})
	}
}

func TestFromInstant_RecoversTheDateFromMidnightStartOrAnyTimeThatDay(t *testing.T) {
	la := losAngeles(t)

	for _, date := range []servicedate.Date{
		servicedate.New(2026, time.November, 2),
		servicedate.New(2026, time.November, 1),
		servicedate.New(2026, time.March, 8),
	} {
		t.Run(date.String(), func(t *testing.T) {
			assert.Equal(t, date, servicedate.FromInstant(date.Start(la), la))
			assert.Equal(t, date, servicedate.FromInstant(date.Start(la).UTC(), la))
			assert.Equal(t, date, servicedate.FromInstant(date.Midnight(la), la))
			assert.Equal(t, date, servicedate.FromInstant(date.Midnight(la).Add(15*time.Hour), la))
		})
	}
}

func TestDate_CalendarFields(t *testing.T) {
	springForward := servicedate.New(2026, time.March, 8)

	assert.Equal(t, "20260308", springForward.String())
	assert.Equal(t, time.Sunday, springForward.Weekday())
	assert.Equal(t, servicedate.New(2026, time.March, 7), springForward.AddDays(-1))
	assert.Equal(t, servicedate.New(2026, time.February, 28), servicedate.New(2026, time.March, 1).AddDays(-1))
	assert.Equal(t, servicedate.New(2027, time.January, 1), servicedate.New(2026, time.December, 31).AddDays(1))
}
