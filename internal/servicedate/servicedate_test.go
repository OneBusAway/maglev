package servicedate_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/servicedate"
)

// A stop time of 08:00:00 has to land at 08:00 local on every service day, including
// the two days a year local midnight is not twelve hours before noon.
func TestStart_HoldsStopTimesAtLocalClockTime(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	const eightAM = 8 * time.Hour

	for _, tc := range []struct {
		name       string
		year       int
		month      time.Month
		day        int
		wantOffset string
	}{
		{name: "ordinary day", year: 2026, month: time.November, day: 2, wantOffset: "PST"},
		{name: "fall back", year: 2026, month: time.November, day: 1, wantOffset: "PST"},
		{name: "spring forward", year: 2026, month: time.March, day: 8, wantOffset: "PDT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stopTime := servicedate.Start(tc.year, tc.month, tc.day, losAngeles).Add(eightAM)

			zone, _ := stopTime.Zone()
			assert.Equal(t, 8, stopTime.In(losAngeles).Hour(), "08:00:00 must read as 08:00 local")
			assert.Equal(t, tc.day, stopTime.In(losAngeles).Day(), "and stay on its own service day")
			assert.Equal(t, tc.wantOffset, zone)
		})
	}
}

func TestStart_IsLocalMidnightOnDaysWithoutATransition(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	start := servicedate.Start(2026, time.June, 15, losAngeles)

	assert.Equal(t, time.Date(2026, time.June, 15, 0, 0, 0, 0, losAngeles), start)
}

func TestStartOf_UsesTheDateInItsOwnLocation(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// 15:00 UTC on the fall-back day is 08:00 in Los Angeles.
	instant := time.Date(2026, time.November, 1, 15, 0, 0, 0, time.UTC).In(losAngeles)

	assert.Equal(t, servicedate.Start(2026, time.November, 1, losAngeles), servicedate.StartOf(instant))
}
