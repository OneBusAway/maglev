package restapi

import (
	"testing"
	"time"

	"maglev.onebusaway.org/internal/clock"
)

// Frequency fixture identifiers shared by the handler tests that exercise
// GTFS frequencies integration (trip-for-vehicle, arrival-and-departure,
// trips-for-route, trips-for-location, schedule-for-stop).
const (
	freqAgencyID    = "freq-agency"
	freqRouteID     = "freq-route"
	freqServiceID   = "freq-service"
	freqTripID      = "freq-trip"        // frequency-based, exact_times=0
	freqExactTripID = "freq-exact-trip"  // frequency-based, exact_times=1
	freqNormalTripD = "freq-normal-trip" // no frequency entry
	freqStopAID     = "freq-stop-a"
	freqStopBID     = "freq-stop-b"
)

// frequencyFixtureClock pins the handler clock to 08:00 UTC on Thursday
// 2025-06-12 — inside both frequency windows (06:00–09:00) and inside the
// calendar's active range, so trip-for-vehicle's default serviceDate resolves
// to a service day the fixture serves.
var frequencyFixtureClock = time.Date(2025, 6, 12, 8, 0, 0, 0, time.UTC)

// createTestApiWithFrequencyData builds a RestAPI backed by an in-memory GTFS
// dataset containing a headway-based frequency trip (exact_times=0), an
// exact-times frequency trip (exact_times=1), and a plain scheduled trip with
// no frequency entry. Agency timezone is UTC to keep service-date arithmetic
// trivial in assertions.
func createTestApiWithFrequencyData(t *testing.T) *RestAPI {
	t.Helper()
	return createTestApiWithGTFSFixture(t, clock.NewMockClock(frequencyFixtureClock), "frequency-fixture.zip", frequencyFixtureFiles())
}

func frequencyFixtureFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			freqAgencyID + ",Frequency Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			freqRouteID + "," + freqAgencyID + ",FR,Frequency Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			freqServiceID + ",1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			freqStopAID + ",Frequency Stop A,37.7749,-122.4194\n" +
			freqStopBID + ",Frequency Stop B,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id,block_id\n" +
			freqRouteID + "," + freqServiceID + "," + freqTripID + ",Downtown via Freq,0,freq-block-1\n" +
			freqRouteID + "," + freqServiceID + "," + freqExactTripID + ",Exact Times Trip,0,freq-block-2\n" +
			freqRouteID + "," + freqServiceID + "," + freqNormalTripD + ",Normal Trip,0,freq-block-3\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			freqTripID + ",06:00:00,06:00:00," + freqStopAID + ",1\n" +
			freqTripID + ",06:10:00,06:10:00," + freqStopBID + ",2\n" +
			freqExactTripID + ",06:00:00,06:00:00," + freqStopAID + ",1\n" +
			freqExactTripID + ",06:15:00,06:15:00," + freqStopBID + ",2\n" +
			freqNormalTripD + ",08:00:00,08:00:00," + freqStopAID + ",1\n" +
			freqNormalTripD + ",08:15:00,08:15:00," + freqStopBID + ",2\n",
		// freq-trip:  06:00–09:00 headway-based window, 10-minute headway.
		// freq-exact-trip: 06:00–09:00 schedule-based window, 30-minute headway.
		// freq-normal-trip: no frequency entry.
		"frequencies.txt": "trip_id,start_time,end_time,headway_secs,exact_times\n" +
			freqTripID + ",06:00:00,09:00:00,600,0\n" +
			freqExactTripID + ",06:00:00,09:00:00,1800,1\n",
	}
}
