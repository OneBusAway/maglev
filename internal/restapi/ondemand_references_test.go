package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
)

func buildAllOnDemandServices(t *testing.T, api *RestAPI, opts onDemandBuildOptions) ([]models.OnDemandService, *models.OnDemandReferences) {
	t.Helper()
	return buildAllOnDemandServicesWithContext(t, context.Background(), api, opts)
}

func buildAllOnDemandServicesWithContext(t *testing.T, ctx context.Context, api *RestAPI, opts onDemandBuildOptions) ([]models.OnDemandService, *models.OnDemandReferences) {
	t.Helper()
	services, err := api.GtfsManager.GtfsDB.Queries.ListOnDemandServices(ctx)
	require.NoError(t, err)
	list, refs, err := api.buildOnDemandServices(ctx, services, opts)
	require.NoError(t, err)
	return list, refs
}

// contextCapturingLogs returns a context whose logger writes to the buffer.
func contextCapturingLogs() (context.Context, *bytes.Buffer) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	return logging.WithLogger(context.Background(), logger), &logs
}

func ids[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, id(item))
	}
	return out
}

func TestBuildOnDemandServices_Charlevoix(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	assert.Equal(t, []string{"CC_CC1", "CC_CC2_med", "CC_CC3", "CC_CC4"}, ids(list, func(s models.OnDemandService) string { return s.ID }))

	cc1 := list[0]
	assert.Equal(t, "CC", cc1.AgencyID)
	assert.Equal(t, "CC_CC1", *cc1.RouteID)
	assert.Equal(t, "Charlevoix County Dial-a-Ride", cc1.Name)
	assert.Equal(t, "zone", cc1.ServiceKind)
	assert.Equal(t, "Can book within the same day. Service operates within Charlevoix County.", *cc1.Description)
	assert.Nil(t, cc1.URL)
	require.Len(t, cc1.Rules, 1)
	assert.Equal(t, []string{"CC_mon-tues-wed-thurs-fri", "CC_sat"}, cc1.Rules[0].CalendarIds)

	assert.Len(t, list[1].Rules, 4)
	for _, rule := range list[1].Rules {
		assert.Len(t, rule.CalendarIds, 2)
	}

	cc3 := list[2]
	assert.Equal(t, "stopGroup", cc3.ServiceKind)
	require.Len(t, cc3.Rules, 1)
	assert.Equal(t, []string{"CC_CC_ironton_ferry_stops"}, cc3.Rules[0].FromIds)
	assert.Equal(t, []string{"CC_mon-tues-wed-thurs-fri-sat-sun"}, cc3.Rules[0].CalendarIds)
	assert.Equal(t, "06:30:00", *cc3.Rules[0].StartPickupTime)
	assert.Equal(t, "22:30:00", *cc3.Rules[0].EndPickupTime)
	assert.Nil(t, cc3.Rules[0].EndDropOffTime)
	assert.Equal(t, 1.0, *cc3.Rules[0].SafeDurationFactor)
	assert.Equal(t, 60.0, *cc3.Rules[0].SafeDurationOffset)

	assert.Equal(t, []string{"CC_beaver_island", "CC_charlevoix_county", "CC_gaylord", "CC_petoskey"},
		ids(refs.ServiceAreas, func(a models.ServiceArea) string { return a.ID }))
	for _, area := range refs.ServiceAreas {
		assert.Empty(t, area.Geometry, "geometryDetail=none omits geometry")
		assert.Nil(t, area.DistanceToArea)
	}

	require.Len(t, refs.LocationGroups, 1)
	assert.Equal(t, models.LocationGroupReference{
		ID: "CC_CC_ironton_ferry_stops", Name: models.NullableString("Ironton Ferry Stops"),
		StopIds: []string{"CC_CC_Ironton_Ferry_East", "CC_CC_Ironton_Ferry_West"},
	}, refs.LocationGroups[0])

	assert.Equal(t, []string{"CC_CC_Ironton_Ferry_East", "CC_CC_Ironton_Ferry_West"}, ids(refs.Stops, func(s models.Stop) string { return s.ID }))
	assert.Equal(t, []string{}, refs.Stops[0].RouteIDs, "group members have no timed stop_times")

	assert.Equal(t, []string{"CC_booking_rule_CC1", "CC_booking_rule_CC2", "CC_booking_rule_CC2_char", "CC_booking_rule_CC3", "CC_booking_rule_CC4"},
		ids(refs.BookingRules, func(b models.BookingRule) string { return b.ID }))
	cc4 := refs.BookingRules[4]
	assert.Equal(t, 0, cc4.BookingType)
	assert.Equal(t, 90, *cc4.PriorNoticeDurationMin, "published as-is; clients ignore forbidden fields")
	assert.Nil(t, cc4.PhoneNumber)
	assert.Equal(t, "(231) 582-6900", *refs.BookingRules[0].PhoneNumber)

	assert.Equal(t, []string{"CC_mon-tues-wed-thurs-fri", "CC_mon-tues-wed-thurs-fri-sat-sun", "CC_sat"},
		ids(refs.Calendars, func(c models.OnDemandCalendar) string { return c.ID }))
	assert.Len(t, refs.Calendars[0].ExceptedDates, 16)
	assert.Equal(t, "2024-05-27", refs.Calendars[0].ExceptedDates[0])
	assert.Equal(t, []string{"mon", "tue", "wed", "thu", "fri"}, refs.Calendars[0].Days)
	assert.Equal(t, "2024-01-01", refs.Calendars[0].StartDate)

	assert.Equal(t, []string{"CC"}, ids(refs.Agencies, func(a models.AgencyReference) string { return a.ID }))
	assert.Equal(t, "America/Detroit", refs.Agencies[0].Timezone)
	assert.Equal(t, []string{"CC_CC1", "CC_CC2_med", "CC_CC3", "CC_CC4"}, ids(refs.Routes, func(r models.Route) string { return r.ID }))
	assert.Equal(t, []models.Situation{}, refs.Situations)
	assert.Equal(t, []models.RouteStopTime{}, refs.StopTimes)
	assert.Equal(t, []models.Trip{}, refs.Trips)
}

func TestBuildOnDemandServices_GeometryDetail(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "alexandria-flex.zip"))

	ringLength := func(raw json.RawMessage) int {
		_, polygons, err := geo.ParseGeoJSONPolygons(raw)
		require.NoError(t, err)
		return len(polygons[0][0])
	}

	_, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailFull})
	require.Len(t, refs.ServiceAreas, 1)
	assert.Equal(t, 4239, ringLength(refs.ServiceAreas[0].Geometry))
	assert.Equal(t, [4]float64{-77.5372039, 38.617508, -76.9092198, 39.057831}, refs.ServiceAreas[0].BBox)
	assert.Nil(t, refs.ServiceAreas[0].Name)

	_, refs = buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailSimplified})
	assert.LessOrEqual(t, ringLength(refs.ServiceAreas[0].Geometry), geo.SimplifyMaxRingPoints)

	_, refs = buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})
	assert.Empty(t, refs.ServiceAreas[0].Geometry)
}

func TestBuildOnDemandServices_QueryPointDistances(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "alexandria-flex.zip"))

	_, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone, AreaDistances: newAreaDistances(api.GtfsManager.FlexIndex(), geoPoint{Lat: 38.836368, Lon: -77.049221})})
	require.NotNil(t, refs.ServiceAreas[0].DistanceToArea)
	assert.Equal(t, 0.0, *refs.ServiceAreas[0].DistanceToArea, "inside the zone")
	assert.Nil(t, refs.ServiceAreas[0].NearestPointOnBoundary)

	_, refs = buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone, AreaDistances: newAreaDistances(api.GtfsManager.FlexIndex(), geoPoint{Lat: 38.60, Lon: -77.20})})
	require.NotNil(t, refs.ServiceAreas[0].DistanceToArea)
	assert.InDelta(t, 1956, *refs.ServiceAreas[0].DistanceToArea, 25, "south of the zone's southern tip")
	require.NotNil(t, refs.ServiceAreas[0].NearestPointOnBoundary)
	assert.InDelta(t, -77.2022, refs.ServiceAreas[0].NearestPointOnBoundary[0], 0.01)
	assert.InDelta(t, 38.6175, refs.ServiceAreas[0].NearestPointOnBoundary[1], 0.001)
}

// twoAgencySharedZoneFiles has two agencies whose flex routes both use zone_x.
func twoAgencySharedZoneFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"a1,Agency One,http://example.com,UTC\na2,Agency Two,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"r1,a1,R1,Route One,3\nr2,a2,R2,Route Two,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt":         "stop_id,stop_name,stop_lat,stop_lon\n",
		"booking_rules.txt": "booking_rule_id,booking_type,prior_notice_last_day,prior_notice_last_time,prior_notice_service_id\nbr,2,1,17:00:00,ghost\n",
		"locations.geojson": `{"type":"FeatureCollection","features":[{"id":"zone_x","type":"Feature","properties":{"stop_name":"Shared"},"geometry":{"type":"Polygon","coordinates":[[[0,0],[0.1,0],[0.1,0.1],[0,0.1],[0,0]]]}}]}`,
		"trips.txt":         "route_id,service_id,trip_id\nr1,svc,t1\nr2,svc,t2\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,location_id,stop_sequence,pickup_type,drop_off_type,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_booking_rule_id,drop_off_booking_rule_id\n" +
			"t1,,,,zone_x,1,2,1,08:00:00,18:00:00,br,br\nt1,,,,zone_x,2,1,2,08:00:00,18:00:00,br,br\n" +
			"t2,,,,zone_x,1,2,1,09:00:00,17:00:00,br,br\nt2,,,,zone_x,2,1,2,09:00:00,17:00:00,br,br\n",
	}
}

func TestBuildOnDemandServices_TwoAgenciesSharingAZone(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "two-agency.zip", twoAgencySharedZoneFiles())
	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	assert.Equal(t, []string{"a1_r1", "a2_r2"}, ids(list, func(s models.OnDemandService) string { return s.ID }))
	assert.Equal(t, []string{"a1_zone_x"}, list[0].Rules[0].FromIds)
	assert.Equal(t, []string{"a2_zone_x"}, list[1].Rules[0].FromIds)
	assert.Equal(t, []string{"a1_zone_x", "a2_zone_x"}, ids(refs.ServiceAreas, func(a models.ServiceArea) string { return a.ID }),
		"each service prefixes the zone with its own agency")
	assert.Equal(t, []string{"a1_br", "a2_br"}, ids(refs.BookingRules, func(b models.BookingRule) string { return b.ID }))
	assert.Equal(t, []string{"a1", "a2"}, ids(refs.Agencies, func(a models.AgencyReference) string { return a.ID }))

	// prior_notice_service_id names a service with no calendar rows at all.
	assert.Nil(t, refs.BookingRules[0].PriorNoticeCalendarId, "an id with no emitted calendar would dangle")
	assert.Nil(t, refs.BookingRules[1].PriorNoticeCalendarId)
	assert.Equal(t, []string{"a1_svc", "a2_svc"}, ids(refs.Calendars, func(c models.OnDemandCalendar) string { return c.ID }),
		"a prior-notice service with no rows compiles to no calendar and no error")
}

func TestBuildOnDemandServices_ServiceWithOnlyInertRulesKeepsEmptyRules(t *testing.T) {
	files := twoAgencySharedZoneFiles()
	files["calendar.txt"] = "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
		"svc,0,0,0,0,0,0,0,20240101,20991231\n" +
		// Feed validation needs one active calendar; no trip uses this one.
		"unused,1,1,1,1,1,1,1,20240101,20991231\n"
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "inert-calendar.zip", files)

	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	require.Equal(t, []string{"a1_r1", "a2_r2"}, ids(list, func(s models.OnDemandService) string { return s.ID }))
	for _, service := range list {
		assert.NotNil(t, service.Rules, "rules is never null on the wire")
		assert.Empty(t, service.Rules, "a calendar with no service days yields no rule")
	}
	assert.Empty(t, refs.Calendars)
	assert.Equal(t, []string{"a1_zone_x", "a2_zone_x"}, ids(refs.ServiceAreas, func(a models.ServiceArea) string { return a.ID }),
		"the service still carries the zones its records reference")
}

// zeroRuleDeviatedFiles permits no pickups anywhere, so compilation yields no rules.
func zeroRuleDeviatedFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\nzr,Zero Rule,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\nhx,zr,HX,Hermann Degenerate,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt":         "stop_id,stop_name,stop_lat,stop_lon\nh1,Start,44.31,-94.46\nh2,End,44.33,-94.44\n",
		"booking_rules.txt": "booking_rule_id,booking_type\nbr_her,0\n",
		"locations.geojson": `{"type":"FeatureCollection","features":[{"id":"zone_a","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-94.47,44.30],[-94.43,44.30],[-94.43,44.34],[-94.47,44.34],[-94.47,44.30]]]}}]}`,
		"trips.txt":         "route_id,service_id,trip_id\nhx,svc,t1\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,location_id,stop_sequence,pickup_type,drop_off_type,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_booking_rule_id,drop_off_booking_rule_id\n" +
			"t1,09:00:00,09:00:00,h1,,1,1,0,,,,\n" +
			"t1,,,,zone_a,2,1,3,09:00:00,09:20:00,,br_her\n" +
			"t1,09:20:00,09:20:00,h2,,3,1,0,,,,\n",
	}
}

func TestBuildOnDemandServices_ZeroRuleServiceStillCarriesAreas(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "zero-rule.zip", zeroRuleDeviatedFiles())
	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	require.Len(t, list, 1)
	assert.Equal(t, "deviatedRoute", list[0].ServiceKind)
	assert.Equal(t, []models.AvailabilityRule{}, list[0].Rules)
	assert.Equal(t, []string{"zr_zone_a"}, ids(refs.ServiceAreas, func(a models.ServiceArea) string { return a.ID }))
	assert.Equal(t, []string{"zr_br_her"}, ids(refs.BookingRules, func(b models.BookingRule) string { return b.ID }))
	assert.Empty(t, refs.Stops, "no rule references a stop")
	assert.Empty(t, refs.Calendars, "no rule references a calendar")
}

func TestBuildOnDemandServices_NoServices(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	list, refs, err := api.buildOnDemandServices(context.Background(), nil, onDemandBuildOptions{})
	require.NoError(t, err)
	assert.Equal(t, []models.OnDemandService{}, list)
	assert.Equal(t, models.NewEmptyOnDemandReferences(), refs)
}

func TestBuildOnDemandServices_DanglingReferencesAreOmitted(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	for _, statement := range []string{
		"DELETE FROM locations WHERE id = 'gaylord'",
		"DELETE FROM booking_rules WHERE id = 'booking_rule_CC4'",
		"DELETE FROM location_group_stops WHERE location_group_id = 'CC_ironton_ferry_stops'",
		"DELETE FROM location_groups WHERE id = 'CC_ironton_ferry_stops'",
		"INSERT INTO ondemand_services (id, agency_id, route_id, service_kind) VALUES ('ghost', 'CC', 'ghost_route', 'zone')",
	} {
		_, err := api.GtfsManager.GtfsDB.DB.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}

	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	assert.Equal(t, []string{"CC_beaver_island", "CC_charlevoix_county", "CC_petoskey"},
		ids(refs.ServiceAreas, func(a models.ServiceArea) string { return a.ID }))
	var endpointIDs []string
	for _, rule := range list[1].Rules {
		endpointIDs = append(endpointIDs, rule.FromIds...)
		endpointIDs = append(endpointIDs, rule.ToIds...)
	}
	assert.Contains(t, endpointIDs, "CC_gaylord", "rules keep an id whose location row is missing")

	assert.Empty(t, refs.LocationGroups)
	assert.Empty(t, refs.Stops, "a missing group contributes no member stops")
	assert.Equal(t, []string{"CC_CC_ironton_ferry_stops"}, list[2].Rules[0].FromIds)

	assert.NotContains(t, ids(refs.BookingRules, func(b models.BookingRule) string { return b.ID }), "CC_booking_rule_CC4")

	ghost := list[len(list)-1]
	assert.Equal(t, "CC_ghost", ghost.ID)
	assert.Equal(t, "ghost_route", ghost.Name, "a service without a routes row falls back to its bare route id")
	assert.NotContains(t, ids(refs.Routes, func(r models.Route) string { return r.ID }), "CC_ghost_route")
}

func TestBuildOnDemandServices_MeasuresAgainstTheGivenSnapshot(t *testing.T) {
	api := alexandriaAPI(t)
	point := geoPoint{Lat: 38.60, Lon: -77.20} // ~2 km outside the real zone
	snapshot := gtfs.NewEmptyFlexIndex()
	snapshot.Areas["area_1449"] = &gtfs.FlexArea{ID: "area_1449", Polygons: [][][][2]float64{{{
		{-77.21, 38.59}, {-77.19, 38.59}, {-77.19, 38.61}, {-77.21, 38.61}, {-77.21, 38.59},
	}}}}

	_, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone, AreaDistances: newAreaDistances(snapshot, point)})

	require.Len(t, refs.ServiceAreas, 1)
	require.NotNil(t, refs.ServiceAreas[0].DistanceToArea)
	assert.Equal(t, 0.0, *refs.ServiceAreas[0].DistanceToArea, "the builder reuses the caller's snapshot rather than re-reading the index")
}

func TestBuildOnDemandServices_UnindexedAreaHasNoDistance(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	ctx := context.Background()
	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(ctx, "UPDATE locations SET geometry = 'not geojson' WHERE id = 'gaylord'")
	require.NoError(t, err)
	_, err = api.GtfsManager.ReloadStatic(ctx)
	require.NoError(t, err)

	_, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone, AreaDistances: newAreaDistances(api.GtfsManager.FlexIndex(), geoPoint{Lat: 45.3, Lon: -85.2})})

	for _, area := range refs.ServiceAreas {
		if area.ID == "CC_gaylord" {
			assert.Nil(t, area.DistanceToArea, "no indexed polygon to measure against")
			continue
		}
		assert.NotNil(t, area.DistanceToArea, area.ID)
	}
}

func TestBuildOnDemandServices_InvalidStoredGeometryIsOmitted(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	ctx, logs := contextCapturingLogs()
	_, err := api.GtfsManager.GtfsDB.DB.ExecContext(ctx, "UPDATE locations SET geometry = 'not geojson', geometry_simplified = NULL WHERE id = 'gaylord'")
	require.NoError(t, err)

	for _, detail := range []GeometryDetail{GeometryDetailFull, GeometryDetailSimplified} {
		_, refs := buildAllOnDemandServicesWithContext(t, ctx, api, onDemandBuildOptions{GeometryDetail: detail})

		for _, area := range refs.ServiceAreas {
			if area.ID != "CC_gaylord" {
				assert.NotEmpty(t, area.Geometry, area.ID)
				continue
			}
			assert.Nil(t, area.Geometry, "invalid JSON is not embedded")
			assert.NotEqual(t, [4]float64{}, area.BBox, "bbox and metadata survive")
		}
		_, err = json.Marshal(refs)
		assert.NoError(t, err, "one corrupt row must not break the response")
	}
	assert.Contains(t, logs.String(), "location_id=gaylord")
}

func TestBuildOnDemandServices_SimplifiedFallsBackToFullGeometry(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	var unsimplifiedID, fullGeometry string
	err := api.GtfsManager.GtfsDB.DB.QueryRowContext(context.Background(),
		"SELECT id, geometry FROM locations WHERE geometry_simplified IS NULL ORDER BY id LIMIT 1").Scan(&unsimplifiedID, &fullGeometry)
	require.NoError(t, err, "fixture needs a zone that already fits the display target")

	_, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailSimplified})

	for _, area := range refs.ServiceAreas {
		if area.ID == "CC_"+unsimplifiedID {
			assert.JSONEq(t, fullGeometry, string(area.Geometry))
			return
		}
	}
	t.Fatalf("area CC_%s not in references", unsimplifiedID)
}

// deviatedRouteWithStopsFiles has a deviated route whose rules start and end
// at timed stops that a second, fixed route also serves.
func deviatedRouteWithStopsFiles() map[string]string {
	files := zeroRuleDeviatedFiles()
	files["routes.txt"] += "tt,zr,TT,Timed,3\n"
	files["trips.txt"] += "tt,svc,t2\n"
	files["stop_times.txt"] = "trip_id,arrival_time,departure_time,stop_id,location_id,stop_sequence,pickup_type,drop_off_type,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_booking_rule_id,drop_off_booking_rule_id\n" +
		"t1,09:00:00,09:00:00,h1,,1,0,1,,,,\n" +
		"t1,,,,zone_a,2,2,2,09:00:00,09:20:00,br_her,br_her\n" +
		"t1,09:20:00,09:20:00,h2,,3,1,0,,,,\n" +
		"t2,10:00:00,10:00:00,h1,,1,0,0,,,,\n" +
		"t2,10:20:00,10:20:00,h2,,2,0,0,,,,\n"
	return files
}

func TestBuildOnDemandServices_RuleStopsBringTheirRoutes(t *testing.T) {
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "deviated-stops.zip", deviatedRouteWithStopsFiles())
	list, refs := buildAllOnDemandServices(t, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	require.Len(t, list, 1)
	require.NotEmpty(t, list[0].Rules)
	assert.Equal(t, []string{"zr_h1", "zr_h2"}, ids(refs.Stops, func(s models.Stop) string { return s.ID }))
	assert.ElementsMatch(t, []string{"zr_hx", "zr_tt"}, refs.Stops[0].RouteIDs)
	assert.Equal(t, []string{"zr_hx", "zr_tt"}, ids(refs.Routes, func(r models.Route) string { return r.ID }),
		"the service's own route appears once; the fixed route serving its stops joins it")
	assert.Equal(t, []string{"zr"}, ids(refs.Agencies, func(a models.AgencyReference) string { return a.ID }))
}

func TestOnDemandServiceModel_NameFallbacks(t *testing.T) {
	service := gtfsdb.OndemandService{ID: "svc", AgencyID: "ag", RouteID: "r9", ServiceKind: "zone"}
	tests := []struct {
		name  string
		route gtfsdb.Route
		want  string
	}{
		{"long name wins", gtfsdb.Route{ShortName: nulls.String("S"), LongName: nulls.String("Long")}, "Long"},
		{"short name when long is empty", gtfsdb.Route{ShortName: nulls.String("S"), LongName: nulls.String("")}, "S"},
		{"bare route id when both are missing", gtfsdb.Route{}, "r9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := onDemandServiceModel(service, tt.route, []models.AvailabilityRule{})
			assert.Equal(t, tt.want, model.Name)
			assert.Equal(t, "ag_svc", model.ID)
			assert.Equal(t, "ag_r9", *model.RouteID)
			assert.Nil(t, model.Description)
			assert.Nil(t, model.URL)
		})
	}
}

func TestBuildOnDemandServices_PriorNoticeCalendarMustResolve(t *testing.T) {
	files := twoAgencySharedZoneFiles()
	files["booking_rules.txt"] = "booking_rule_id,booking_type,prior_notice_last_day,prior_notice_last_time,prior_notice_service_id\n" +
		"br,2,1,17:00:00,holiday\nbr_svc,2,1,17:00:00,svc\n"
	files["calendar_dates.txt"] = "service_id,date,exception_type\nholiday,20260704,1\n"
	files["stop_times.txt"] = strings.ReplaceAll(files["stop_times.txt"], "09:00:00,17:00:00,br,br", "09:00:00,17:00:00,br_svc,br_svc")
	api := createTestApiWithGTFSFixture(t, clock.RealClock{}, "prior-notice-dates.zip", files)
	ctx, logs := contextCapturingLogs()

	_, refs := buildAllOnDemandServicesWithContext(t, ctx, api, onDemandBuildOptions{GeometryDetail: GeometryDetailNone})

	require.Equal(t, []string{"a1_br", "a2_br_svc"}, ids(refs.BookingRules, func(b models.BookingRule) string { return b.ID }))
	assert.NotContains(t, ids(refs.Calendars, func(c models.OnDemandCalendar) string { return c.ID }), "a1_holiday",
		"a calendar_dates-only service has no base calendar")
	assert.Nil(t, refs.BookingRules[0].PriorNoticeCalendarId, "an id with no emitted calendar is dropped")
	assert.Equal(t, "a2_svc", *refs.BookingRules[1].PriorNoticeCalendarId, "an id that resolves is kept")
	assert.Empty(t, logs.String(), "the dangling id is logged once per reload by buildFlexIndex, not per request")
}
