package restapi

import (
	"encoding/json"
	"fmt"
	"strconv"
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

type dstArrival struct {
	ServiceDate          int64 `json:"serviceDate"`
	ScheduledArrivalTime int64 `json:"scheduledArrivalTime"`
	Frequency            *struct {
		StartTime int64 `json:"startTime"`
	} `json:"frequency"`
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

			wantArrival := start.Add(8 * time.Hour).UnixMilli()
			wantServiceDate := tc.date.Midnight(losAngeles).UnixMilli()

			_, plural := callAPIHandler[dstArrivalsResponse](t, api, fmt.Sprintf(
				"/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=5&minutesAfter=30", stopID))
			arrivals := plural.Data.Entry.ArrivalsAndDepartures
			require.Len(t, arrivals, 1, "the 08:00 Sunday trip is in the 07:45 to 08:20 window")
			assert.Equal(t, wantArrival, arrivals[0].ScheduledArrivalTime)
			assert.Equal(t, wantServiceDate, arrivals[0].ServiceDate)
			require.NotNil(t, arrivals[0].Frequency)
			assert.Equal(t, wantArrival, arrivals[0].Frequency.StartTime, "the 08:00 frequency window starts at 08:00 local")

			for _, serviceDate := range []int64{arrivals[0].ServiceDate, start.UnixMilli()} {
				resp, single := callAPIHandler[dstArrivalResponse](t, api, fmt.Sprintf(
					"/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d", stopID, tripID, serviceDate))
				require.Equal(t, 200, resp.StatusCode)
				assert.Equal(t, wantArrival, single.Data.Entry.ScheduledArrivalTime)
				assert.Equal(t, wantServiceDate, single.Data.Entry.ServiceDate)
				require.NotNil(t, single.Data.Entry.Frequency)
				assert.Equal(t, wantArrival, single.Data.Entry.Frequency.StartTime)
			}
		})
	}
}

// dstScheduleFiles adds a 10:00 Sunday trip without frequencies and a 12:00
// exact_times=1 trip to dstFiles.
func dstScheduleFiles() map[string]string {
	files := dstFiles()
	files["trips.txt"] += "dst-route,dst-svc,dst-trip-fixed,Headsign,0\n" +
		"dst-route,dst-svc,dst-trip-exact,Headsign,0\n"
	files["stop_times.txt"] += "dst-trip-fixed,10:00:00,10:00:00,dst-stop1,1\n" +
		"dst-trip-fixed,10:30:00,10:30:00,dst-stop2,2\n" +
		"dst-trip-exact,12:00:00,12:00:00,dst-stop1,1\n" +
		"dst-trip-exact,12:30:00,12:30:00,dst-stop2,2\n"
	files["frequencies.txt"] += "dst-trip-exact,12:00:00,12:10:00,600,1\n"
	return files
}

type dstScheduleForStopResponse struct {
	Data struct {
		Entry struct {
			StopRouteSchedules []struct {
				StopRouteDirectionSchedules []struct {
					ScheduleStopTimes []struct {
						ArrivalTime int64 `json:"arrivalTime"`
					} `json:"scheduleStopTimes"`
					ScheduleFrequencies []struct {
						StartTime   int64 `json:"startTime"`
						ServiceDate int64 `json:"serviceDate"`
					} `json:"scheduleFrequencies"`
				} `json:"stopRouteDirectionSchedules"`
			} `json:"stopRouteSchedules"`
		} `json:"entry"`
	} `json:"data"`
}

type dstScheduleForRouteResponse struct {
	Data struct {
		Entry struct {
			ScheduleDate      int64             `json:"scheduleDate"`
			StopTripGroupings []json.RawMessage `json:"stopTripGroupings"`
		} `json:"entry"`
	} `json:"data"`
}

func TestScheduleEndpoints_ServeStopTimesOnDSTServiceDays(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	stopID := utils.FormCombinedID("dst-agency", "dst-stop1")
	routeID := utils.FormCombinedID("dst-agency", "dst-route")

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
			api := createTestApiWithGTFSFixture(t, clock.NewMockClock(start.Add(7*time.Hour)),
				fmt.Sprintf("dst-schedule-%s.zip", tc.date), dstScheduleFiles())

			wantFixedArrival := start.Add(10 * time.Hour).UnixMilli()
			wantExactTimesArrival := start.Add(12 * time.Hour).UnixMilli()
			wantFrequencyStart := start.Add(8 * time.Hour).UnixMilli()
			wantServiceDate := tc.date.Midnight(losAngeles).UnixMilli()

			day := fmt.Sprintf("%04d-%02d-%02d", tc.date.Year, tc.date.Month, tc.date.Day)
			for _, date := range []string{day, strconv.FormatInt(start.UnixMilli(), 10)} {
				_, stopSchedule := callAPIHandler[dstScheduleForStopResponse](t, api, fmt.Sprintf(
					"/api/where/schedule-for-stop/%s.json?key=TEST&date=%s", stopID, date))
				require.Len(t, stopSchedule.Data.Entry.StopRouteSchedules, 1, "date=%s", date)
				directions := stopSchedule.Data.Entry.StopRouteSchedules[0].StopRouteDirectionSchedules
				require.Len(t, directions, 1)
				require.Len(t, directions[0].ScheduleStopTimes, 2)
				assert.Equal(t, wantFixedArrival, directions[0].ScheduleStopTimes[0].ArrivalTime, "date=%s", date)
				assert.Equal(t, wantExactTimesArrival, directions[0].ScheduleStopTimes[1].ArrivalTime, "date=%s", date)
				require.Len(t, directions[0].ScheduleFrequencies, 1)
				assert.Equal(t, wantFrequencyStart, directions[0].ScheduleFrequencies[0].StartTime, "date=%s", date)
				assert.Equal(t, wantServiceDate, directions[0].ScheduleFrequencies[0].ServiceDate, "date=%s", date)

				_, routeSchedule := callAPIHandler[dstScheduleForRouteResponse](t, api, fmt.Sprintf(
					"/api/where/schedule-for-route/%s.json?key=TEST&date=%s", routeID, date))
				assert.Equal(t, wantServiceDate, routeSchedule.Data.Entry.ScheduleDate, "date=%s", date)
				assert.NotEmpty(t, routeSchedule.Data.Entry.StopTripGroupings, "date=%s", date)
			}
		})
	}
}
