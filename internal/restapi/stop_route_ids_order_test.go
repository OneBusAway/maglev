package restapi

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

// RABA stop 2000 is served by 13 routes whose IDs and short names sort differently
// (route 15 is named "15" but route 151 is named "1"), so this order holds only when
// routes are sorted by name, as Java's StopBeanServiceImpl does.
var raba2000RouteIDsByName = []string{
	"25_151", "25_153", "25_154", "25_157", "25_6446", "25_159", "25_160",
	"25_15", "25_1885", "25_3779", "25_44X", "25_24", "25_161",
}

func TestStopRouteIDsAreSortedByRouteName(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(parityTime))
	defer api.Shutdown()

	tests := []struct {
		name  string
		stops func(t *testing.T) []models.Stop
	}{
		{
			name: "stops-for-agency",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-agency/25.json?key=org.onebusaway.iphone")
				return model.Data.List
			},
		},
		{
			name: "stops-for-route",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/25_151.json?key=org.onebusaway.iphone")
				return model.Data.References.Stops
			},
		},
		{
			name: "trips-for-route stop references",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[TripsForRouteResponse](t, api, fmt.Sprintf(
					"/api/where/trips-for-route/25_151.json?key=org.onebusaway.iphone&time=%d&includeSchedule=true", parityTime.UnixMilli()))
				return model.Data.References.Stops
			},
		},
		{
			name: "stops-for-location",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsResponse](t, api,
					"/api/where/stops-for-location.json?key=org.onebusaway.iphone&lat=40.583170&lon=-122.392586&radius=50")
				return model.Data.List
			},
		},
		{
			name: "stop",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[EntryResponse[models.Stop]](t, api, "/api/where/stop/25_2000.json?key=org.onebusaway.iphone")
				return []models.Stop{model.Data.Entry}
			},
		},
		{
			name: "schedule-for-stop stop references",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[EntryResponse[struct{}]](t, api, "/api/where/schedule-for-stop/25_2000.json?key=org.onebusaway.iphone&date=2025-06-12")
				return model.Data.References.Stops
			},
		},
		{
			name: "arrivals-and-departures-for-stop stop references",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[EntryResponse[struct{}]](t, api, fmt.Sprintf(
					"/api/where/arrivals-and-departures-for-stop/25_2000.json?key=org.onebusaway.iphone&time=%d", parityTime.UnixMilli()))
				return model.Data.References.Stops
			},
		},
		{
			name: "arrival-and-departure-for-stop stop references",
			stops: func(t *testing.T) []models.Stop {
				arrival := firstArrivalAtStop(t, api, "25_2000")
				_, model := callAPIHandler[EntryResponse[struct{}]](t, api, fmt.Sprintf(
					"/api/where/arrival-and-departure-for-stop/25_2000.json?key=org.onebusaway.iphone&tripId=%s&serviceDate=%d&time=%d",
					arrival.TripID, arrival.ServiceDate.UnixMilli(), parityTime.UnixMilli()))
				return model.Data.References.Stops
			},
		},
		{
			name: "arrivals-and-departures-for-location stop references",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[EntryResponse[struct{}]](t, api, fmt.Sprintf(
					"/api/where/arrivals-and-departures-for-location.json?key=org.onebusaway.iphone&lat=40.583170&lon=-122.392586&radius=50&time=%d",
					parityTime.UnixMilli()))
				return model.Data.References.Stops
			},
		},
		{
			name: "search stop",
			stops: func(t *testing.T) []models.Stop {
				stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(context.Background(), "2000")
				require.NoError(t, err)
				_, model := callAPIHandler[StopsResponse](t, api,
					"/api/where/search/stop.json?key=org.onebusaway.iphone&input="+url.QueryEscape(stop.Name.String))
				return model.Data.List
			},
		},
		{
			name: "stops-for-location by stop code",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsResponse](t, api,
					"/api/where/stops-for-location.json?key=org.onebusaway.iphone&lat=40.583170&lon=-122.392586&query=2000")
				return model.Data.List
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stop, ok := stopsByID(tt.stops(t))["25_2000"]
			require.True(t, ok, "stop 25_2000 should be in the response")

			assert.Equal(t, raba2000RouteIDsByName, stop.RouteIDs)
			assert.Equal(t, raba2000RouteIDsByName, stop.StaticRouteIDs)
		})
	}
}

// firstArrivalAtStop returns the first arrival at stopID at parityTime.
func firstArrivalAtStop(t *testing.T, api *RestAPI, stopID string) models.ArrivalAndDeparture {
	t.Helper()
	type arrivalsEntry struct {
		ArrivalsAndDepartures []models.ArrivalAndDeparture `json:"arrivalsAndDepartures"`
	}
	_, model := callAPIHandler[EntryResponse[arrivalsEntry]](t, api, fmt.Sprintf(
		"/api/where/arrivals-and-departures-for-stop/%s.json?key=org.onebusaway.iphone&time=%d", stopID, parityTime.UnixMilli()))
	require.NotEmpty(t, model.Data.Entry.ArrivalsAndDepartures, "stop %s should have arrivals at parityTime", stopID)
	return model.Data.Entry.ArrivalsAndDepartures[0]
}
