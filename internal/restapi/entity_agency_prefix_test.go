package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

func agencyPrefixFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"own,Owner Agency,http://example.com,UTC\n" +
			"other,Other Agency,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"own-route,own,OW,Owner Route,3\n" +
			"other-route,other,OT,Other Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"stop-1,Stop One,37.7749,-122.4194\n" +
			"stop-2,Stop Two,37.7849,-122.4094\n",
		"shapes.txt": "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\n" +
			"own-shape,37.7749,-122.4194,1\n" +
			"own-shape,37.7849,-122.4094,2\n",
		"trips.txt": "route_id,service_id,trip_id,block_id,shape_id\n" +
			"own-route,svc,own-trip,own-block,own-shape\n" +
			"other-route,svc,other-trip,other-block,\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"own-trip,09:00:00,09:00:00,stop-1,1\n" +
			"own-trip,09:30:00,09:30:00,stop-2,2\n" +
			"other-trip,10:00:00,10:00:00,stop-1,1\n" +
			"other-trip,10:30:00,10:30:00,stop-2,2\n",
	}
}

func TestEntityHandlers_AgencyPrefixMustOwnEntity(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	entities := []struct {
		endpoint string
		entityID string
	}{
		{"trip", "own-trip"},
		{"route", "own-route"},
		{"shape", "own-shape"},
		{"block", "own-block"},
		{"schedule-for-route", "own-route"},
		{"stops-for-route", "own-route"},
	}
	prefixes := []struct {
		agencyID   string
		wantStatus int
	}{
		{"own", http.StatusOK},
		{"other", http.StatusNotFound},
		{"unknown", http.StatusNotFound},
	}

	for _, entity := range entities {
		for _, prefix := range prefixes {
			path := "/api/where/" + entity.endpoint + "/" + prefix.agencyID + "_" + entity.entityID + ".json?key=TEST"
			t.Run(entity.endpoint+"/"+prefix.agencyID, func(t *testing.T) {
				resp, model := serveApiAndRetrieveEndpoint(t, api, path)
				assert.Equal(t, prefix.wantStatus, resp.StatusCode)
				assert.Equal(t, prefix.wantStatus, model.Code)
			})
		}
	}
}

// agencyPrefixActiveTrip is inside own-trip's 09:00–09:30 UTC span, within the
// 30-minute-late / 10-minute-early window trips-for-route uses.
var agencyPrefixActiveTrip = time.Date(2025, 6, 13, 9, 15, 0, 0, time.UTC)

func TestTripsForRoute_AgencyPrefixMustOwnRoute(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())
	activeAt := agencyPrefixActiveTrip.UnixMilli()

	cases := []struct {
		name       string
		routeID    string
		wantTrips  bool
		wantStatus int
		timeParam  string
	}{
		{name: "owning prefix", routeID: "own_own-route", wantTrips: true, wantStatus: http.StatusOK, timeParam: fmt.Sprintf("%d", activeAt)},
		{name: "other agency", routeID: "other_own-route", wantTrips: false, wantStatus: http.StatusOK, timeParam: fmt.Sprintf("%d", activeAt)},
		{name: "unknown agency", routeID: "unknown_own-route", wantTrips: false, wantStatus: http.StatusOK, timeParam: fmt.Sprintf("%d", activeAt)},
		{name: "unknown route", routeID: "own_missing", wantTrips: false, wantStatus: http.StatusOK, timeParam: fmt.Sprintf("%d", activeAt)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := fmt.Sprintf("/api/where/trips-for-route/%s.json?key=TEST&time=%s", tc.routeID, tc.timeParam)
			resp, model := callAPIHandler[TripsForRouteResponse](t, api, path)
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
			assert.Equal(t, tc.wantStatus, model.Code)
			assert.Equal(t, "OK", model.Text)
			assert.False(t, model.Data.LimitExceeded)
			if tc.wantTrips {
				require.NotEmpty(t, model.Data.List)
				return
			}
			assert.Empty(t, model.Data.List)
		})
	}

	t.Run("invalid time stays a field error", func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trips-for-route/other_own-route.json?key=TEST&time=garbage")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, http.StatusBadRequest, model.Code)
	})
}

func TestRouteScheduleAndStops_InvalidParamsPrecedeOwnership(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "agency-prefix.zip", agencyPrefixFiles())

	t.Run("schedule-for-route invalid date", func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-route/other_own-route.json?key=TEST&date=garbage")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, http.StatusBadRequest, model.Code)
	})

	t.Run("stops-for-route invalid time", func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/stops-for-route/other_own-route.json?key=TEST&time=garbage")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, http.StatusBadRequest, model.Code)
	})
}
