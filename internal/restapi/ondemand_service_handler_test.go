package restapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/models"
)

// onDemandEntryResponse decodes the /ondemand entry envelope.
type onDemandEntryResponse struct {
	Code        int    `json:"code"`
	CurrentTime int64  `json:"currentTime"`
	Text        string `json:"text"`
	Version     int    `json:"version"`
	Data        struct {
		Entry       models.OnDemandService    `json:"entry"`
		References  models.OnDemandReferences `json:"references"`
		FieldErrors map[string][]string       `json:"fieldErrors"`
	} `json:"data"`
}

// onDemandListResponse decodes the /ondemand list envelope.
type onDemandListResponse struct {
	Code    int    `json:"code"`
	Text    string `json:"text"`
	Version int    `json:"version"`
	Data    struct {
		LimitExceeded bool                      `json:"limitExceeded"`
		List          []models.OnDemandService  `json:"list"`
		OutOfRange    *bool                     `json:"outOfRange"`
		References    models.OnDemandReferences `json:"references"`
		FieldErrors   map[string][]string       `json:"fieldErrors"`
	} `json:"data"`
}

func alexandriaAPI(t *testing.T) *RestAPI {
	t.Helper()
	return createTestApiWithFeed(t, models.GetFixturePath(t, "alexandria-flex.zip"))
}

func str(s string) *string   { return &s }
func num(i int) *int         { return &i }
func flt(f float64) *float64 { return &f }

func TestOnDemandServiceHandler_AlexandriaMatchesWorkedExample(t *testing.T) {
	api := alexandriaAPI(t)
	resp, model := callAPIHandler[onDemandEntryResponse](t, api, "/api/ondemand/service/5088_77652.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)

	booking := str("5088_booking_route_77652")
	wantEntry := models.OnDemandService{
		ID: "5088_77652", AgencyID: "5088", RouteID: str("5088_77652"), Name: "DOT Paratransit", ServiceKind: "zone",
		Rules: []models.AvailabilityRule{
			{
				FromIds: []string{"5088_area_1449"}, ToIds: []string{"5088_area_1449"},
				StartPickupTime: str("05:00:00"), EndPickupTime: str("24:50:00"), EndDropOffTime: str("25:00:00"),
				CalendarIds: []string{"5088_c_71675_b_85952_d_63"}, PickupType: 2, DropOffType: 2,
				PickupBookingRuleId: booking, DropOffBookingRuleId: booking,
				SafeDurationFactor: flt(1), SafeDurationOffset: flt(0),
			},
			{
				FromIds: []string{"5088_area_1449"}, ToIds: []string{"5088_area_1449"},
				StartPickupTime: str("07:00:00"), EndPickupTime: str("24:50:00"), EndDropOffTime: str("25:00:00"),
				CalendarIds: []string{"5088_c_71675_b_85952_d_64"}, PickupType: 2, DropOffType: 2,
				PickupBookingRuleId: booking, DropOffBookingRuleId: booking,
				SafeDurationFactor: flt(1), SafeDurationOffset: flt(0),
			},
		},
	}
	assert.Equal(t, wantEntry, model.Data.Entry)

	refs := model.Data.References
	require.Len(t, refs.Agencies, 1)
	assert.Equal(t, "5088", refs.Agencies[0].ID)
	assert.Equal(t, "Alexandria DOT", refs.Agencies[0].Name)
	assert.Equal(t, "America/Los_Angeles", refs.Agencies[0].Timezone, "agency timezone, never the stop's America/New_York")
	require.Len(t, refs.Routes, 1)
	assert.Equal(t, "5088_77652", refs.Routes[0].ID)
	assert.Equal(t, "DOT Paratransit", refs.Routes[0].LongName)
	assert.Empty(t, refs.Stops)
	assert.Empty(t, refs.Situations)
	assert.Empty(t, refs.StopTimes)
	assert.Empty(t, refs.Trips)
	assert.Empty(t, refs.LocationGroups)

	require.Len(t, refs.ServiceAreas, 1)
	area := refs.ServiceAreas[0]
	assert.Equal(t, "5088_area_1449", area.ID)
	assert.Nil(t, area.Name)
	assert.Nil(t, area.Description)
	assert.Equal(t, [4]float64{-77.5372039, 38.617508, -76.9092198, 39.057831}, area.BBox)
	assert.Nil(t, area.DistanceToArea)
	assert.Nil(t, area.NearestPointOnBoundary)
	geometryType, polygons, err := geo.ParseGeoJSONPolygons(area.Geometry)
	require.NoError(t, err)
	assert.Equal(t, geo.GeoJSONPolygon, geometryType)
	assert.Len(t, polygons[0][0], 4239, "geometryDetail defaults to full on service/{id}")

	require.Len(t, refs.BookingRules, 1)
	rule := refs.BookingRules[0]
	assert.Equal(t, "5088_booking_route_77652", rule.ID)
	assert.Equal(t, 2, rule.BookingType)
	assert.Nil(t, rule.PriorNoticeDurationMin)
	assert.Nil(t, rule.PriorNoticeDurationMax)
	assert.Equal(t, num(1), rule.PriorNoticeLastDay)
	assert.Equal(t, str("17:00:00"), rule.PriorNoticeLastTime)
	assert.Equal(t, num(14), rule.PriorNoticeStartDay)
	assert.Equal(t, str("00:00:00"), rule.PriorNoticeStartTime)
	assert.Nil(t, rule.PriorNoticeCalendarId)
	assert.True(t, strings.HasPrefix(*rule.Message, "DOT is the City of Alexandria's paratransit program"))
	assert.Nil(t, rule.PickupMessage)
	assert.Nil(t, rule.DropOffMessage)
	assert.Equal(t, str("703-746-5222"), rule.PhoneNumber)
	assert.Equal(t, str("https://www.alexandriava.gov/Paratransit"), rule.InfoUrl)
	assert.True(t, strings.HasPrefix(*rule.BookingUrl, "https://spare-rider-alexandriadot-production.vercel.app/"))

	assert.Equal(t, []models.OnDemandCalendar{
		{ID: "5088_c_71675_b_85952_d_63", Days: []string{"mon", "tue", "wed", "thu", "fri", "sat"}, StartDate: "2025-12-01", EndDate: "2026-12-01", ExceptedDates: []string{}},
		{ID: "5088_c_71675_b_85952_d_64", Days: []string{"sun"}, StartDate: "2025-12-01", EndDate: "2026-12-01", ExceptedDates: []string{}},
	}, refs.Calendars)
}

func TestOnDemandServiceHandler_GeometryDetail(t *testing.T) {
	api := alexandriaAPI(t)

	tests := []struct {
		name         string
		query        string
		wantRing     func(t *testing.T, n int)
		wantGeometry bool
	}{
		{"simplified", "&geometryDetail=simplified", func(t *testing.T, n int) { assert.LessOrEqual(t, n, geo.SimplifyMaxRingPoints) }, true},
		{"full", "&geometryDetail=full", func(t *testing.T, n int) { assert.Equal(t, 4239, n) }, true},
		{"none", "&geometryDetail=none", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[onDemandEntryResponse](t, api, "/api/ondemand/service/5088_77652.json?key=TEST"+tt.query)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			area := model.Data.References.ServiceAreas[0]
			assert.Equal(t, [4]float64{-77.5372039, 38.617508, -76.9092198, 39.057831}, area.BBox, "bbox is present at every detail level")
			if !tt.wantGeometry {
				assert.Empty(t, area.Geometry)
				return
			}
			_, polygons, err := geo.ParseGeoJSONPolygons(area.Geometry)
			require.NoError(t, err)
			tt.wantRing(t, len(polygons[0][0]))
		})
	}
}

func TestOnDemandServiceHandler_Errors(t *testing.T) {
	api := alexandriaAPI(t)

	tests := []struct {
		name       string
		endpoint   string
		wantStatus int
		wantText   string
	}{
		{"unknown service", "/api/ondemand/service/5088_nope.json?key=TEST", http.StatusNotFound, "resource not found"},
		{"wrong agency prefix", "/api/ondemand/service/9999_77652.json?key=TEST", http.StatusNotFound, "resource not found"},
		{"missing agency prefix", "/api/ondemand/service/77652.json?key=TEST", http.StatusBadRequest, "invalid format: 77652"},
		{"invalid geometryDetail", "/api/ondemand/service/5088_77652.json?key=TEST&geometryDetail=bbox", http.StatusBadRequest, `Invalid field value for field "geometryDetail".`},
		{"missing api key", "/api/ondemand/service/5088_77652.json", http.StatusUnauthorized, "permission denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, tt.wantStatus, model.Code)
			assert.Equal(t, tt.wantText, model.Text)
		})
	}

	_, model := callAPIHandler[onDemandEntryResponse](t, api, "/api/ondemand/service/5088_77652.json?key=TEST&geometryDetail=bbox")
	assert.Equal(t, []string{`Invalid field value for field "geometryDetail".`}, model.Data.FieldErrors["geometryDetail"])
}

func TestOnDemandServiceHandler_NonFlexFeedIs404NotUnmounted(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, _ := serveApiAndRetrieveEndpoint(t, api, "/api/ondemand/service/25_1.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a plain route is not a service; the namespace is still mounted")
}
