package restapi

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name  string
		stops func(t *testing.T) []models.Stop
	}{
		{
			name: "stops-for-agency",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsResponse](t, api, "/api/where/stops-for-agency/25.json?key=TEST")
				return model.Data.List
			},
		},
		{
			name: "stops-for-route",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsForRouteResponse](t, api, "/api/where/stops-for-route/25_151.json?key=TEST")
				return model.Data.References.Stops
			},
		},
		{
			name: "trips-for-route stop references",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[TripsForRouteResponse](t, api, fmt.Sprintf(
					"/api/where/trips-for-route/25_151.json?key=TEST&time=%d&includeSchedule=true", parityTime.UnixMilli()))
				return model.Data.References.Stops
			},
		},
		{
			name: "stops-for-location by stop code",
			stops: func(t *testing.T) []models.Stop {
				_, model := callAPIHandler[StopsResponse](t, api,
					"/api/where/stops-for-location.json?key=TEST&lat=40.583170&lon=-122.392586&query=2000")
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
