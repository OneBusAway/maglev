package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/nulls"
)

func TestArrivalDelayPropagationFromAbsoluteTime(t *testing.T) {
	day := time.Date(2011, 7, 1, 0, 0, 0, 0, time.UTC)
	queryTime := day.Add(8*time.Hour + time.Minute)

	api := createTestApiWithClock(t, clock.NewMockClock(queryTime))
	defer api.Shutdown()
	defer api.GtfsManager.MockResetRealTimeData()

	ctx := context.Background()
	q := api.GtfsManager.GtfsDB.Queries

	const (
		agency  = "TimeOnlyPropagationAgency"
		route   = "TimeOnlyPropagationRoute"
		trip    = "TimeOnlyPropagationTrip"
		service = "TimeOnlyPropagationService"
		first   = "TimeOnlyPropagationFirst"
		second  = "TimeOnlyPropagationSecond"
	)

	_, err := q.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID: agency, Name: agency,
		Url: "https://example.com", Timezone: "UTC",
	})
	require.NoError(t, err)

	_, err = q.CreateRoute(ctx, gtfsdb.CreateRouteParams{
		ID: route, AgencyID: agency, Type: 3,
	})
	require.NoError(t, err)

	_, err = q.CreateCalendar(ctx, gtfsdb.CreateCalendarParams{
		ID:     service,
		Monday: 1, Tuesday: 1, Wednesday: 1, Thursday: 1,
		Friday: 1, Saturday: 1, Sunday: 1,
		StartDate: "20000101", EndDate: "20301231",
	})
	require.NoError(t, err)

	_, err = q.CreateTrip(ctx, gtfsdb.CreateTripParams{
		ID: trip, RouteID: route, ServiceID: service,
	})
	require.NoError(t, err)

	for i, stop := range []string{first, second} {
		_, err = q.CreateStop(ctx, gtfsdb.CreateStopParams{
			ID: stop, Name: nulls.String(stop),
			Lat: 47.60, Lon: -122.33,
		})
		require.NoError(t, err)

		scheduledOffset := 8*time.Hour + time.Duration(i)*10*time.Minute
		_, err = q.CreateStopTime(ctx, gtfsdb.CreateStopTimeParams{
			TripID: trip, StopID: stop, StopSequence: int64(i + 1),
			ArrivalTime:   int64(scheduledOffset),
			DepartureTime: int64(scheduledOffset),
		})
		require.NoError(t, err)
	}

	// A is scheduled at 08:00 but departs at 08:05.
	// B is scheduled at 08:10, so its prediction should be 08:15.
	departureAtFirst := day.Add(8*time.Hour + 5*time.Minute)
	delay := 5 * time.Minute
	earlyDeparture := day.Add(8*time.Hour - 2*time.Minute)
	conflictingDelay := time.Minute
	sequence := uint32(1)

	tests := []struct {
		name        string
		event       *gtfs.StopTimeEvent
		arrivalOnly bool
		wantDelay   time.Duration
	}{
		{
			name:      "absolute-time-only",
			event:     &gtfs.StopTimeEvent{Time: &departureAtFirst},
			wantDelay: delay,
		},
		{
			name:      "equivalent-delay-control",
			event:     &gtfs.StopTimeEvent{Delay: &delay},
			wantDelay: delay,
		},
		{name: "arrival-time-only", event: &gtfs.StopTimeEvent{Time: &departureAtFirst}, arrivalOnly: true, wantDelay: delay},
		{name: "time-overrides-delay", event: &gtfs.StopTimeEvent{Time: &departureAtFirst, Delay: &conflictingDelay}, wantDelay: delay},
		{name: "early-departure", event: &gtfs.StopTimeEvent{Time: &earlyDeparture}, wantDelay: -2 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := day.Add(8*time.Hour + 10*time.Minute + tt.wantDelay)
			update := gtfs.StopTimeUpdate{StopID: stringPointerForPropagationTest(first), StopSequence: &sequence, Departure: tt.event}
			if tt.arrivalOnly {
				update.Arrival, update.Departure = tt.event, nil
			}
			api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{
				ID:              gtfs.TripID{ID: trip},
				StopTimeUpdates: []gtfs.StopTimeUpdate{update},
			}})

			pluralURL := arrivalsAndDeparturesURL(
				agency+"_"+second,
				url.Values{
					"key":           {"org.onebusaway.iphone"},
					"minutesBefore": {"10"},
					"minutesAfter":  {"30"},
				},
			)
			response, plural := callAPIHandler[ArrivalsAndDeparturesResponse](
				t, api, pluralURL,
			)
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Len(t, plural.Data.Entry.ArrivalsAndDepartures, 1)

			arrival := plural.Data.Entry.ArrivalsAndDepartures[0]
			assert.True(t, arrival.Predicted)
			assert.Equal(t, want.UnixMilli(),
				arrival.PredictedArrivalTime.UnixMilli(),
				"plural endpoint should propagate the five-minute delay")
			assert.Equal(t, want.UnixMilli(), arrival.PredictedDepartureTime.UnixMilli())

			params := url.Values{
				"key":         {"org.onebusaway.iphone"},
				"tripId":      {agency + "_" + trip},
				"serviceDate": {fmt.Sprint(day.UnixMilli())},
				"time":        {fmt.Sprint(queryTime.UnixMilli())},
			}
			singularURL := "/api/where/arrival-and-departure-for-stop/" +
				agency + "_" + second + ".json?" + params.Encode()

			response, singular := callAPIHandler[ArrivalAndDepartureResponse](
				t, api, singularURL,
			)
			require.Equal(t, http.StatusOK, response.StatusCode)
			assert.True(t, singular.Data.Entry.Predicted)
			assert.Equal(t, want.UnixMilli(),
				singular.Data.Entry.PredictedArrivalTime.UnixMilli(),
				"singular endpoint should propagate the five-minute delay")
			assert.Equal(t, want.UnixMilli(), singular.Data.Entry.PredictedDepartureTime.UnixMilli())
		})
	}
}

func stringPointerForPropagationTest(value string) *string {
	return &value
}

func TestGetPredictedTimes_TimeOnlyWithoutStaticSchedule(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	now := time.Date(2011, 7, 1, 8, 5, 0, 0, time.UTC)
	sequence := uint32(1)
	api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{
		ID: gtfs.TripID{ID: "time-only-missing-static-schedule"},
		StopTimeUpdates: []gtfs.StopTimeUpdate{{
			StopSequence: &sequence,
			Departure:    &gtfs.StopTimeEvent{Time: &now},
		}},
	}})
	arrival, departure, predicted := api.getPredictedTimes(t.Context(),
		"time-only-missing-static-schedule", "missing-stop", 2, now, now)
	assert.False(t, predicted, "a missing schedule must not imply an on-time prediction")
	assert.True(t, arrival.IsZero())
	assert.True(t, departure.IsZero())
}
