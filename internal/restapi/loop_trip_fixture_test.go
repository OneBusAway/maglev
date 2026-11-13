package restapi

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// Loop fixture identifiers. The fixture exists because no RABA trip exercises
// the two properties stopSequence depends on: RABA numbers every trip 0..N-1,
// so a stop's position and its raw stop_sequence are the same number there, and
// no RABA trip visits the same stop twice.
const (
	loopAgencyID  = "loop-agency"
	loopRouteID   = "loop-route"
	loopServiceID = "loop-service"
	loopTripID    = "loop-trip"

	loopStopOriginID = "loop-stop-origin"
	loopStopHubID    = "loop-stop-hub" // visited twice: positions 1 and 3
	loopStopFarID    = "loop-stop-far"
	loopStopEndID    = "loop-stop-end"
)

// loopFixtureClock is 08:05 UTC on Thursday 2025-06-12, five minutes into the
// trip, so a 60-before/240-after window spans all five stop times.
var loopFixtureClock = time.Date(2025, 6, 12, 8, 5, 0, 0, time.UTC)

// loopFixtureServiceDate is the UTC midnight the fixture's stop times are
// measured from, used to turn each visit's wall-clock time into the epoch
// scheduledArrivalTime the API reports.
var loopFixtureServiceDate = time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)

// loopTripVisit is one stop_time of loop-trip: where the stop falls in the
// trip's ordered stop list, and the raw feed stop_sequence that must not be
// mistaken for that position.
type loopTripVisit struct {
	stopID       string
	arrivalAt    time.Duration
	stopSequence int
	wantPosition int
}

// loopTripVisits mirrors loop-trip's stop_times in order. stop_sequence counts
// by ten from ten, so neither the raw value nor the raw value minus one equals
// the position — and the hub stop appears twice, so a position keyed by stop
// rather than by stop_sequence collapses its two visits onto one index.
var loopTripVisits = []loopTripVisit{
	{stopID: loopStopOriginID, arrivalAt: 8 * time.Hour, stopSequence: 10, wantPosition: 0},
	{stopID: loopStopHubID, arrivalAt: 8*time.Hour + 10*time.Minute, stopSequence: 20, wantPosition: 1},
	{stopID: loopStopFarID, arrivalAt: 8*time.Hour + 20*time.Minute, stopSequence: 30, wantPosition: 2},
	{stopID: loopStopHubID, arrivalAt: 8*time.Hour + 30*time.Minute, stopSequence: 40, wantPosition: 3},
	{stopID: loopStopEndID, arrivalAt: 8*time.Hour + 40*time.Minute, stopSequence: 50, wantPosition: 4},
}

// loopTripStopCount is how many stop_times loop-trip has, and therefore the
// exclusive upper bound every stopSequence in its arrivals must respect.
func loopTripStopCount() int {
	return len(loopTripVisits)
}

// loopFixtureCenter sits among the fixture's four stops, all of which are within
// a few hundred metres of it, so one arrivals-and-departures-for-location
// request covers every stop time of loop-trip.
var loopFixtureCenter = url.Values{
	"lat":    {"37.7754"},
	"lon":    {"-122.4194"},
	"radius": {"2000"},
}

// createTestApiWithLoopTripData builds a RestAPI backed by an in-memory GTFS
// dataset whose single trip is numbered 10/20/30/40/50 and visits loop-stop-hub
// twice. Agency timezone is UTC so a stop time's service-day offset is also its
// offset from loopFixtureServiceDate.
func createTestApiWithLoopTripData(t *testing.T) *RestAPI {
	t.Helper()
	return createTestApiWithGTFSFixture(t, clock.NewMockClock(loopFixtureClock), "loop-trip-fixture.zip", loopFixtureFiles())
}

func loopFixtureFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			loopAgencyID + ",Loop Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			loopRouteID + "," + loopAgencyID + ",LP,Loop Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			loopServiceID + ",1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			loopStopOriginID + ",Loop Origin,37.7749,-122.4194\n" +
			loopStopHubID + ",Loop Hub,37.7759,-122.4194\n" +
			loopStopFarID + ",Loop Far Side,37.7769,-122.4184\n" +
			loopStopEndID + ",Loop End,37.7739,-122.4204\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			loopRouteID + "," + loopServiceID + "," + loopTripID + ",Loop via Hub,0,loop-block-1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			loopTripID + ",08:00:00,08:00:00," + loopStopOriginID + ",10\n" +
			loopTripID + ",08:10:00,08:10:00," + loopStopHubID + ",20\n" +
			loopTripID + ",08:20:00,08:20:00," + loopStopFarID + ",30\n" +
			loopTripID + ",08:30:00,08:30:00," + loopStopHubID + ",40\n" +
			loopTripID + ",08:40:00,08:40:00," + loopStopEndID + ",50\n",
	}
}

// loopVisitsByScheduledArrival keys the fixture's visits by the epoch
// scheduledArrivalTime the API reports for them. The hub stop's two visits
// differ only by that timestamp, so it is the only thing in the response that
// says which of the two an arrival is.
func loopVisitsByScheduledArrival() map[int64]loopTripVisit {
	byArrival := make(map[int64]loopTripVisit, len(loopTripVisits))
	for _, visit := range loopTripVisits {
		byArrival[loopFixtureServiceDate.Add(visit.arrivalAt).UnixMilli()] = visit
	}
	return byArrival
}

// assertLoopTripPositions checks each of loop-trip's arrivals against the visit
// its scheduledArrivalTime identifies, and returns the positions the endpoint
// reported so a caller can require the coverage it expects.
func assertLoopTripPositions(t *testing.T, arrivals []models.ArrivalAndDeparture) map[int]bool {
	t.Helper()

	loopTripCombinedID := utils.FormCombinedID(loopAgencyID, loopTripID)
	byArrival := loopVisitsByScheduledArrival()
	seen := make(map[int]bool, len(loopTripVisits))

	for _, arrival := range arrivals {
		require.Equal(t, loopTripCombinedID, arrival.TripID,
			"the fixture has one trip, so every arrival should belong to it")

		scheduledArrival := arrival.ScheduledArrivalTime.UnixMilli()
		visit, ok := byArrival[scheduledArrival]
		require.True(t, ok, "arrival scheduled at %d matches no loop-trip stop time", scheduledArrival)

		assert.Equal(t, utils.FormCombinedID(loopAgencyID, visit.stopID), arrival.StopID,
			"the %s stop time belongs to stop %s", visit.arrivalAt, visit.stopID)
		assert.Equal(t, visit.wantPosition, arrival.StopSequence,
			"stop %s at %s is position %d of the trip, not raw stop_sequence %d",
			visit.stopID, visit.arrivalAt, visit.wantPosition, visit.stopSequence)
		assert.Equal(t, loopTripStopCount(), arrival.TotalStopsInTrip,
			"loop-trip has %d stops", loopTripStopCount())

		seen[visit.wantPosition] = true
	}

	return seen
}
