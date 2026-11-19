package restapi

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

const (
	nearbyIntervalAgencyID = "ni-agency"
	nearbyIntervalBusType  = "3"
	nearbyIntervalTramType = "0"
)

// nearbyIntervalClock is a Monday noon in the agency's timezone (UTC), inside
// the fixture calendar, so the default service interval is Monday 00:00-24:00.
var nearbyIntervalClock = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// nearbyIntervalFiles builds a feed with one found stop and several nearby
// candidates, each visited at a different time. Every candidate sits within
// 100 m of the found stop and the service runs every day, so the service date
// never excludes a stop and only the stop time can.
func nearbyIntervalFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			nearbyIntervalAgencyID + ",Interval Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"bus," + nearbyIntervalAgencyID + ",B,Bus," + nearbyIntervalBusType + "\n" +
			"tram," + nearbyIntervalAgencyID + ",T,Tram," + nearbyIntervalTramType + "\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"s-found,Found,37.7749,-122.4194\n" +
			"s-2300,Visited 23:00,37.7752,-122.4194\n" +
			"s-2400,Visited 24:00,37.7753,-122.4194\n" +
			"s-2500,Visited 25:00 only,37.7754,-122.4194\n" +
			"s-mixed,Visited 25:00 and 10:00,37.7755,-122.4194\n" +
			"s-split,Bus 25:00 and tram 10:00,37.7756,-122.4194\n" +
			"s-far,Terminal,37.8749,-122.4194\n",
		"trips.txt": "route_id,service_id,trip_id\n" +
			"bus,svc,t-found\n" +
			"bus,svc,t-2300\n" +
			"bus,svc,t-2400\n" +
			"bus,svc,t-2500\n" +
			"bus,svc,t-mixed-late\n" +
			"bus,svc,t-mixed-day\n" +
			"bus,svc,t-split-bus\n" +
			"tram,svc,t-split-tram\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			nearbyIntervalVisit("t-found", "s-found", "10:00:00") +
			nearbyIntervalVisit("t-2300", "s-2300", "23:00:00") +
			nearbyIntervalVisit("t-2400", "s-2400", "24:00:00") +
			nearbyIntervalVisit("t-2500", "s-2500", "25:00:00") +
			nearbyIntervalVisit("t-mixed-late", "s-mixed", "25:00:00") +
			nearbyIntervalVisit("t-mixed-day", "s-mixed", "10:00:00") +
			nearbyIntervalVisit("t-split-bus", "s-split", "25:00:00") +
			nearbyIntervalVisit("t-split-tram", "s-split", "10:00:00"),
	}
}

// nearbyIntervalVisit returns the two stop_times rows of a trip that visits
// stopID at the given time and ends at the far terminal.
func nearbyIntervalVisit(tripID, stopID, visitTime string) string {
	return tripID + "," + visitTime + "," + visitTime + "," + stopID + ",1\n" +
		tripID + ",26:00:00,26:00:00,s-far,2\n"
}

func nearbyIntervalStopIDs(stopIDs ...string) []string {
	combined := make([]string, 0, len(stopIDs))
	for _, id := range stopIDs {
		combined = append(combined, nearbyIntervalAgencyID+"_"+id)
	}
	slices.Sort(combined)
	return combined
}

func TestArrivalsAndDeparturesForLocationNearbyStopsRequireVisitInServiceInterval(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(nearbyIntervalClock), "nearby-interval.zip", nearbyIntervalFiles())

	tests := []struct {
		name       string
		routeType  string
		wantNearby []string
	}{
		{
			name:       "without routeType a visit past 24:00 does not qualify",
			wantNearby: nearbyIntervalStopIDs("s-2300", "s-2400", "s-mixed", "s-split"),
		},
		{
			name:       "routeType must match the route whose visit is in the interval",
			routeType:  nearbyIntervalBusType,
			wantNearby: nearbyIntervalStopIDs("s-2300", "s-2400", "s-mixed"),
		},
		{
			name:       "routeType matching the in-interval route keeps the stop",
			routeType:  nearbyIntervalTramType,
			wantNearby: nearbyIntervalStopIDs("s-split"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overrides := url.Values{
				"lat":    {"37.7749"},
				"lon":    {"-122.4194"},
				"radius": {"30"},
			}
			if tt.routeType != "" {
				overrides.Set("routeType", tt.routeType)
			}
			params := []url.Values{overrides}

			resp, model := callAPIHandler[ArrivalsAndDeparturesForLocationResponse](t, api, arrivalsForLocationURL(params...))

			require.Equal(t, http.StatusOK, resp.StatusCode)
			var gotNearby []string
			for _, nearby := range model.Data.Entry.NearbyStopIDs {
				gotNearby = append(gotNearby, nearby.StopID)
			}
			slices.Sort(gotNearby)
			assert.Equal(t, tt.wantNearby, gotNearby)
		})
	}
}
