package restapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// TestServiceDayWindows_PreviousDayDate verifies that the previous-day window's
// date is midnight of yesterday, not today — a trip only found via that window
// (a past-midnight block) must have its schedule position extrapolated against
// yesterday's service day.
func TestServiceDayWindows_PreviousDayDate(t *testing.T) {
	// tfr-svc runs every day of the week, so both the current-day and
	// previous-day windows are populated for any query time.
	api := createTestApiWithTripsForRouteFixture(t, clock.NewMockClock(tripsForRouteTestClock))

	windows, err := api.serviceDayWindows(context.Background(), tripsForRouteTestClock)
	require.NoError(t, err)
	require.Len(t, windows, 2, "tfr-svc runs every day, so a previous-day window should be present")

	wantToday := time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
	wantYesterday := time.Date(2025, 6, 11, 0, 0, 0, 0, time.UTC)

	assert.True(t, windows[0].date.Equal(wantToday), "windows[0].date should be midnight of the query date, got %v", windows[0].date)
	assert.True(t, windows[1].date.Equal(wantYesterday), "windows[1].date should be midnight of the day before, got %v", windows[1].date)
}
