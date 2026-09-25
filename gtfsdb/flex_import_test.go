package gtfsdb

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/geo"
)

// newTestClientWithZip imports a GTFS zip from disk into a fresh in-memory client.
func newTestClientWithZip(t *testing.T, zipPath string) *Client {
	t.Helper()
	bytes, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	return newTestClientWithBytes(t, bytes, zipPath)
}

// newTestClientWithBytes imports GTFS zip bytes into a fresh in-memory client.
func newTestClientWithBytes(t *testing.T, zipBytes []byte, source string) *Client {
	t.Helper()
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	parsed, err := ParseGtfsData(zipBytes, source)
	require.NoError(t, err)
	_, err = client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	return client
}

func countRows(t *testing.T, client *Client, table string) int {
	t.Helper()
	var n int
	require.NoError(t, client.DB.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestStoreGtfsData_AlexandriaFlexImports(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/alexandria-flex.zip")

	assert.Equal(t, 1, countRows(t, client, "agencies"))
	assert.Equal(t, 1, countRows(t, client, "routes"))
	assert.Equal(t, 1, countRows(t, client, "stops"))
	assert.Equal(t, 2, countRows(t, client, "trips"))
	assert.Equal(t, 0, countRows(t, client, "stop_times"), "windowed records never land in stop_times")

	trip, err := client.Queries.GetTrip(context.Background(), "t_6124961_b_85952_tn_0")
	require.NoError(t, err)
	assert.False(t, trip.MinArrivalTime.Valid, "a flex-only trip has no cached time bounds")
}

func TestStoreGtfsData_ZeroStopsFeedImports(t *testing.T) {
	client := newTestClientWithBytes(t, flexfixtures.ZipBytes(t, flexfixtures.ZeroStopsFiles()), "flex-zero-stops")

	assert.Equal(t, 0, countRows(t, client, "stops"))
	assert.Equal(t, 22, countRows(t, client, "trips"))
	assert.Equal(t, 0, countRows(t, client, "stop_times"))
	assert.Equal(t, 0, countRows(t, client, "block_layover"))
}

func TestStoreGtfsData_DeviatedFeedIndexesTimedRecordsOnly(t *testing.T) {
	client := newTestClientWithBytes(t, flexfixtures.ZipBytes(t, flexfixtures.GroupDeviatedFiles()), "flex-group-deviated")

	assert.Equal(t, 6, countRows(t, client, "stop_times"), "3 timed rows on her-trip + 3 on her-trip-2")
	assert.Equal(t, 7, countRows(t, client, "stops"))
	assert.Equal(t, 4, countRows(t, client, "trips"), "the group and windowed-stop trips survive validation")

	// her-trip ends at h3 09:40 and her-trip-2 starts at h3 10:00 in the same block:
	// the layover index must be built from the timed records around the zone rows.
	assert.Equal(t, 1, countRows(t, client, "block_layover"))

	var indexed int
	require.NoError(t, client.DB.QueryRow(
		"SELECT COUNT(*) FROM block_trip_entry WHERE trip_id IN ('her-trip','her-trip-2')").Scan(&indexed))
	assert.Equal(t, 2, indexed)
	require.NoError(t, client.DB.QueryRow(
		"SELECT COUNT(*) FROM block_trip_entry WHERE trip_id IN ('ruf-trip','win-trip')").Scan(&indexed))
	assert.Equal(t, 0, indexed, "trips with no timed records are skipped by the block index")
}

func TestStoreGtfsData_FlexTablesPopulated(t *testing.T) {
	tests := []struct {
		name   string
		zip    string
		counts map[string]int
	}{
		{
			name: "alexandria",
			zip:  "../testdata/alexandria-flex.zip",
			counts: map[string]int{
				"booking_rules": 1, "locations": 1, "location_groups": 0, "location_group_stops": 0,
				"flex_stop_times": 4, "stop_times": 0, "stops": 1,
			},
		},
		{
			name: "manistee",
			zip:  "../testdata/manistee-flex.zip",
			counts: map[string]int{
				"booking_rules": 8, "locations": 7, "location_groups": 0, "location_group_stops": 0,
				"flex_stop_times": 40, "stop_times": 24, "stops": 4, "trips": 26,
			},
		},
		{
			name: "charlevoix",
			zip:  "../testdata/charlevoix-flex.zip",
			counts: map[string]int{
				"booking_rules": 5, "locations": 4, "location_groups": 1, "location_group_stops": 2,
				"flex_stop_times": 26, "stop_times": 0, "stops": 2, "trips": 13,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClientWithZip(t, tt.zip)
			for table, want := range tt.counts {
				assert.Equal(t, want, countRows(t, client, table), table)
			}
		})
	}
}

func TestStoreGtfsData_AlexandriaLocationRow(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/alexandria-flex.zip")
	ctx := context.Background()

	locations, err := client.Queries.GetLocationsByIDs(ctx, []string{"area_1449"})
	require.NoError(t, err)
	require.Len(t, locations, 1)
	loc := locations[0]

	assert.InDelta(t, -77.5372039, loc.MinLon, 1e-9)
	assert.InDelta(t, 38.617508, loc.MinLat, 1e-9)
	assert.InDelta(t, -76.9092198, loc.MaxLon, 1e-9)
	assert.InDelta(t, 39.057831, loc.MaxLat, 1e-9)
	assert.False(t, loc.Name.Valid, "the feed publishes no stop_name for the zone")

	_, full, err := geo.ParseGeoJSONPolygons([]byte(loc.Geometry))
	require.NoError(t, err)
	assert.Len(t, full[0][0], 4239, "geometry is stored verbatim")

	require.True(t, loc.GeometrySimplified.Valid, "a 4,239-point ring must be simplified")
	_, simplified, err := geo.ParseGeoJSONPolygons([]byte(loc.GeometrySimplified.String))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(simplified[0][0]), geo.SimplifyMaxRingPoints)

	rule, err := client.Queries.GetBookingRulesByIDs(ctx, []string{"booking_route_77652"})
	require.NoError(t, err)
	require.Len(t, rule, 1)
	assert.Equal(t, int64(2), rule[0].BookingType)
	assert.Equal(t, int64(1), rule[0].PriorNoticeLastDay.Int64)
	assert.Equal(t, int64(17*time.Hour), rule[0].PriorNoticeLastTime.Int64)
	assert.Equal(t, int64(14), rule[0].PriorNoticeStartDay.Int64)
	assert.Equal(t, int64(0), rule[0].PriorNoticeStartTime.Int64)
	assert.True(t, rule[0].PriorNoticeStartTime.Valid, "00:00:00 is a real value, not NULL")
	assert.False(t, rule[0].PriorNoticeDurationMin.Valid)
	assert.False(t, rule[0].PriorNoticeServiceID.Valid, "empty strings are stored as NULL")
	assert.True(t, strings.HasPrefix(rule[0].Message.String, "DOT is the City of Alexandria"))
	assert.Equal(t, "703-746-5222", rule[0].PhoneNumber.String)

	var factor, offset sql.NullFloat64
	require.NoError(t, client.DB.QueryRowContext(ctx,
		"SELECT safe_duration_factor, safe_duration_offset FROM flex_stop_times WHERE trip_id = 't_6124961_b_85952_tn_0' AND stop_sequence = 1").
		Scan(&factor, &offset))
	assert.Equal(t, sql.NullFloat64{Float64: 1, Valid: true}, factor)
	assert.Equal(t, sql.NullFloat64{Float64: 0, Valid: true}, offset, "0.0 is a real value, not NULL")
}

func TestStoreGtfsData_CharlevoixSmallZonesAreNotSimplified(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/charlevoix-flex.zip")
	locations, err := client.Queries.GetLocationsByIDs(context.Background(), []string{"gaylord", "petoskey", "charlevoix_county"})
	require.NoError(t, err)
	require.Len(t, locations, 3)
	byID := map[string]Location{}
	for _, loc := range locations {
		byID[loc.ID] = loc
	}
	assert.False(t, byID["gaylord"].GeometrySimplified.Valid, "a 5-point ring already meets the target")
	assert.False(t, byID["petoskey"].GeometrySimplified.Valid)
	assert.True(t, byID["charlevoix_county"].GeometrySimplified.Valid)

	groupStops, err := client.Queries.GetLocationGroupStopsForGroups(context.Background(), []string{"CC_ironton_ferry_stops"})
	require.NoError(t, err)
	require.Len(t, groupStops, 2)
	assert.Equal(t, "CC_Ironton_Ferry_East", groupStops[0].StopID)
	assert.Equal(t, "CC_Ironton_Ferry_West", groupStops[1].StopID)

	var kind int64
	require.NoError(t, client.DB.QueryRow(
		"SELECT pickup_type FROM flex_stop_times WHERE trip_id = 'CC3_mon-tues-wed-thurs-fri-sat-sun' AND stop_sequence = 1").Scan(&kind))
	assert.Equal(t, int64(2), kind)
}

func squareLocation(id, name string) gtfs.Location {
	ring := [][2]float64{{-122.0, 47.0}, {-121.9, 47.0}, {-121.9, 47.1}, {-122.0, 47.1}, {-122.0, 47.0}}
	return gtfs.Location{
		Id:   id,
		Name: name,
		Geometry: gtfs.LocationGeometry{
			Type:     geo.GeoJSONPolygon,
			Polygons: [][][][2]float64{{ring}},
			Raw:      []byte(`{"type":"Polygon","coordinates":[[[-122,47],[-121.9,47],[-121.9,47.1],[-122,47.1],[-122,47]]]}`),
		},
	}
}

func TestStoreFlexEntities_DuplicatesAndUnusableRecords(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	_, err = client.Queries.CreateStop(ctx, CreateStopParams{ID: "member", Lat: 47.05, Lon: -121.95})
	require.NoError(t, err)
	member := &gtfs.Stop{Id: "member"}
	uninserted := &gtfs.Stop{Id: "no-coordinates"}

	staticData := &gtfs.Static{
		BookingRules: []gtfs.BookingRule{
			{Id: "rule", PhoneNumber: "first"},
			{Id: "rule", PhoneNumber: "last"},
		},
		Locations: []gtfs.Location{
			squareLocation("zone", "first"),
			squareLocation("zone", "last"),
			{Id: "empty", Geometry: gtfs.LocationGeometry{Type: geo.GeoJSONPolygon}},
		},
		LocationGroups: []gtfs.LocationGroup{
			{Id: "group", Name: "first"},
			{Id: "group", Name: "last", Stops: []*gtfs.Stop{member, member, uninserted}},
		},
	}
	insertedStopIDs := map[string]struct{}{"member": {}}

	require.NoError(t, client.storeFlexEntities(ctx, staticData, insertedStopIDs, client.Queries))

	rules, err := client.Queries.GetBookingRulesByIDs(ctx, []string{"rule"})
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "last", rules[0].PhoneNumber.String, "the last duplicate wins, as go-gtfs resolves references")

	locations, err := client.Queries.GetLocationsByIDs(ctx, []string{"zone", "empty"})
	require.NoError(t, err)
	require.Len(t, locations, 1, "a location with no polygons is skipped, not fatal")
	assert.Equal(t, "last", locations[0].Name.String)

	groups, err := client.Queries.GetLocationGroupsByIDs(ctx, []string{"group"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "last", groups[0].Name.String)

	groupStops, err := client.Queries.GetLocationGroupStopsForGroups(ctx, []string{"group"})
	require.NoError(t, err)
	require.Len(t, groupStops, 1, "duplicate and uninserted members are dropped")
	assert.Equal(t, "member", groupStops[0].StopID)
}

func TestStoreGtfsData_OnDemandTablesAndStopAgencies(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/charlevoix-flex.zip")
	ctx := context.Background()

	assert.Equal(t, 4, countRows(t, client, "ondemand_services"))
	assert.Equal(t, 13, countRows(t, client, "ondemand_rules"))
	assert.Equal(t, 2, countRows(t, client, "ondemand_stop_services"))

	service, err := client.Queries.GetOnDemandService(ctx, "CC3")
	require.NoError(t, err)
	assert.Equal(t, OndemandService{ID: "CC3", AgencyID: "CC", RouteID: "CC3", ServiceKind: ServiceKindStopGroup}, service)

	// Group members appear in no stop_times row; the UNION gives them an agency anyway.
	agencyStops, err := client.Queries.GetStopIDsForAgency(ctx, "CC")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"CC_Ironton_Ferry_East", "CC_Ironton_Ferry_West"}, agencyStops)
}

func TestStoreGtfsData_ReimportWithoutFlexClearsFlexTables(t *testing.T) {
	client := newTestClientWithZip(t, "../testdata/charlevoix-flex.zip")
	require.Greater(t, countRows(t, client, "ondemand_rules"), 0)

	rabaBytes, err := os.ReadFile("../testdata/raba.zip")
	require.NoError(t, err)
	parsed, err := ParseGtfsData(rabaBytes, "raba")
	require.NoError(t, err)
	changed, err := client.StoreGtfsData(context.Background(), parsed)
	require.NoError(t, err)
	require.True(t, changed)

	for _, table := range []string{"booking_rules", "locations", "location_groups", "location_group_stops",
		"flex_stop_times", "ondemand_services", "ondemand_rules", "ondemand_stop_services"} {
		assert.Equal(t, 0, countRows(t, client, table), table)
	}
}

func TestStoreGtfsData_GroupMemberWithoutCoordinatesGetsNoAgency(t *testing.T) {
	files := flexfixtures.GroupDeviatedFiles()
	files["stops.txt"] = strings.Replace(files["stops.txt"], "s3,Gartz Kirche,53.2000,14.3800", "s3,Gartz Kirche,,", 1)
	client := newTestClientWithBytes(t, flexfixtures.ZipBytes(t, files), "flex-group-deviated")

	var pointerCount int
	require.NoError(t, client.DB.QueryRow(
		"SELECT COUNT(*) FROM ondemand_stop_services WHERE stop_id = 's3'").Scan(&pointerCount))
	assert.Equal(t, 1, pointerCount, "the compiled pointer is kept")

	agencyStops, err := client.Queries.GetStopIDsForAgency(context.Background(), "gd")
	require.NoError(t, err)
	assert.Contains(t, agencyStops, "s1")
	assert.NotContains(t, agencyStops, "s3", "an unstored stop cannot join the stop agency index")
}
