package gtfsdb

import (
	"os"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/flexfixtures"
)

var (
	compileAgency = gtfs.Agency{Id: "ag", Name: "Compile Agency"}
	compileRoute  = gtfs.Route{Id: "r1", Agency: &compileAgency}
	weekdaySvc    = gtfs.Service{Id: "weekday", Monday: true}
	saturdaySvc   = gtfs.Service{Id: "saturday", Saturday: true}
	zoneA         = gtfs.Location{Id: "zone_a"}
	zoneB         = gtfs.Location{Id: "zone_b"}
	bookingX      = gtfs.BookingRule{Id: "bx", Type: 1}
)

func windowed(seq int, pickup, dropOff gtfs.PickupDropOffPolicy, start, end time.Duration) gtfs.ScheduledStopTime {
	return gtfs.ScheduledStopTime{
		StopSequence:             seq,
		PickupType:               pickup,
		DropOffType:              dropOff,
		StartPickupDropOffWindow: &start,
		EndPickupDropOffWindow:   &end,
		PickupBookingRule:        &bookingX,
		DropOffBookingRule:       &bookingX,
	}
}

func zoneRecord(zone *gtfs.Location, seq int, pickup, dropOff gtfs.PickupDropOffPolicy, start, end time.Duration) gtfs.ScheduledStopTime {
	st := windowed(seq, pickup, dropOff, start, end)
	st.Location = zone
	return st
}

func groupRecord(group *gtfs.LocationGroup, seq int, pickup, dropOff gtfs.PickupDropOffPolicy, start, end time.Duration) gtfs.ScheduledStopTime {
	st := windowed(seq, pickup, dropOff, start, end)
	st.LocationGroup = group
	return st
}

func timedRecord(stop *gtfs.Stop, seq int, at time.Duration, pickup, dropOff gtfs.PickupDropOffPolicy) gtfs.ScheduledStopTime {
	return gtfs.ScheduledStopTime{Stop: stop, StopSequence: seq, ArrivalTime: at, DepartureTime: at, PickupType: pickup, DropOffType: dropOff}
}

func compileStatic(trips ...gtfs.ScheduledTrip) *gtfs.Static {
	return &gtfs.Static{Agencies: []gtfs.Agency{compileAgency}, Routes: []gtfs.Route{compileRoute}, Trips: trips}
}

func trip(id string, svc *gtfs.Service, records ...gtfs.ScheduledStopTime) gtfs.ScheduledTrip {
	return gtfs.ScheduledTrip{ID: id, Route: &compileRoute, Service: svc, StopTimes: records}
}

func int64Ptr(v int64) *int64 { return &v }

const (
	fiveAM  = 5 * time.Hour
	sixPM   = 18 * time.Hour
	nineAM  = 9 * time.Hour
	ninePM  = 21 * time.Hour
	pickup  = gtfs.PickupDropOffPolicy_PhoneAgency
	noPick  = gtfs.PickupDropOffPolicy_No
	coord   = gtfs.PickupDropOffPolicy_CoordinateWithDriver
	regular = gtfs.PickupDropOffPolicy_Yes
)

func TestCompileOnDemand_Patterns(t *testing.T) {
	group := gtfs.LocationGroup{Id: "grp", Stops: []*gtfs.Stop{{Id: "s1"}, {Id: "s2"}, {Id: "s3"}}}
	h1, h2, h3 := &gtfs.Stop{Id: "h1"}, &gtfs.Stop{Id: "h2"}, &gtfs.Stop{Id: "h3"}
	w1 := &gtfs.Stop{Id: "w1"}

	tests := []struct {
		name         string
		static       *gtfs.Static
		wantKind     string
		wantRules    int
		wantStops    map[string][]string
		wantServices int
	}{
		{
			name: "single zone dial-a-ride",
			static: compileStatic(trip("t1", &weekdaySvc,
				zoneRecord(&zoneA, 1, pickup, noPick, fiveAM, sixPM),
				zoneRecord(&zoneA, 2, noPick, pickup, fiveAM, sixPM))),
			wantKind: ServiceKindZone, wantRules: 1, wantStops: map[string][]string{}, wantServices: 1,
		},
		{
			name: "zone to zone",
			static: compileStatic(trip("t1", &weekdaySvc,
				zoneRecord(&zoneA, 1, pickup, noPick, fiveAM, sixPM),
				zoneRecord(&zoneB, 2, noPick, pickup, fiveAM, sixPM))),
			wantKind: ServiceKindZoneToZone, wantRules: 1, wantStops: map[string][]string{}, wantServices: 1,
		},
		{
			name: "location group",
			static: compileStatic(trip("t1", &weekdaySvc,
				groupRecord(&group, 1, pickup, noPick, nineAM, ninePM),
				groupRecord(&group, 2, noPick, pickup, nineAM, ninePM))),
			wantKind:     ServiceKindStopGroup,
			wantRules:    1,
			wantStops:    map[string][]string{"s1": {"r1"}, "s2": {"r1"}, "s3": {"r1"}},
			wantServices: 1,
		},
		{
			name: "deviated route pairs timed pickups with later zone drop-offs",
			static: compileStatic(trip("t1", &weekdaySvc,
				timedRecord(h1, 1, nineAM, regular, regular),
				zoneRecord(&zoneA, 2, noPick, coord, nineAM, nineAM+20*time.Minute),
				timedRecord(h2, 3, nineAM+20*time.Minute, regular, regular),
				zoneRecord(&zoneA, 4, noPick, coord, nineAM+20*time.Minute, nineAM+40*time.Minute),
				timedRecord(h3, 5, nineAM+40*time.Minute, regular, regular))),
			wantKind:     ServiceKindDeviatedRoute,
			wantRules:    3, // h1->zone(2), h1->zone(4), h2->zone(4); timed->timed pairs are skipped
			wantStops:    map[string][]string{"h1": {"r1"}, "h2": {"r1"}},
			wantServices: 1,
		},
		{
			name: "degenerate deviated route with no pickups still yields a service",
			static: compileStatic(trip("t1", &weekdaySvc,
				timedRecord(h1, 1, nineAM, noPick, regular),
				zoneRecord(&zoneA, 2, noPick, coord, nineAM, ninePM),
				timedRecord(h2, 3, ninePM, noPick, regular))),
			wantKind: ServiceKindDeviatedRoute, wantRules: 0, wantStops: map[string][]string{}, wantServices: 1,
		},
		{
			name: "windowed stop records with no zone or group are unknown",
			static: compileStatic(trip("t1", &weekdaySvc,
				func() gtfs.ScheduledStopTime {
					st := windowed(1, pickup, noPick, fiveAM, sixPM)
					st.Stop = w1
					return st
				}(),
				func() gtfs.ScheduledStopTime {
					st := windowed(2, noPick, pickup, fiveAM, sixPM)
					st.Stop = w1
					return st
				}())),
			wantKind: ServiceKindUnknown, wantRules: 1, wantStops: map[string][]string{"w1": {"r1"}}, wantServices: 1,
		},
		{
			name:         "route with no flex record is not a service",
			static:       compileStatic(trip("t1", &weekdaySvc, timedRecord(h1, 1, nineAM, regular, regular), timedRecord(h2, 2, ninePM, regular, regular))),
			wantServices: 0, wantStops: map[string][]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled := CompileOnDemand(tt.static)
			require.Len(t, compiled.Services, tt.wantServices)
			assert.Equal(t, tt.wantStops, compiled.StopServices)
			if tt.wantServices == 0 {
				assert.Empty(t, compiled.Rules)
				return
			}
			assert.Equal(t, CompiledService{ID: "r1", AgencyID: "ag", RouteID: "r1", Kind: tt.wantKind}, compiled.Services[0])
			assert.Len(t, compiled.Rules, tt.wantRules)
		})
	}
}

func TestCompileOnDemand_SideSplitAndPointWindows(t *testing.T) {
	h1 := &gtfs.Stop{Id: "h1"}
	bookingDrop := gtfs.BookingRule{Id: "bdrop", Type: 2}
	zone := zoneRecord(&zoneA, 2, noPick, coord, nineAM, nineAM+20*time.Minute)
	zone.PickupBookingRule = nil
	zone.DropOffBookingRule = &bookingDrop
	factor, offset := 1.5, 120.0
	zone.SafeDurationFactor = &factor
	zone.SafeDurationOffset = &offset

	compiled := CompileOnDemand(compileStatic(trip("t1", &weekdaySvc, timedRecord(h1, 1, nineAM, regular, regular), zone)))
	require.Len(t, compiled.Rules, 1)
	rule := compiled.Rules[0]

	assert.Equal(t, "h1", rule.FromID)
	assert.Equal(t, EndpointStop, rule.FromKind)
	assert.Equal(t, "zone_a", rule.ToID)
	assert.Equal(t, EndpointLocation, rule.ToKind)
	assert.Equal(t, int64Ptr(int64(nineAM)), rule.StartPickupTime, "a timed pickup is a point window at its departure time")
	assert.Equal(t, int64Ptr(int64(nineAM)), rule.EndPickupTime)
	assert.Equal(t, int64Ptr(int64(nineAM+20*time.Minute)), rule.EndDropOffTime)
	assert.Equal(t, int64(0), rule.PickupType)
	assert.Equal(t, int64(3), rule.DropOffType)
	assert.Nil(t, rule.PickupBookingRuleID, "pickup-side fields come from the pickup record")
	require.NotNil(t, rule.DropOffBookingRuleID)
	assert.Equal(t, "bdrop", *rule.DropOffBookingRuleID)
	assert.Equal(t, "weekday", rule.GTFSServiceID)
	assert.Equal(t, "t1", rule.TripID)
	require.NotNil(t, rule.SafeDurationFactor)
	assert.Equal(t, 1.5, *rule.SafeDurationFactor, "no trip or pickup value, so the drop-off record supplies it")
	assert.Equal(t, 120.0, *rule.SafeDurationOffset)
}

func TestCompileOnDemand_SafeDurationPrecedence(t *testing.T) {
	tripFactor, pickFactor, dropFactor := 2.0, 3.0, 4.0
	pick := zoneRecord(&zoneA, 1, pickup, noPick, fiveAM, sixPM)
	pick.SafeDurationFactor = &pickFactor
	drop := zoneRecord(&zoneA, 2, noPick, pickup, fiveAM, sixPM)
	drop.SafeDurationFactor = &dropFactor

	withTrip := trip("t1", &weekdaySvc, pick, drop)
	withTrip.SafeDurationFactor = &tripFactor
	compiled := CompileOnDemand(compileStatic(withTrip))
	require.Len(t, compiled.Rules, 1)
	assert.Equal(t, 2.0, *compiled.Rules[0].SafeDurationFactor, "trips.txt wins")
	assert.Nil(t, compiled.Rules[0].SafeDurationOffset, "each field resolves independently")

	compiled = CompileOnDemand(compileStatic(trip("t1", &weekdaySvc, pick, drop)))
	assert.Equal(t, 3.0, *compiled.Rules[0].SafeDurationFactor, "the pickup record beats the drop-off record")
}

func TestCompileOnDemand_DedupAndCalendars(t *testing.T) {
	same := func(id string, svc *gtfs.Service) gtfs.ScheduledTrip {
		return trip(id, svc,
			zoneRecord(&zoneA, 1, pickup, noPick, fiveAM, sixPM),
			zoneRecord(&zoneA, 2, noPick, pickup, fiveAM, sixPM))
	}

	compiled := CompileOnDemand(compileStatic(same("t1", &weekdaySvc), same("t2", &weekdaySvc)))
	require.Len(t, compiled.Rules, 1, "identical tuples on the same calendar collapse")
	assert.Equal(t, "t1", compiled.Rules[0].TripID, "the first trip is the representative")
	assert.Nil(t, compiled.Rules[0].EndDropOffTime, "endDropOffTime is null when equal to endPickupTime")

	compiled = CompileOnDemand(compileStatic(same("t1", &weekdaySvc), same("t2", &saturdaySvc)))
	require.Len(t, compiled.Rules, 2, "one row per (tuple, gtfs_service_id)")
	assert.Equal(t, "saturday", compiled.Rules[0].GTFSServiceID, "rules sort by service id within a service")
	assert.Equal(t, "weekday", compiled.Rules[1].GTFSServiceID)
}

func parseFixtureZip(t *testing.T, path string) *gtfs.Static {
	t.Helper()
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := ParseGtfsData(bytes, path)
	require.NoError(t, err)
	return parsed.Static
}

func rulesForService(rules []CompiledRule, serviceID string) []CompiledRule {
	var out []CompiledRule
	for _, r := range rules {
		if r.ServiceID == serviceID {
			out = append(out, r)
		}
	}
	return out
}

func TestCompileOnDemand_Alexandria(t *testing.T) {
	compiled := CompileOnDemand(parseFixtureZip(t, "../testdata/alexandria-flex.zip"))

	require.Equal(t, []CompiledService{{ID: "77652", AgencyID: "5088", RouteID: "77652", Kind: ServiceKindZone}}, compiled.Services)
	assert.Empty(t, compiled.StopServices, "stop 4258639 is referenced by no record")

	booking := "booking_route_77652"
	one, zero := 1.0, 0.0
	want := []CompiledRule{
		{
			ServiceID: "77652", TripID: "t_6124961_b_85952_tn_0",
			FromID: "area_1449", FromKind: EndpointLocation, ToID: "area_1449", ToKind: EndpointLocation,
			StartPickupTime: int64Ptr(int64(5 * time.Hour)), EndPickupTime: int64Ptr(int64(24*time.Hour + 50*time.Minute)),
			EndDropOffTime: int64Ptr(int64(25 * time.Hour)),
			GTFSServiceID:  "c_71675_b_85952_d_63", PickupType: 2, DropOffType: 2,
			PickupBookingRuleID: &booking, DropOffBookingRuleID: &booking,
			SafeDurationFactor: &one, SafeDurationOffset: &zero,
		},
		{
			ServiceID: "77652", TripID: "t_6124409_b_85952_tn_0",
			FromID: "area_1449", FromKind: EndpointLocation, ToID: "area_1449", ToKind: EndpointLocation,
			StartPickupTime: int64Ptr(int64(7 * time.Hour)), EndPickupTime: int64Ptr(int64(24*time.Hour + 50*time.Minute)),
			EndDropOffTime: int64Ptr(int64(25 * time.Hour)),
			GTFSServiceID:  "c_71675_b_85952_d_64", PickupType: 2, DropOffType: 2,
			PickupBookingRuleID: &booking, DropOffBookingRuleID: &booking,
			SafeDurationFactor: &one, SafeDurationOffset: &zero,
		},
	}
	assert.Equal(t, want, compiled.Rules)
}

func TestCompileOnDemand_RealMichiganFeeds(t *testing.T) {
	t.Run("manistee", func(t *testing.T) {
		compiled := CompileOnDemand(parseFixtureZip(t, "../testdata/manistee-flex.zip"))
		require.Equal(t, []CompiledService{
			{ID: "MC1", AgencyID: "MC", RouteID: "MC1", Kind: ServiceKindZone},
			{ID: "MC2", AgencyID: "MC", RouteID: "MC2", Kind: ServiceKindZoneToZone},
		}, compiled.Services, "MC3 is timed-only and MC4 has no trips")
		assert.Len(t, rulesForService(compiled.Rules, "MC1"), 2)
		assert.Len(t, rulesForService(compiled.Rules, "MC2"), 18)
		assert.Empty(t, compiled.StopServices)
		for _, rule := range rulesForService(compiled.Rules, "MC1") {
			assert.Nil(t, rule.EndDropOffTime)
			assert.Equal(t, 2.0, *rule.SafeDurationFactor)
			assert.Equal(t, 30.0, *rule.SafeDurationOffset)
		}
	})
	t.Run("charlevoix", func(t *testing.T) {
		compiled := CompileOnDemand(parseFixtureZip(t, "../testdata/charlevoix-flex.zip"))
		require.Equal(t, []CompiledService{
			{ID: "CC1", AgencyID: "CC", RouteID: "CC1", Kind: ServiceKindZone},
			{ID: "CC2_med", AgencyID: "CC", RouteID: "CC2_med", Kind: ServiceKindZoneToZone},
			{ID: "CC3", AgencyID: "CC", RouteID: "CC3", Kind: ServiceKindStopGroup},
			{ID: "CC4", AgencyID: "CC", RouteID: "CC4", Kind: ServiceKindZone},
		}, compiled.Services)
		assert.Len(t, rulesForService(compiled.Rules, "CC1"), 2, "one row per calendar; merged at the API layer")
		assert.Len(t, rulesForService(compiled.Rules, "CC2_med"), 8)
		assert.Len(t, rulesForService(compiled.Rules, "CC3"), 1)
		assert.Len(t, rulesForService(compiled.Rules, "CC4"), 2)
		assert.Equal(t, map[string][]string{"CC_Ironton_Ferry_East": {"CC3"}, "CC_Ironton_Ferry_West": {"CC3"}}, compiled.StopServices)
	})
	t.Run("zero stops", func(t *testing.T) {
		parsed, err := ParseGtfsData(flexfixtures.ZipBytes(t, flexfixtures.ZeroStopsFiles()), "flex-zero-stops")
		require.NoError(t, err)
		compiled := CompileOnDemand(parsed.Static)
		require.Len(t, compiled.Services, 2)
		assert.Equal(t, ServiceKindZoneToZone, compiled.Services[0].Kind)
		assert.Equal(t, ServiceKindZoneToZone, compiled.Services[1].Kind)
		assert.Len(t, rulesForService(compiled.Rules, "AP1"), 18, "9 per calendar")
		assert.Len(t, rulesForService(compiled.Rules, "AP2_med"), 4)
	})
}

func TestCompileOnDemand_GroupDeviatedFixture(t *testing.T) {
	parsed, err := ParseGtfsData(flexfixtures.ZipBytes(t, flexfixtures.GroupDeviatedFiles()), "flex-group-deviated")
	require.NoError(t, err)
	compiled := CompileOnDemand(parsed.Static)

	require.Equal(t, []CompiledService{
		{ID: "hermann", AgencyID: "gd", RouteID: "hermann", Kind: ServiceKindDeviatedRoute},
		{ID: "rufbus", AgencyID: "gd", RouteID: "rufbus", Kind: ServiceKindStopGroup},
		{ID: "winstop", AgencyID: "gd", RouteID: "winstop", Kind: ServiceKindZone},
	}, compiled.Services, "a windowed stop record beside one zone classifies as zone")
	assert.Len(t, rulesForService(compiled.Rules, "hermann"), 3, "blank pickup_type cells default to 0, so timed stops board")
	assert.Len(t, rulesForService(compiled.Rules, "rufbus"), 1)
	assert.Len(t, rulesForService(compiled.Rules, "winstop"), 1)
	assert.Equal(t, map[string][]string{
		"h1": {"hermann"}, "h2": {"hermann"},
		"s1": {"rufbus"}, "s2": {"rufbus"}, "s3": {"rufbus"},
		"w1": {"winstop"},
	}, compiled.StopServices)
}

func TestComparePtr(t *testing.T) {
	one, two := int64(1), int64(2)
	assert.Equal(t, 0, ComparePtr[int64](nil, nil))
	assert.Equal(t, -1, ComparePtr(nil, &one))
	assert.Equal(t, 1, ComparePtr(&one, nil))
	assert.Equal(t, -1, ComparePtr(&one, &two))
	assert.Equal(t, 1, ComparePtr(&two, &one))
	assert.Equal(t, 0, ComparePtr(&one, &one))

	early, late := "08:00:00", "17:30:00"
	assert.Equal(t, -1, ComparePtr(&early, &late))
}
