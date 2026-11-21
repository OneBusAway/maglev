package gtfsdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

func spanYYYYMMDD(t *testing.T, v interface{}) string {
	t.Helper()
	switch d := v.(type) {
	case nil:
		return ""
	case string:
		return d
	case []byte:
		return string(d)
	default:
		t.Fatalf("unexpected span date type %T", v)
		return ""
	}
}

func TestGetRouteServiceSpan_CalendarBounds(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID: "agency-1", Name: "Test", Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{ID: "route-1", AgencyID: "agency-1", Type: 3})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID: "cal-1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		StartDate: "20240101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID: "trip-1", RouteID: "route-1", ServiceID: "cal-1",
	})
	require.NoError(t, err)

	span, err := client.Queries.GetRouteServiceSpan(ctx, "route-1")
	require.NoError(t, err)
	assert.Equal(t, "20240101", spanYYYYMMDD(t, span.FirstDate))
	assert.Equal(t, "20251231", spanYYYYMMDD(t, span.LastDate))
}

func TestGetRouteServiceSpan_Type1ExtendsAllZeroExcluded(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID: "agency-1", Name: "Test", Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{ID: "route-1", AgencyID: "agency-1", Type: 3})
	require.NoError(t, err)

	// Wide all-zero weekday calendar must not widen the span by itself.
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID: "cal-zero", StartDate: "20200101", EndDate: "20301231",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID: "trip-1", RouteID: "route-1", ServiceID: "cal-zero",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID: "cal-zero", Date: "20240615", ExceptionType: 1,
	})
	require.NoError(t, err)

	span, err := client.Queries.GetRouteServiceSpan(ctx, "route-1")
	require.NoError(t, err)
	assert.Equal(t, "20240615", spanYYYYMMDD(t, span.FirstDate))
	assert.Equal(t, "20240615", spanYYYYMMDD(t, span.LastDate))
}

func TestGetRouteServiceSpan_Type1ExtendsPastCalendarEnd(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID: "agency-1", Name: "Test", Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{ID: "route-1", AgencyID: "agency-1", Type: 3})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID: "cal-1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		StartDate: "20240101", EndDate: "20240630",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID: "trip-1", RouteID: "route-1", ServiceID: "cal-1",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID: "cal-1", Date: "20241225", ExceptionType: 1,
	})
	require.NoError(t, err)

	span, err := client.Queries.GetRouteServiceSpan(ctx, "route-1")
	require.NoError(t, err)
	assert.Equal(t, "20240101", spanYYYYMMDD(t, span.FirstDate))
	assert.Equal(t, "20241225", spanYYYYMMDD(t, span.LastDate),
		"type-1 addition past calendar end_date must extend the planned span")
}

func TestGetRouteServiceSpan_Type2DoesNotShrinkSpan(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID: "agency-1", Name: "Test", Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{ID: "route-1", AgencyID: "agency-1", Type: 3})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendar(ctx, CreateCalendarParams{
		ID: "cal-1", Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1, Friday: 1,
		StartDate: "20240101", EndDate: "20251231",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateTrip(ctx, CreateTripParams{
		ID: "trip-1", RouteID: "route-1", ServiceID: "cal-1",
	})
	require.NoError(t, err)
	// Removals across the whole span must not shrink planned [first,last].
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID: "cal-1", Date: "20240101", ExceptionType: 2,
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateCalendarDate(ctx, CreateCalendarDateParams{
		ServiceID: "cal-1", Date: "20251231", ExceptionType: 2,
	})
	require.NoError(t, err)

	span, err := client.Queries.GetRouteServiceSpan(ctx, "route-1")
	require.NoError(t, err)
	assert.Equal(t, "20240101", spanYYYYMMDD(t, span.FirstDate))
	assert.Equal(t, "20251231", spanYYYYMMDD(t, span.LastDate))
}

func TestGetRouteServiceSpan_EmptyRoute(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	_, err = client.Queries.CreateAgency(ctx, CreateAgencyParams{
		ID: "agency-1", Name: "Test", Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = client.Queries.CreateRoute(ctx, CreateRouteParams{ID: "route-1", AgencyID: "agency-1", Type: 3})
	require.NoError(t, err)

	span, err := client.Queries.GetRouteServiceSpan(ctx, "route-1")
	require.NoError(t, err)
	assert.Empty(t, spanYYYYMMDD(t, span.FirstDate))
	assert.Empty(t, spanYYYYMMDD(t, span.LastDate))
}
