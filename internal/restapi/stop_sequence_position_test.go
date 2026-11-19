package restapi

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

// stopSequence is the stop's index into its trip's ordered stop list, not the
// feed's raw stop_sequence. These tests pin that for both arrivals endpoints,
// which build their arrivals from separate loops and so need separate
// assertions. The loop fixture they share lives in loop_trip_fixture_test.go.
//
// They are in their own file on purpose. Appending them to the two handler test
// files put them at the same line as whatever main had appended, which turned
// every catch-up merge into a conflict over a hunk where neither side disagreed.

// TestArrivalsAndDeparturesForStopHandler_StopSequenceIsPositionInTrip pins
// stopSequence to the stop's index in the trip's ordered stop list, which is how
// testdata/openapi.yml defines the field and what the singular
// arrival-and-departure-for-stop endpoint already returns.
//
// The loop fixture is what distinguishes a position from the raw feed value:
// its stop_sequence counts by ten from ten, and its hub stop is visited twice,
// so reporting the raw stop_sequence, the raw value minus one, or a position
// keyed by stop rather than by stop_sequence each fail here.
func TestArrivalsAndDeparturesForStopHandler_StopSequenceIsPositionInTrip(t *testing.T) {
	api := createTestApiWithLoopTripData(t)

	tests := []struct {
		name          string
		stopID        string
		wantPositions []int
	}{
		{
			name:          "first stop of the trip",
			stopID:        loopStopOriginID,
			wantPositions: []int{0},
		},
		{
			name:          "stop the trip visits twice",
			stopID:        loopStopHubID,
			wantPositions: []int{1, 3},
		},
		{
			name:          "last stop of the trip",
			stopID:        loopStopEndID,
			wantPositions: []int{4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopID := utils.FormCombinedID(loopAgencyID, tt.stopID)
			_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
				arrivalsAndDeparturesURL(stopID, url.Values{
					"minutesBefore": {"60"}, "minutesAfter": {"240"},
				}))

			arrivals := model.Data.Entry.ArrivalsAndDepartures
			require.Len(t, arrivals, len(tt.wantPositions),
				"stop %s is served %d times in the window", tt.stopID, len(tt.wantPositions))

			seen := assertLoopTripPositions(t, arrivals)
			for _, want := range tt.wantPositions {
				assert.True(t, seen[want],
					"stop %s should be reported at position %d of the trip", tt.stopID, want)
			}
		})
	}
}

// TestArrivalsAndDeparturesForStopHandler_StopSequenceOnZeroBasedFeed covers the
// symptom reported in #1577 on a real feed: RABA numbers stop_sequence from 0,
// so deriving stopSequence by subtracting one reported -1 for the first stop of
// a trip. Stop 1030 is the first stop of 25 RABA trips.
func TestArrivalsAndDeparturesForStopHandler_StopSequenceOnZeroBasedFeed(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(arrivalsTestClock))
	agencyID := mustGetAgencies(t, api)[0].ID
	ctx := context.Background()

	const firstStopCode = "1030"
	stopID := utils.FormCombinedID(agencyID, firstStopCode)
	_, model := callAPIHandler[ArrivalsAndDeparturesResponse](t, api,
		arrivalsAndDeparturesURL(stopID, url.Values{
			"minutesBefore": {"1440"}, "minutesAfter": {"1440"},
		}))

	arrivals := model.Data.Entry.ArrivalsAndDepartures
	require.NotEmpty(t, arrivals, "stop %s should have arrivals in a 48h window", firstStopCode)

	servesFirstStopOfATrip := false
	for _, arrival := range arrivals {
		tripID, err := utils.ExtractCodeID(arrival.TripID)
		require.NoError(t, err)

		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		require.NoError(t, err)

		var positions []int
		for position, stopTime := range stopTimes {
			if stopTime.StopID == firstStopCode {
				positions = append(positions, position)
			}
		}
		// No RABA trip visits 1030 twice, so its one position is unambiguous.
		// Repeated visits are covered by the loop fixture instead.
		require.Len(t, positions, 1,
			"stop %s should be visited exactly once on trip %s", firstStopCode, tripID)
		wantPosition := positions[0]

		assert.Equal(t, wantPosition, arrival.StopSequence,
			"trip %s: stopSequence should be the stop's position in the trip", tripID)
		assert.Less(t, arrival.StopSequence, arrival.TotalStopsInTrip,
			"trip %s: stopSequence must fall inside totalStopsInTrip", tripID)

		servesFirstStopOfATrip = servesFirstStopOfATrip || wantPosition == 0
	}

	assert.True(t, servesFirstStopOfATrip,
		"stop %s should start at least one trip, the case that reported -1", firstStopCode)
}

// TestArrivalsAndDeparturesForLocation_StopSequenceIsPositionInTrip pins the
// for-location pipeline to the same stopSequence semantics as
// arrivals-and-departures-for-stop. The two endpoints build arrivals from
// separate loops over the trip stop index, so each needs its own assertion.
func TestArrivalsAndDeparturesForLocation_StopSequenceIsPositionInTrip(t *testing.T) {
	api := createTestApiWithLoopTripData(t)

	resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api,
		arrivalsForLocationURL(loopFixtureCenter, url.Values{
			"minutesBefore": {"60"}, "minutesAfter": {"240"},
		}))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	arrivals := model.Data.Entry.ArrivalsAndDepartures
	require.Len(t, arrivals, loopTripStopCount(),
		"every stop time of loop-trip is inside the radius and the window")

	seen := assertLoopTripPositions(t, arrivals)
	for position := 0; position < loopTripStopCount(); position++ {
		assert.True(t, seen[position],
			"the trip's stop at position %d should be reported at that position", position)
	}
}
