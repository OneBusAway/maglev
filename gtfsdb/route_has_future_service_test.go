package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// TestRouteHasFutureService_BeyondTwoYearWindow guards the documented "bounded to
// 2 years past MAX(ref_date, today)" contract on RouteHasFutureService
// (query.sql): a route whose only service starts well beyond that horizon
// should not be reported as having future service. The fixture also plants a
// calendar_dates addition past the horizon so the "feed's maximum date"
// (max of cal.end_date and cd.date across the route) extends well beyond
// where the horizon caps; the expected 0 result therefore proves the
// horizon clamp is doing the work in both parts of the query -- Part 2
// rejects the calendar row because cal.start_date > horizon, and Part 1
// rejects the addition because cd.date > horizon -- rather than the query
// happening to run out of feed data.
func TestRouteHasFutureService_BeyondTwoYearWindow(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency-1",
		Name:     "Test Agency",
		Url:      "https://example.com",
		Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:       "route-1",
		AgencyID: "agency-1",
		Type:     3,
	})
	require.NoError(t, err)

	// Service starts far beyond the documented 2-year search horizon (ref_date
	// below is 2024-01-01; this calendar row doesn't start until 2099).
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "cal-far-future",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20990101",
		EndDate:   "20991231",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:        "trip-1",
		RouteID:   "route-1",
		ServiceID: "cal-far-future",
	})
	require.NoError(t, err)

	// Addition also lives past the horizon. Without this, a query that used
	// the feed's max date as an implicit upper bound could return 0 simply
	// because there's no data past ref_date -- ambiguous. With the addition,
	// the feed does extend past the horizon, so a 0 result specifically means
	// the horizon clamp on cd.date (Part 1) and cal.start_date (Part 2) held.
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID:     "cal-far-future",
		Date:          "20990601",
		ExceptionType: 1,
	})
	require.NoError(t, err)

	hasFuture, err := client.Queries.RouteHasFutureService(ctx, RouteHasFutureServiceParams{
		RouteID: "route-1",
		RefDate: "20240101",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), hasFuture,
		"service starting more than 2 years after ref_date must not be found; "+
			"the horizon-clamp branches in both parts of the query must reject "+
			"the calendar row and the past-horizon addition")
}

// TestRouteHasFutureService_WithinTwoYearWindow verifies the common case is
// unaffected by the horizon clamp: a route with service well within 2 years of
// ref_date must still be found.
func TestRouteHasFutureService_WithinTwoYearWindow(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency-1",
		Name:     "Test Agency",
		Url:      "https://example.com",
		Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:       "route-1",
		AgencyID: "agency-1",
		Type:     3,
	})
	require.NoError(t, err)

	// Service starts a few months after ref_date -- well within the 2-year horizon.
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "cal-near-future",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240601",
		EndDate:   "20241231",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:        "trip-1",
		RouteID:   "route-1",
		ServiceID: "cal-near-future",
	})
	require.NoError(t, err)

	hasFuture, err := client.Queries.RouteHasFutureService(ctx, RouteHasFutureServiceParams{
		RouteID: "route-1",
		RefDate: "20240101",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), hasFuture,
		"service starting within the 2-year horizon must still be found")
}

// TestRouteHasFutureService_ServiceEndedInPast verifies a route whose only
// service already ended before ref_date correctly reports no future service.
func TestRouteHasFutureService_ServiceEndedInPast(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency-1",
		Name:     "Test Agency",
		Url:      "https://example.com",
		Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:       "route-1",
		AgencyID: "agency-1",
		Type:     3,
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "cal-past",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20200101",
		EndDate:   "20201231",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:        "trip-1",
		RouteID:   "route-1",
		ServiceID: "cal-past",
	})
	require.NoError(t, err)

	hasFuture, err := client.Queries.RouteHasFutureService(ctx, RouteHasFutureServiceParams{
		RouteID: "route-1",
		RefDate: "20240101",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), hasFuture,
		"a route whose only service already ended must report no future service")
}

// TestRouteHasFutureService_AdditionBeyondCalendarEnd covers Part 1 of the
// RouteHasFutureService query directly: an exception_type=1 addition on a
// date past every calendar row's end_date should still be detected. The
// calendar row ends before ref_date so Part 2 (calendar-window overlap)
// contributes nothing -- if the query returns 1 here, it's exclusively
// because Part 1 caught the addition against the (ref_date, horizon]
// window without needing any calendar row to cover the date.
//
// The symmetric "exception_type=2 removal cancels an otherwise-valid
// regular service day, should return 0" case is intentionally NOT tested:
// the query's Part 2 is documented as a mild over-approximation that does
// not verify per-day exception cancellations. Handling that would require
// re-introducing the day-by-day enumeration the previous refactor
// removed; the benign miscategorization is called out in the query's
// doc comment (gtfsdb/query.sql above RouteHasFutureService).
func TestRouteHasFutureService_AdditionBeyondCalendarEnd(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID:       "agency-1",
		Name:     "Test Agency",
		Url:      "https://example.com",
		Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{
		ID:       "route-1",
		AgencyID: "agency-1",
		Type:     3,
	})
	require.NoError(t, err)

	// Calendar row ends well before ref_date, so Part 2 of the query
	// cannot contribute a hit for this service.
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID:        "cal-limited",
		Monday:    1,
		Tuesday:   1,
		Wednesday: 1,
		Thursday:  1,
		Friday:    1,
		Saturday:  1,
		Sunday:    1,
		StartDate: "20240101",
		EndDate:   "20240630",
	})
	require.NoError(t, err)

	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID:        "trip-1",
		RouteID:   "route-1",
		ServiceID: "cal-limited",
	})
	require.NoError(t, err)

	// Addition sits past the calendar's end_date but well within the
	// query's 2-year horizon relative to ref_date below.
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID:     "cal-limited",
		Date:          "20250801",
		ExceptionType: 1,
	})
	require.NoError(t, err)

	hasFuture, err := client.Queries.RouteHasFutureService(ctx, RouteHasFutureServiceParams{
		RouteID: "route-1",
		RefDate: "20250101",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), hasFuture,
		"an exception_type=1 addition past every calendar row's end_date must "+
			"be reported as future service via Part 1 of the query")
}
