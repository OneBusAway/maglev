package restapi

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/utils"
)

// dstFiles is one Los Angeles agency with a single 08:00 trip running every day, so
// the same stop time can be read on a DST transition day and on an ordinary one.
func dstFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"dst-agency,DST Agency,http://example.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"dst-route,dst-agency,DR,DST Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"dst-svc,1,1,1,1,1,1,1,20260101,20261231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"dst-stop1,Stop One,37.7749,-122.4194\n" +
			"dst-stop2,Stop Two,37.7849,-122.4094\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,direction_id\n" +
			"dst-route,dst-svc,dst-trip,Headsign,0\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"dst-trip,08:00:00,08:00:00,dst-stop1,1\n" +
			"dst-trip,08:30:00,08:30:00,dst-stop2,2\n",
	}
}

// TestArrivalsForStop_StopTimesHoldLocalClockTimeAcrossDST verifies that an 08:00:00
// stop time is served as 08:00 local on the two days a year local midnight is not the
// start of the service day. GTFS measures stop times from noon less twelve hours.
func TestArrivalsForStop_StopTimesHoldLocalClockTimeAcrossDST(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{name: "ordinary day", at: time.Date(2026, 11, 2, 7, 50, 0, 0, losAngeles)},
		{name: "fall back", at: time.Date(2026, 11, 1, 7, 50, 0, 0, losAngeles)},
		{name: "spring forward", at: time.Date(2026, 3, 8, 7, 50, 0, 0, losAngeles)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := createTestApiWithGTFSFixture(t, clock.NewMockClock(tc.at),
				fmt.Sprintf("dst-%s.zip", tc.at.Format("20060102")), dstFiles())

			type rawResponse struct {
				Data struct {
					Entry struct {
						ArrivalsAndDepartures []map[string]json.RawMessage `json:"arrivalsAndDepartures"`
					} `json:"entry"`
				} `json:"data"`
			}

			combinedStopID := utils.FormCombinedID("dst-agency", "dst-stop1")
			url := fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/%s.json?key=TEST&minutesBefore=5&minutesAfter=30&time=%d",
				combinedStopID, tc.at.UnixMilli())
			_, model := callAPIHandler[rawResponse](t, api, url)

			require.Len(t, model.Data.Entry.ArrivalsAndDepartures, 1,
				"the 08:00 trip must be in the window that starts at 07:50")

			var scheduled int64
			require.NoError(t, json.Unmarshal(
				model.Data.Entry.ArrivalsAndDepartures[0]["scheduledArrivalTime"], &scheduled))

			arrival := time.UnixMilli(scheduled).In(losAngeles)
			assert.Equal(t, 8, arrival.Hour(), "08:00:00 must be served as 08:00 local")
			assert.Equal(t, 0, arrival.Minute())
			assert.Equal(t, tc.at.Day(), arrival.Day())
		})
	}
}
