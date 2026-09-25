package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/models"
)

func TestOnDemandServicesForAgencyHandler_Manistee(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "manistee-flex.zip"))
	resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-agency/MC.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.False(t, model.Data.LimitExceeded)
	assert.Nil(t, model.Data.OutOfRange, "only services-for-location carries outOfRange")
	require.Len(t, model.Data.List, 2)

	mc1, mc2 := model.Data.List[0], model.Data.List[1]
	assert.Equal(t, "MC_MC1", mc1.ID)
	assert.Equal(t, "zone", mc1.ServiceKind)
	assert.Equal(t, "Manistee Dial-a-Ride (City)", mc1.Name)
	assert.Empty(t, mc1.MatchReason, "matchReason is a services-for-location field")
	require.Len(t, mc1.Rules, 2)
	assert.Equal(t, "05:30:00", *mc1.Rules[0].StartPickupTime)
	assert.Equal(t, "18:00:00", *mc1.Rules[0].EndPickupTime)
	assert.Nil(t, mc1.Rules[0].EndDropOffTime)
	assert.Equal(t, []string{"MC_mon-tues-wed-thurs-fri"}, mc1.Rules[0].CalendarIds)
	assert.Equal(t, "10:00:00", *mc1.Rules[1].StartPickupTime)
	assert.Equal(t, []string{"MC_sat"}, mc1.Rules[1].CalendarIds)
	assert.Equal(t, "MC_booking_rule_MC1", *mc1.Rules[0].PickupBookingRuleId)
	assert.Equal(t, 2.0, *mc1.Rules[0].SafeDurationFactor)
	assert.Equal(t, 30.0, *mc1.Rules[0].SafeDurationOffset)

	assert.Equal(t, "MC_MC2", mc2.ID)
	assert.Equal(t, "zoneToZone", mc2.ServiceKind)
	assert.Len(t, mc2.Rules, 18)

	refs := model.Data.References
	assert.Len(t, refs.ServiceAreas, 6, "grand_traverse_county is stored but referenced by no record")
	for _, area := range refs.ServiceAreas {
		_, polygons, err := geo.ParseGeoJSONPolygons(area.Geometry)
		require.NoError(t, err, area.ID)
		assert.LessOrEqual(t, len(polygons[0][0]), geo.SimplifyMaxRingPoints, "list endpoints default to simplified geometry")
	}
	assert.Len(t, refs.BookingRules, 6, "MC3_college and MC4_med are referenced by no rule or record")
	require.Len(t, refs.Calendars, 2)
	assert.Equal(t, "MC_mon-tues-wed-thurs-fri", refs.Calendars[0].ID)
	assert.Len(t, refs.Calendars[0].ExceptedDates, 16)
	assert.Equal(t, []string{"MC_MC1", "MC_MC2"}, []string{refs.Routes[0].ID, refs.Routes[1].ID})
	assert.Empty(t, refs.Stops, "no rule references a stop")
}

func TestOnDemandServicesForAgencyHandler_ZeroStopsFeedOnTheWire(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "flex-zero-stops.zip", flexfixtures.ZeroStopsFiles())
	resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-agency/AP.json?key=TEST&geometryDetail=full")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.List, 2)
	assert.Equal(t, "AP_AP1", model.Data.List[0].ID)
	assert.Equal(t, "zoneToZone", model.Data.List[0].ServiceKind)
	assert.Len(t, model.Data.List[0].Rules, 18)
	assert.Equal(t, "AP_AP2_med", model.Data.List[1].ID)
	assert.Len(t, model.Data.List[1].Rules, 4)

	refs := model.Data.References
	assert.Empty(t, refs.Stops, "the feed has no stops at all")

	byID := map[string]models.BookingRule{}
	for _, rule := range refs.BookingRules {
		byID[rule.ID] = rule
	}
	ap1 := byID["AP_booking_rule_AP1"]
	assert.Equal(t, 1, ap1.BookingType)
	assert.Nil(t, ap1.PriorNoticeDurationMin, "type 1 with no duration bound reaches the wire as null")
	assert.Nil(t, ap1.PriorNoticeDurationMax)
	ap2 := byID["AP_booking_rule_AP2"]
	assert.Equal(t, 2, ap2.BookingType)
	assert.Equal(t, num(7), ap2.PriorNoticeLastDay)
	assert.Nil(t, ap2.PriorNoticeLastTime, "type 2 with a last day but no last time reaches the wire as null")

	areaIDs := make([]string, 0, len(refs.ServiceAreas))
	for _, area := range refs.ServiceAreas {
		areaIDs = append(areaIDs, area.ID)
	}
	assert.NotContains(t, areaIDs, "AP_ignored_zone", "location.geojson (singular) is ignored")
	assert.Contains(t, areaIDs, "AP_nemt_all_michigan_upper")
	for _, area := range refs.ServiceAreas {
		if area.ID != "AP_nemt_all_michigan_upper" {
			continue
		}
		_, polygons, err := geo.ParseGeoJSONPolygons(area.Geometry)
		require.NoError(t, err)
		require.Len(t, polygons[0], 2, "the interior ring (hole) survives import")
		assert.Len(t, polygons[0][1], 5)
	}

	calendarIDs := make([]string, 0)
	for _, calendar := range refs.Calendars {
		calendarIDs = append(calendarIDs, calendar.ID)
	}
	assert.Equal(t, []string{"AP_mon-tues-wed-thurs-fri", "AP_sat"}, calendarIDs)
	assert.Equal(t, []string{"2026-01-01"}, refs.Calendars[0].ExceptedDates)
}

func TestOnDemandServicesForAgencyHandler_Errors(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "manistee-flex.zip"))

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/ondemand/services-for-agency/nope.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "resource not found", model.Text)

	resp, model = serveApiAndRetrieveEndpoint(t, api, "/api/ondemand/services-for-agency/MC.json?key=TEST&geometryDetail=verbatim")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, `Invalid field value for field "geometryDetail".`, model.Text)
}

func TestOnDemandServicesForAgencyHandler_AgencyWithoutFlexIsEmptyList(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := callAPIHandler[onDemandListResponse](t, api, "/api/ondemand/services-for-agency/25.json?key=TEST")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []models.OnDemandService{}, model.Data.List)
	assert.Equal(t, *models.NewEmptyOnDemandReferences(), model.Data.References, "all ten keys present and empty")
}
