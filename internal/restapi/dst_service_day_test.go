package restapi

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/servicedate"
	"maglev.onebusaway.org/internal/utils"
)

// dstFiles is one Los Angeles agency with an 08:00 trip that runs only on Sundays.
func dstFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"dst-agency,DST Agency,http://example.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"dst-route,dst-agency,DR,DST Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"dst-svc,0,0,0,0,0,0,1,20260101,20261231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"dst-stop1,Stop One,37.7749,-122.4194\n" +
			"dst-stop2,Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id\n" +
			"dst-route,dst-svc,dst-trip,Headsign,0\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"dst-trip,08:00:00,08:00:00,dst-stop1,1\n" +
			"dst-trip,08:30:00,08:30:00,dst-stop2,2\n",
		"frequencies.txt": "trip_id,start_time,end_time,headway_secs,exact_times\n" +
			"dst-trip,08:00:00,09:00:00,600,0\n",
	}
}

type dstFrequency struct {
	StartTime int64 `json:"startTime"`
}

type dstArrival struct {
	ServiceDate          int64         `json:"serviceDate"`
	ScheduledArrivalTime int64         `json:"scheduledArrivalTime"`
	Frequency            *dstFrequency `json:"frequency"`
	TripStatus           *struct {
		ServiceDate int64         `json:"serviceDate"`
		Frequency   *dstFrequency `json:"frequency"`
	} `json:"tripStatus"`
}

func assertDSTArrival(t *testing.T, a dstArrival, start time.Time) {
	t.Helper()
	wantArrival := start.Add(8 * time.Hour).UnixMilli()
	assert.Equal(t, wantArrival, a.ScheduledArrivalTime)
	assert.Equal(t, start.UnixMilli(), a.ServiceDate)
	assert.Equal(t, (8 * time.Hour).Milliseconds(), a.ScheduledArrivalTime-a.ServiceDate)
	require.NotNil(t, a.Frequency)
	assert.Equal(t, wantArrival, a.Frequency.StartTime, "the 08:00 frequency window starts at 08:00 local")
	require.NotNil(t, a.TripStatus)
	assert.Equal(t, start.UnixMilli(), a.TripStatus.ServiceDate)
	require.NotNil(t, a.TripStatus.Frequency)
	assert.Equal(t, wantArrival, a.TripStatus.Frequency.StartTime)
}

type dstArrivalsResponse struct {
	Data struct {
		Entry struct {
			ArrivalsAndDepartures []dstArrival `json:"arrivalsAndDepartures"`
		} `json:"entry"`
	} `json:"data"`
}

type dstArrivalResponse struct {
	Data struct {
		Entry dstArrival `json:"entry"`
	} `json:"data"`
}

func TestArrivalsEndpoints_ServeStopTimesOnDSTServiceDays(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	stopID := utils.FormCombinedID("dst-agency", "dst-stop1")
	tripID := utils.FormCombinedID("dst-agency", "dst-trip")

	for _, tc := range []struct {
		name string
		date servicedate.Date
	}{
		{name: "ordinary Sunday", date: servicedate.New(2026, time.November, 8)},
		{name: "fall back", date: servicedate.New(2026, time.November, 1)},
		{name: "spring forward", date: servicedate.New(2026, time.March, 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := tc.date.Start(losAngeles)
			now := start.Add(7*time.Hour + 50*time.Minute)
			api := createTestApiWithGTFSFixture(t, clock.NewMockClock(now),
				fmt.Sprintf("dst-%s.zip", tc.date), dstFiles())

			_, plural := callAPIHandler[dstArrivalsResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=5&minutesAfter=30", stopID))
			arrivals := plural.Data.Entry.ArrivalsAndDepartures
			require.Len(t, arrivals, 1, "the 08:00 Sunday trip is in the 07:45 to 08:20 window")
			assertDSTArrival(t, arrivals[0], start)

			for _, serviceDate := range []int64{arrivals[0].ServiceDate, tc.date.Midnight(losAngeles).UnixMilli()} {
				resp, single := callAPIHandler[dstArrivalResponse](t, api, fmt.Sprintf(
					"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate))
				require.Equal(t, 200, resp.StatusCode)
				assertDSTArrival(t, single.Data.Entry, start)
			}
		})
	}
}

type dstTripDetailsResponse struct {
	Data struct {
		Entry struct {
			ServiceDate int64 `json:"serviceDate"`
		} `json:"entry"`
	} `json:"data"`
}

func TestTripDetails_AcceptsTheArrivalsServiceDateOnDSTServiceDays(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	stopID := utils.FormCombinedID("dst-agency", "dst-stop1")
	tripID := utils.FormCombinedID("dst-agency", "dst-trip")

	for _, date := range []servicedate.Date{
		servicedate.New(2026, time.November, 8),
		servicedate.New(2026, time.November, 1),
		servicedate.New(2026, time.March, 8),
	} {
		t.Run(date.String(), func(t *testing.T) {
			now := date.Start(losAngeles).Add(7*time.Hour + 50*time.Minute)
			api := createTestApiWithGTFSFixture(t, clock.NewMockClock(now),
				fmt.Sprintf("dst-details-%s.zip", date), dstFiles())

			_, plural := callAPIHandler[dstArrivalsResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=5&minutesAfter=30", stopID))
			require.Len(t, plural.Data.Entry.ArrivalsAndDepartures, 1)

			midnight := date.Midnight(losAngeles)
			for _, serviceDate := range []string{
				fmt.Sprint(plural.Data.Entry.ArrivalsAndDepartures[0].ServiceDate),
				fmt.Sprint(midnight.UnixMilli()),
				midnight.Format("2006-01-02"),
			} {
				resp, details := callAPIHandler[dstTripDetailsResponse](t, api, fmt.Sprintf(
					"/api/where/trip-details/%s.json?key=TEST&serviceDate=%s", tripID, serviceDate))
				require.Equal(t, 200, resp.StatusCode, serviceDate)
				assert.Equal(t, date, servicedate.FromInstant(time.UnixMilli(details.Data.Entry.ServiceDate), losAngeles), serviceDate)
			}
		})
	}
}

func TestArrivalAndDepartureForStop_PicksTheClosestLoopVisitOnDSTServiceDays(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	files := dstFiles()
	delete(files, "frequencies.txt")
	files["stop_times.txt"] = "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
		"dst-trip,08:00:00,08:00:00,dst-stop1,1\n" +
		"dst-trip,08:30:00,08:30:00,dst-stop2,2\n" +
		"dst-trip,09:00:00,09:00:00,dst-stop1,3\n"

	stopID := utils.FormCombinedID("dst-agency", "dst-stop1")
	tripID := utils.FormCombinedID("dst-agency", "dst-trip")

	for _, tc := range []struct {
		name  string
		date  servicedate.Date
		at    time.Duration
		visit time.Duration
	}{
		{name: "fall back", date: servicedate.New(2026, time.November, 1), at: 8*time.Hour + 20*time.Minute, visit: 8 * time.Hour},
		{name: "spring forward", date: servicedate.New(2026, time.March, 8), at: 8*time.Hour + 40*time.Minute, visit: 9 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := tc.date.Start(losAngeles)
			api := createTestApiWithGTFSFixture(t, clock.NewMockClock(start.Add(tc.at)),
				fmt.Sprintf("dst-loop-%s.zip", tc.date), files)

			resp, single := callAPIHandler[dstArrivalResponse](t, api, fmt.Sprintf(
				"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, start.UnixMilli()))
			require.Equal(t, 200, resp.StatusCode)
			assert.Equal(t, start.Add(tc.visit).UnixMilli(), single.Data.Entry.ScheduledArrivalTime)
		})
	}
}
