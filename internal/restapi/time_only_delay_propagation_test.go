package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/servicedate"
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
	targetSequence := uint32(2)
	skippedTarget := gtfs.StopTimeUpdate{StopSequence: &targetSequence, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}
	skippedTargetWithEvents := skippedTarget
	skippedTargetWithEvents.Departure = &gtfs.StopTimeEvent{Time: &departureAtFirst}

	tests := []struct {
		name         string
		event        *gtfs.StopTimeEvent
		arrivalOnly  bool
		wantDelay    time.Duration
		idOnly       bool
		target       *gtfs.StopTimeUpdate
		tripDelay    *time.Duration
		noPrediction bool
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
		{name: "id-only-time", event: &gtfs.StopTimeEvent{Time: &departureAtFirst}, idOnly: true, wantDelay: delay},
		{name: "id-only-delay", event: &gtfs.StopTimeEvent{Delay: &delay}, idOnly: true, wantDelay: delay},
		{name: "skipped-target-after-time", event: &gtfs.StopTimeEvent{Time: &departureAtFirst}, target: &skippedTarget, noPrediction: true},
		{name: "skipped-target-after-delay", event: &gtfs.StopTimeEvent{Delay: &delay}, target: &skippedTarget, noPrediction: true},
		{name: "skipped-target-with-events-and-trip-delay", event: &gtfs.StopTimeEvent{Time: &departureAtFirst}, target: &skippedTargetWithEvents, tripDelay: &delay, noPrediction: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := day.Add(8*time.Hour + 10*time.Minute + tt.wantDelay)
			update := gtfs.StopTimeUpdate{StopID: stringPointerForPropagationTest(first), StopSequence: &sequence, Departure: tt.event}
			if tt.arrivalOnly {
				update.Arrival, update.Departure = tt.event, nil
			}
			if tt.idOnly {
				update.StopSequence = nil
			}
			updates := []gtfs.StopTimeUpdate{update}
			if tt.target != nil {
				updates = append(updates, *tt.target)
			}
			api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{
				ID:              gtfs.TripID{ID: trip},
				StopTimeUpdates: updates,
				Delay:           tt.tripDelay,
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
			for endpoint, arrival := range map[string]models.ArrivalAndDeparture{"plural": plural.Data.Entry.ArrivalsAndDepartures[0], "singular": singular.Data.Entry} {
				assert.Equal(t, !tt.noPrediction, arrival.Predicted, endpoint)
				if tt.noPrediction {
					assert.True(t, arrival.PredictedArrivalTime.IsZero(), endpoint)
					assert.True(t, arrival.PredictedDepartureTime.IsZero(), endpoint)
				} else {
					assert.Equal(t, want.UnixMilli(), arrival.PredictedArrivalTime.UnixMilli(), endpoint)
					assert.Equal(t, want.UnixMilli(), arrival.PredictedDepartureTime.UnixMilli(), endpoint)
				}
			}
		})
	}
}

func stringPointerForPropagationTest(value string) *string {
	return &value
}

func TestGetPredictedTimes_PropagationRules(t *testing.T) {
	day := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(day.Add(8*time.Hour+2*time.Minute)))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)
	target, _, trip, _ := setupDelayPropTestData(t, api, 3)
	first, middle := "propagation-first", "propagation-middle"
	for i, stop := range []string{first, middle} {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateStop(t.Context(), gtfsdb.CreateStopParams{
			ID: stop, Name: nulls.String(stop), Lat: 47, Lon: -122,
		})
		require.NoError(t, err)
		offset := 7*time.Hour + time.Duration(40+i*10)*time.Minute
		_, err = api.GtfsManager.GtfsDB.Queries.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
			TripID: trip, StopID: stop, StopSequence: int64(i + 1),
			ArrivalTime: int64(offset), DepartureTime: int64(offset + 2*time.Minute),
		})
		require.NoError(t, err)
	}
	// The middle stop is visited again after the target. Its ID alone cannot
	// identify whether an update is upstream or downstream of the target.
	_, err := api.GtfsManager.GtfsDB.Queries.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
		TripID: trip, StopID: middle, StopSequence: 4,
		ArrivalTime: int64(8*time.Hour + 20*time.Minute), DepartureTime: int64(8*time.Hour + 22*time.Minute),
	})
	require.NoError(t, err)

	seq1, seq2, seq3, seq4 := uint32(1), uint32(2), uint32(3), uint32(4)
	delay, olderDelay, zero := 5*time.Minute, 9*time.Minute, time.Duration(0)
	uncertainty := int32(30)
	arrivalTime := day.Add(7*time.Hour + 45*time.Minute)
	departureTime := day.Add(7*time.Hour + 47*time.Minute)
	closestTime := day.Add(7*time.Hour + 53*time.Minute)
	earlyTime := day.Add(7*time.Hour + 40*time.Minute)
	prior := gtfs.StopTimeUpdate{StopSequence: &seq1, Departure: &gtfs.StopTimeEvent{Time: &departureTime}}
	skipped := gtfs.StopTimeUpdate{StopSequence: &seq2, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}
	noData := gtfs.StopTimeUpdate{StopSequence: &seq2, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}
	tests := []struct {
		name      string
		updates   []gtfs.StopTimeUpdate
		tripDelay *time.Duration
		wantDelay *time.Duration // nil means no prediction, including zero timestamps
	}{
		{"time-only-departure", []gtfs.StopTimeUpdate{prior}, nil, &delay},
		{"time-only-arrival", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Arrival: &gtfs.StopTimeEvent{Time: &arrivalTime}}}, nil, &delay},
		{"explicit-delay", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, &delay},
		{"zero-delay-is-a-prediction", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Departure: &gtfs.StopTimeEvent{Delay: &zero}}}, nil, &zero},
		{"early-departure", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Departure: &gtfs.StopTimeEvent{Time: &earlyTime}}}, nil, ptrDuration(-2 * time.Minute)},
		{"time-overrides-delay", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Departure: &gtfs.StopTimeEvent{Time: &departureTime, Delay: &olderDelay}}}, nil, &delay},
		{"departure-overrides-arrival", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Arrival: &gtfs.StopTimeEvent{Delay: &olderDelay}, Departure: &gtfs.StopTimeEvent{Time: &departureTime}}}, nil, &delay},
		{"empty-departure-uses-arrival", []gtfs.StopTimeUpdate{{StopSequence: &seq1, Arrival: &gtfs.StopTimeEvent{Time: &arrivalTime}, Departure: &gtfs.StopTimeEvent{}}}, nil, &delay},
		{"closest-prior-wins", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq2, Departure: &gtfs.StopTimeEvent{Time: &closestTime}}}, nil, ptrDuration(time.Minute)},
		{"propagate-past-skipped", []gtfs.StopTimeUpdate{prior, skipped}, nil, &delay},
		{"skipped-events-do-not-replace-earlier-delay", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq2, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED, Departure: &gtfs.StopTimeEvent{Delay: &olderDelay}}}, nil, &delay},
		{"skipped-target", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq3, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}}, nil, nil},
		{"skipped-target-with-events", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq3, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, nil},
		{"skipped-target-with-trip-delay", []gtfs.StopTimeUpdate{{StopSequence: &seq3, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}}, &delay, nil},
		{"skipped-target-by-id", []gtfs.StopTimeUpdate{prior, {StopID: &target, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SKIPPED}}, nil, nil},
		{"no-data-target", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq3, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}}, &delay, nil},
		{"no-data-boundary", []gtfs.StopTimeUpdate{prior, noData}, &delay, nil},
		{"skipped-does-not-clear-no-data", []gtfs.StopTimeUpdate{{StopSequence: &seq1, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}, skipped}, &delay, nil},
		{"no-data-target-with-malformed-events", []gtfs.StopTimeUpdate{{StopSequence: &seq3, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA, Arrival: &gtfs.StopTimeEvent{Delay: &delay}}}, &delay, nil},
		{"no-data-with-malformed-events", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq2, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, &delay, nil},
		{"no-data-boundary-by-id", []gtfs.StopTimeUpdate{{StopID: &first, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}}, &delay, nil},
		{"prediction-resumes-at-target-after-no-data", []gtfs.StopTimeUpdate{noData, {StopSequence: &seq3, Arrival: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, &delay},
		{"prediction-resumes-before-target-after-no-data", []gtfs.StopTimeUpdate{{StopSequence: &seq1, ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_NO_DATA}, {StopSequence: &seq2, Departure: &gtfs.StopTimeEvent{Time: &closestTime}}}, nil, ptrDuration(time.Minute)},
		{"id-only-time", []gtfs.StopTimeUpdate{{StopID: &first, Departure: &gtfs.StopTimeEvent{Time: &departureTime}}}, nil, &delay},
		{"id-only-delay", []gtfs.StopTimeUpdate{{StopID: &first, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, &delay},
		{"ambiguous-prior-id", []gtfs.StopTimeUpdate{{StopID: &middle, Departure: &gtfs.StopTimeEvent{Time: &closestTime}}}, nil, nil},
		{"future-update-does-not-propagate-backward", []gtfs.StopTimeUpdate{{StopSequence: &seq4, StopID: &middle, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, nil},
		{"drifted-sequence-resolves-by-id", []gtfs.StopTimeUpdate{{StopSequence: &seq4, StopID: &first, Departure: &gtfs.StopTimeEvent{Time: &departureTime}}}, nil, &delay},
		{"empty-target-does-not-invent-prediction", []gtfs.StopTimeUpdate{{StopSequence: &seq3}}, &delay, nil},
		{"empty-target-interrupts-earlier-delay", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq3}}, nil, nil},
		{"uncertainty-only-target", []gtfs.StopTimeUpdate{{StopSequence: &seq3, Arrival: &gtfs.StopTimeEvent{Uncertainty: &uncertainty}}}, nil, nil},
		{"empty-prior-does-not-fall-back-to-trip-delay", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq2}}, &delay, nil},
		{"trip-delay-with-skipped-prior", []gtfs.StopTimeUpdate{skipped}, &delay, &delay},
		{"trip-delay-only", nil, &delay, &delay},
		{"target-event-overrides-prior-and-trip-delay", []gtfs.StopTimeUpdate{prior, {StopSequence: &seq3, Departure: &gtfs.StopTimeEvent{Delay: &zero}}}, &olderDelay, &zero},
		{"sequenced-target-overrides-id-fallback", []gtfs.StopTimeUpdate{{StopID: &target, Arrival: &gtfs.StopTimeEvent{Delay: &olderDelay}}, {StopSequence: &seq3, Arrival: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, &delay},
		{"unknown-stop-id-is-ignored", []gtfs.StopTimeUpdate{{StopID: stringPointerForPropagationTest("not-on-this-trip"), Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, nil},
		{"unidentified-update-is-ignored", []gtfs.StopTimeUpdate{{Departure: &gtfs.StopTimeEvent{Delay: &delay}}}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{ID: gtfs.TripID{ID: trip}, Delay: tt.tripDelay, StopTimeUpdates: tt.updates}})
			scheduledArrival, scheduledDeparture := day.Add(8*time.Hour), day.Add(8*time.Hour+5*time.Minute)
			arrival, departure, predicted := api.getPredictedTimes(t.Context(), trip, target, 3, scheduledArrival, scheduledDeparture)
			if tt.wantDelay == nil {
				assert.False(t, predicted)
				assert.True(t, arrival.IsZero())
				assert.True(t, departure.IsZero())
				return
			}
			require.True(t, predicted)
			assert.Equal(t, scheduledArrival.Add(*tt.wantDelay), arrival)
			assert.Equal(t, scheduledDeparture.Add(*tt.wantDelay), departure)
		})
	}
	// Querying either visit of a repeated stop must not attach an ID-only event
	// to that visit when there is no sequence to distinguish them.
	api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{ID: gtfs.TripID{ID: trip}, StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &middle, Departure: &gtfs.StopTimeEvent{Delay: &delay}}}}})
	for _, sequence := range []int64{2, 4} {
		arrival, departure, predicted := api.getPredictedTimes(t.Context(), trip, middle, sequence, day, day)
		assert.False(t, predicted)
		assert.True(t, arrival.IsZero())
		assert.True(t, departure.IsZero())
	}

	// Stop times beyond 24 hours belong to the same service day. Deriving the
	// source delay must retain that anchor, including on DST transition dates.
	_, err = api.GtfsManager.GtfsDB.Queries.CreateStopTime(t.Context(), gtfsdb.CreateStopTimeParams{
		TripID: trip, StopID: middle, StopSequence: 5,
		ArrivalTime: int64(32 * time.Hour), DepartureTime: int64(32*time.Hour + 5*time.Minute),
	})
	require.NoError(t, err)
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for _, date := range []servicedate.Date{servicedate.New(2025, 3, 9), servicedate.New(2025, 11, 2)} {
		t.Run("overnight-"+date.String(), func(t *testing.T) {
			start := date.Start(location)
			actualDeparture := start.Add(7*time.Hour + 47*time.Minute)
			api.GtfsManager.SetRealTimeTripsForTest([]gtfs.Trip{{ID: gtfs.TripID{ID: trip}, StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: &first, Departure: &gtfs.StopTimeEvent{Time: &actualDeparture}}}}})
			arrival, departure, predicted := api.getPredictedTimes(t.Context(), trip, middle, 5, start.Add(32*time.Hour), start.Add(32*time.Hour+5*time.Minute))
			require.True(t, predicted)
			assert.Equal(t, start.Add(32*time.Hour+5*time.Minute), arrival)
			assert.Equal(t, start.Add(32*time.Hour+10*time.Minute), departure)
		})
	}
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
