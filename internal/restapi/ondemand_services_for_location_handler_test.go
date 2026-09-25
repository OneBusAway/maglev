package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func matchReasons(list []models.OnDemandService) map[string]string {
	reasons := make(map[string]string, len(list))
	for _, service := range list {
		reasons[service.ID] = service.MatchReason
	}
	return reasons
}

func TestOnDemandServicesForLocationHandler_Alexandria(t *testing.T) {
	api := alexandriaAPI(t)

	tests := []struct {
		name           string
		query          string
		wantIDs        []string
		wantReason     string
		wantOutOfRange bool
		check          func(t *testing.T, area models.ServiceArea)
	}{
		{
			name: "point inside the zone", query: "lat=38.836368&lon=-77.049221",
			wantIDs: []string{"5088_77652"}, wantReason: models.MatchReasonAreaContainsPoint,
			check: func(t *testing.T, area models.ServiceArea) {
				require.NotNil(t, area.DistanceToArea)
				assert.Equal(t, 0.0, *area.DistanceToArea)
				assert.Nil(t, area.NearestPointOnBoundary)
			},
		},
		{
			name: "just outside with a radius that reaches the boundary", query: "lat=38.60&lon=-77.20&radius=2500",
			wantIDs: []string{"5088_77652"}, wantReason: models.MatchReasonAreaNearby,
			check: func(t *testing.T, area models.ServiceArea) {
				require.NotNil(t, area.DistanceToArea)
				assert.InDelta(t, 1956, *area.DistanceToArea, 25)
				require.NotNil(t, area.NearestPointOnBoundary)
				assert.InDelta(t, -77.2022, area.NearestPointOnBoundary[0], 0.01)
				assert.InDelta(t, 38.6175, area.NearestPointOnBoundary[1], 0.001)
			},
		},
		{
			name: "just outside with the default radius is empty", query: "lat=38.60&lon=-77.20",
			wantIDs: []string{}, wantOutOfRange: true, // a 600 m box around the point misses the zone bbox
		},
		{
			name: "far away is empty and out of range", query: "lat=39.5&lon=-77.0",
			wantIDs: []string{}, wantOutOfRange: true,
		},
		{
			name: "radius above the maximum is clamped", query: "lat=39.5&lon=-77.0&radius=1000000",
			wantIDs: []string{}, wantOutOfRange: true,
		},
		{
			name: "viewport intersecting the zone", query: "lat=38.9&lon=-77.1&latSpan=0.05&lonSpan=0.05",
			wantIDs: []string{"5088_77652"}, wantReason: models.MatchReasonAreaIntersectsViewport,
			check: func(t *testing.T, area models.ServiceArea) {
				assert.Nil(t, area.DistanceToArea, "distance fields are null in viewport mode")
				assert.Nil(t, area.NearestPointOnBoundary)
			},
		},
		{
			name: "viewport is not clamped to 20 km", query: "lat=38.84&lon=-77.22&latSpan=1.0&lonSpan=1.0",
			wantIDs: []string{"5088_77652"}, wantReason: models.MatchReasonAreaIntersectsViewport,
		},
		{
			name: "radius wins over spans", query: "lat=38.60&lon=-77.20&radius=2500&latSpan=0.05&lonSpan=0.05",
			wantIDs: []string{"5088_77652"}, wantReason: models.MatchReasonAreaNearby,
			check: func(t *testing.T, area models.ServiceArea) {
				require.NotNil(t, area.DistanceToArea)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&"+tt.query)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotNil(t, model.Data.OutOfRange)
			assert.Equal(t, tt.wantOutOfRange, *model.Data.OutOfRange)
			assert.False(t, model.Data.LimitExceeded)

			gotIDs := make([]string, 0, len(model.Data.List))
			for _, service := range model.Data.List {
				gotIDs = append(gotIDs, service.ID)
			}
			assert.Equal(t, tt.wantIDs, gotIDs)
			if len(tt.wantIDs) == 0 {
				assert.Empty(t, model.Data.References.ServiceAreas)
				return
			}
			assert.Equal(t, tt.wantReason, model.Data.List[0].MatchReason)
			assert.Len(t, model.Data.List[0].Rules, 2, "list elements carry full rules")
			require.Len(t, model.Data.References.ServiceAreas, 1)
			assert.NotEmpty(t, model.Data.References.ServiceAreas[0].Geometry, "simplified geometry by default")
			if tt.check != nil {
				tt.check(t, model.Data.References.ServiceAreas[0])
			}
		})
	}
}

func TestOnDemandServicesForLocationHandler_CharlevoixStopMatches(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))

	t.Run("point at a group stop", func(t *testing.T) {
		resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=45.2562297&lon=-85.1852475")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, map[string]string{
			"CC_CC1":     models.MatchReasonAreaContainsPoint,
			"CC_CC2_med": models.MatchReasonAreaContainsPoint,
			"CC_CC3":     models.MatchReasonStopWithinRadius,
		}, matchReasons(model.Data.List), "CC4 (Beaver Island) is far away and absent")
		assert.Equal(t, "CC_CC1", model.Data.List[0].ID, "list sorts by id")
		assert.False(t, *model.Data.OutOfRange)

		stopIDs := make([]string, 0)
		for _, stop := range model.Data.References.Stops {
			stopIDs = append(stopIDs, stop.ID)
		}
		assert.Equal(t, []string{"CC_CC_Ironton_Ferry_East", "CC_CC_Ironton_Ferry_West"}, stopIDs)
		require.Len(t, model.Data.References.LocationGroups, 1)
	})

	t.Run("viewport around the ferry", func(t *testing.T) {
		_, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=45.256&lon=-85.1835&latSpan=0.002&lonSpan=0.006")
		assert.Equal(t, models.MatchReasonStopWithinViewport, matchReasons(model.Data.List)["CC_CC3"])
		assert.Equal(t, models.MatchReasonAreaIntersectsViewport, matchReasons(model.Data.List)["CC_CC1"])
	})

	t.Run("stop outside the radius is not matched", func(t *testing.T) {
		_, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=45.30&lon=-85.20&radius=100")
		_, hasCC3 := matchReasons(model.Data.List)["CC_CC3"]
		assert.False(t, hasCC3)
	})
}

func TestOnDemandServicesForLocationHandler_Errors(t *testing.T) {
	api := alexandriaAPI(t)

	tests := []struct {
		name      string
		query     string
		wantField string
	}{
		{"invalid lat", "lat=abc&lon=-77.0", "lat"},
		{"missing lon", "lat=38.8", "lon"},
		{"lat out of range", "lat=91&lon=-77.0", "lat"},
		{"invalid geometryDetail", "lat=38.8&lon=-77.0&geometryDetail=bbox", "geometryDetail"},
		{"negative radius", "lat=38.8&lon=-77.0&radius=-5", "radius"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&"+tt.query)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			assert.NotEmpty(t, model.Data.FieldErrors[tt.wantField])
		})
	}
}

func TestOnDemandServicesForLocationHandler_NonFlexFeed(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=40.58&lon=-122.39")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []models.OnDemandService{}, model.Data.List)
	assert.False(t, *model.Data.OutOfRange, "inside RABA's stop bounds")

	_, model = callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=47.6&lon=-122.3")
	assert.True(t, *model.Data.OutOfRange, "outside every agency's stop bounds and there are no service bounds")
}

func TestOnDemandOutOfRange_NoBoundsAtAll(t *testing.T) {
	api := NewRestAPI(&app.Application{GtfsManager: newTestManagerNoData(t)})
	search := utils.CalculateBounds(47.6, -122.3, 600)
	assert.False(t, api.onDemandOutOfRange(search, gtfs.NewEmptyFlexIndex(), nil), "no stop or service bounds means nothing is out of range")
}

// An agency id containing "_" cannot be split back out of a combined service
// id, so matching must carry the bare id from the index.
func TestOnDemandServicesForLocationHandler_AgencyIDWithUnderscore(t *testing.T) {
	files := twoAgencySharedZoneFiles()
	for name, content := range files {
		files[name] = strings.ReplaceAll(content, "a1", "north_co")
	}
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "underscore-agency.zip", files)

	resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-location.json?key=TEST&lat=0.05&lon=0.05&geometryDetail=none")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"a2_r2", "north_co_r1"}, ids(model.Data.List, func(s models.OnDemandService) string { return s.ID }))
}
