// Package flexfixtures holds in-memory GTFS-Flex feeds shared by gtfsdb and
// restapi tests. Both packages need the same synthetic feeds, and Go test files
// cannot be imported across packages, so the file maps live here instead.
package flexfixtures

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// Identifiers used by GroupDeviatedFiles.
const (
	GDAgencyID       = "gd"
	GDRufbusRouteID  = "rufbus"
	GDHermannRouteID = "hermann"
	GDWinstopRouteID = "winstop"
	GDGroupID        = "grp476"
	GDZoneAID        = "zone_a"
	GDZoneMultiID    = "zone_multi"
	GDRufTripID      = "ruf-trip"
	GDHerTripID      = "her-trip"
	GDWinTripID      = "win-trip"
	GDHerBlockID     = "her-block"
	GDBookingRufID   = "br_ruf"
	GDBookingHerID   = "br_her"
	GDServiceID      = "svc"
)

// Identifiers used by ZeroStopsFiles.
const (
	APAgencyID    = "AP"
	APRoute1ID    = "AP1"
	APRoute2ID    = "AP2_med"
	APWeekdaySvc  = "mon-tues-wed-thurs-fri"
	APSaturdaySvc = "sat"
)

// Identifiers used by TwoAgencyFiles.
const (
	TAFixedAgencyID = "aa"
	TAFlexAgencyID  = "bb"
	TAFlexRouteID   = "flexbb"
	TAGroupID       = "grp_bb"
	TASharedStopID  = "X" // served by aa's fixed route and referenced by bb's flex rules
	TAFlexStopID    = "Y" // referenced only by bb's flex rules
	TAFixedStopID   = "Z" // served only by aa's fixed route
)

// ZipBytes zips the given file map in memory.
func ZipBytes(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// GroupDeviatedFiles is a UTC feed with three routes:
//   - rufbus:  location-group route (group of three stops, group→group windows)
//   - hermann: deviated route (timed stop, zone, timed stop, zone, timed stop);
//     pickup_type/drop_off_type cells are BLANK on the timed rows so the
//     blank-cell → 0 default is what makes those stops pickup-capable
//   - winstop: a windowed stop-id record next to a MultiPolygon zone (spec §9.1)
func GroupDeviatedFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"gd,Group Deviated Transit,http://example.com,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"rufbus,gd,476,RufBus 476,3\n" +
			"hermann,gd,HX,Hermann Express,3\n" +
			"winstop,gd,WS,Window Stop Shuttle,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"s1,Angermuende Bahnhof,53.0100,14.0000\n" +
			"s2,Angermuende Markt,53.0150,14.0050\n" +
			"s3,Gartz Kirche,53.2000,14.3800\n" +
			"h1,New Ulm Center,44.3100,-94.4600\n" +
			"h2,New Ulm Hospital,44.3200,-94.4500\n" +
			"h3,New Ulm College,44.3300,-94.4400\n" +
			"w1,Window Stop,44.4000,-94.4000\n",
		"location_groups.txt": "location_group_id,location_group_name\n" +
			"grp476,RufBus 476 stops\n",
		"location_group_stops.txt": "location_group_id,stop_id\n" +
			"grp476,s1\ngrp476,s2\ngrp476,s3\n",
		"booking_rules.txt": "booking_rule_id,booking_type,prior_notice_duration_min,prior_notice_last_day,prior_notice_last_time,message,phone_number\n" +
			"br_ruf,1,30,,,Book 30 minutes ahead,+49 3331 1234\n" +
			"br_her,2,,1,17:00:00,Book by 5 pm the day before,+1 507 359 1717\n",
		"locations.geojson": `{"type":"FeatureCollection","features":[
{"id":"zone_a","type":"Feature","properties":{"stop_name":"Hermann deviation zone"},"geometry":{"type":"Polygon","coordinates":[[[-94.47,44.30],[-94.43,44.30],[-94.43,44.34],[-94.47,44.34],[-94.47,44.30]]]}},
{"id":"zone_multi","type":"Feature","properties":{},"geometry":{"type":"MultiPolygon","coordinates":[[[[-94.41,44.39],[-94.39,44.39],[-94.39,44.41],[-94.41,44.41],[-94.41,44.39]]],[[[-94.36,44.39],[-94.34,44.39],[-94.34,44.41],[-94.36,44.41],[-94.36,44.39]]]]}}
]}`,
		"trips.txt": "route_id,service_id,trip_id,block_id\n" +
			"rufbus,svc,ruf-trip,\n" +
			"hermann,svc,her-trip,her-block\n" +
			"hermann,svc,her-trip-2,her-block\n" +
			"winstop,svc,win-trip,\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,location_id,location_group_id,stop_sequence,pickup_type,drop_off_type,start_pickup_drop_off_window,end_pickup_drop_off_window,pickup_booking_rule_id,drop_off_booking_rule_id\n" +
			"ruf-trip,,,,,grp476,1,2,1,07:00:00,19:00:00,br_ruf,br_ruf\n" +
			"ruf-trip,,,,,grp476,2,1,2,07:00:00,19:00:00,br_ruf,br_ruf\n" +
			"her-trip,09:00:00,09:00:00,h1,,,1,,,,,,\n" +
			"her-trip,,,,zone_a,,2,1,3,09:00:00,09:20:00,,br_her\n" +
			"her-trip,09:20:00,09:20:00,h2,,,3,,,,,,\n" +
			"her-trip,,,,zone_a,,4,1,3,09:20:00,09:40:00,,br_her\n" +
			"her-trip,09:40:00,09:40:00,h3,,,5,,,,,,\n" +
			"her-trip-2,10:00:00,10:00:00,h3,,,1,,,,,,\n" +
			"her-trip-2,10:20:00,10:20:00,h2,,,2,,,,,,\n" +
			"her-trip-2,10:40:00,10:40:00,h1,,,3,,,,,,\n" +
			"win-trip,,,w1,,,1,2,1,08:00:00,10:00:00,br_ruf,br_ruf\n" +
			"win-trip,,,,zone_multi,,2,1,2,08:00:00,10:00:00,br_ruf,br_ruf\n",
	}
}

// ZeroStopsFiles is the Arenac feed reduced to a handful of vertices per zone:
// header-only stops.txt, pure zone→zone, type-1 rules with neither duration
// bound, a type-2 rule with prior_notice_last_day but no last time, a stray
// location.geojson that must be ignored, and one polygon with a hole.
func ZeroStopsFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"AP,Arenac Public Transit Authority,https://arenactransit.com/,America/Detroit\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"AP1,AP,Aren_ST1,Arenac Dial-A-Ride,3\n" +
			"AP2_med,AP,Aren_ST2,Arenac Out of County Medical Transportation,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"sat,0,0,0,0,0,1,0,20240101,20271231\n" +
			"mon-tues-wed-thurs-fri,1,1,1,1,1,0,0,20240101,20271231\n",
		"calendar_dates.txt": "service_id,date,exception_type\n" +
			"mon-tues-wed-thurs-fri,20260101,2\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n",
		"booking_rules.txt": "booking_rule_id,booking_type,prior_notice_duration_min,prior_notice_duration_max,prior_notice_last_day,prior_notice_last_time,message,phone_number\n" +
			"booking_rule_AP1,1,,,,,Arenac dial-a-ride,(989) 846-7500\n" +
			"booking_rule_AP1_bay_trans,1,,,,,Arenac to Bay County,(989) 846-7500\n" +
			"booking_rule_AP1_glad_trans,1,,,,,Arenac to Gladwin County,(989) 846-7500\n" +
			"booking_rule_AP1_ogem_trans,1,,,,,Arenac to Ogemaw County,(989) 846-7500\n" +
			"booking_rule_AP1_iosc_trans,1,,,,,Arenac to Iosco County,(989) 846-7500\n" +
			"booking_rule_AP2,2,,,7,,Non-emergency medical transportation,(989) 846-7500\n",
		"location.geojson": `{"type":"FeatureCollection","features":[{"id":"ignored_zone","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]]]}}]}`,
		"locations.geojson": `{"type":"FeatureCollection","features":[
{"id":"arenac_county","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-84.167,43.910],[-83.564,43.910],[-83.564,44.164],[-84.167,44.164],[-84.167,43.910]]]}},
{"id":"bay_county","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-84.168,43.479],[-83.699,43.479],[-83.699,43.997],[-84.168,43.997],[-84.168,43.479]]]}},
{"id":"gladwin_county","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-84.608,43.814],[-84.166,43.814],[-84.166,44.162],[-84.608,44.162],[-84.608,43.814]]]}},
{"id":"ogemaw_county","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-84.371,44.161],[-83.883,44.161],[-83.883,44.509],[-84.371,44.509],[-84.371,44.161]]]}},
{"id":"iosco_county","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-83.887,44.162],[-83.317,44.162],[-83.317,44.512],[-83.887,44.512],[-83.887,44.162]]]}},
{"id":"nemt_all_michigan_upper","type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-86.541,43.293],[-83.260,43.293],[-83.260,45.810],[-86.541,45.810],[-86.541,43.293]],[[-84.856,44.160],[-84.856,44.511],[-84.366,44.511],[-84.366,44.160],[-84.856,44.160]]]}}
]}`,
		"trips.txt": "route_id,service_id,trip_id\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_same_zone\n" +
			"AP1,sat,AP1_sat_same_zone\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_arenac_to_bay\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_bay_to_arenac\n" +
			"AP1,sat,AP1_sat_arenac_to_bay\n" +
			"AP1,sat,AP1_sat_bay_to_arenac\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_arenac_to_gladwin\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_gladwin_to_arenac\n" +
			"AP1,sat,AP1_sat_arenac_to_gladwin\n" +
			"AP1,sat,AP1_sat_gladwin_to_arenac\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_arenac_to_ogemaw\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_ogemaw_to_arenac\n" +
			"AP1,sat,AP1_sat_arenac_to_ogemaw\n" +
			"AP1,sat,AP1_sat_ogemaw_to_arenac\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_arenac_to_iosco\n" +
			"AP1,mon-tues-wed-thurs-fri,AP1_wk_iosco_to_arenac\n" +
			"AP1,sat,AP1_sat_arenac_to_iosco\n" +
			"AP1,sat,AP1_sat_iosco_to_arenac\n" +
			"AP2_med,mon-tues-wed-thurs-fri,AP2_wk_arenac_to_nemt\n" +
			"AP2_med,mon-tues-wed-thurs-fri,AP2_wk_nemt_to_arenac\n" +
			"AP2_med,sat,AP2_sat_arenac_to_nemt\n" +
			"AP2_med,sat,AP2_sat_nemt_to_arenac\n",
		"stop_times.txt": zeroStopsStopTimes(),
	}
}

// zeroStopsStopTimes emits the two-record zone→zone pattern for every trip in
// ZeroStopsFiles: weekday trips 06:00–19:00, Saturday trips 09:00–17:00.
func zeroStopsStopTimes() string {
	type pair struct{ trip, from, to, rule string }
	pairs := []pair{
		{"AP1_wk_same_zone", "arenac_county", "arenac_county", "booking_rule_AP1"},
		{"AP1_sat_same_zone", "arenac_county", "arenac_county", "booking_rule_AP1"},
		{"AP1_wk_arenac_to_bay", "arenac_county", "bay_county", "booking_rule_AP1_bay_trans"},
		{"AP1_wk_bay_to_arenac", "bay_county", "arenac_county", "booking_rule_AP1_bay_trans"},
		{"AP1_sat_arenac_to_bay", "arenac_county", "bay_county", "booking_rule_AP1_bay_trans"},
		{"AP1_sat_bay_to_arenac", "bay_county", "arenac_county", "booking_rule_AP1_bay_trans"},
		{"AP1_wk_arenac_to_gladwin", "arenac_county", "gladwin_county", "booking_rule_AP1_glad_trans"},
		{"AP1_wk_gladwin_to_arenac", "gladwin_county", "arenac_county", "booking_rule_AP1_glad_trans"},
		{"AP1_sat_arenac_to_gladwin", "arenac_county", "gladwin_county", "booking_rule_AP1_glad_trans"},
		{"AP1_sat_gladwin_to_arenac", "gladwin_county", "arenac_county", "booking_rule_AP1_glad_trans"},
		{"AP1_wk_arenac_to_ogemaw", "arenac_county", "ogemaw_county", "booking_rule_AP1_ogem_trans"},
		{"AP1_wk_ogemaw_to_arenac", "ogemaw_county", "arenac_county", "booking_rule_AP1_ogem_trans"},
		{"AP1_sat_arenac_to_ogemaw", "arenac_county", "ogemaw_county", "booking_rule_AP1_ogem_trans"},
		{"AP1_sat_ogemaw_to_arenac", "ogemaw_county", "arenac_county", "booking_rule_AP1_ogem_trans"},
		{"AP1_wk_arenac_to_iosco", "arenac_county", "iosco_county", "booking_rule_AP1_iosc_trans"},
		{"AP1_wk_iosco_to_arenac", "iosco_county", "arenac_county", "booking_rule_AP1_iosc_trans"},
		{"AP1_sat_arenac_to_iosco", "arenac_county", "iosco_county", "booking_rule_AP1_iosc_trans"},
		{"AP1_sat_iosco_to_arenac", "iosco_county", "arenac_county", "booking_rule_AP1_iosc_trans"},
		{"AP2_wk_arenac_to_nemt", "arenac_county", "nemt_all_michigan_upper", "booking_rule_AP2"},
		{"AP2_wk_nemt_to_arenac", "nemt_all_michigan_upper", "arenac_county", "booking_rule_AP2"},
		{"AP2_sat_arenac_to_nemt", "arenac_county", "nemt_all_michigan_upper", "booking_rule_AP2"},
		{"AP2_sat_nemt_to_arenac", "nemt_all_michigan_upper", "arenac_county", "booking_rule_AP2"},
	}
	out := "trip_id,arrival_time,departure_time,stop_id,location_id,location_group_id,stop_sequence,pickup_type,drop_off_type,pickup_booking_rule_id,drop_off_booking_rule_id,start_pickup_drop_off_window,end_pickup_drop_off_window,safe_duration_factor,safe_duration_offset\n"
	for _, p := range pairs {
		start, end := "06:00:00", "19:00:00"
		if len(p.trip) > 7 && p.trip[4:7] == "sat" {
			start, end = "09:00:00", "17:00:00"
		}
		out += p.trip + ",,,," + p.from + ",,1,2,1," + p.rule + "," + p.rule + "," + start + "," + end + ",2,30\n"
		out += p.trip + ",,,," + p.to + ",,2,1,2," + p.rule + "," + p.rule + "," + start + "," + end + ",2,30\n"
	}
	return out
}

// TwoAgencyFiles is a UTC feed where agency aa runs a fixed route through stop
// X and agency bb runs a flex service whose rules reference X (as a windowed
// pickup and as a location-group member) and the flex-only stop Y.
func TwoAgencyFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"aa,Alpha Transit,http://example.com/aa,UTC\n" +
			"bb,Beta Dial-A-Ride,http://example.com/bb,UTC\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"fixed,aa,1,Alpha Line,3\n" +
			"flexbb,bb,B,Beta Flex,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"svc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"X,Shared Plaza,47.6000,-122.3300\n" +
			"Y,Beta Corner,47.6100,-122.3200\n" +
			"Z,Alpha Terminal,47.6200,-122.3100\n",
		"location_groups.txt": "location_group_id,location_group_name\n" +
			"grp_bb,Beta stops\n",
		"location_group_stops.txt": "location_group_id,stop_id\n" +
			"grp_bb,X\ngrp_bb,Y\n",
		"trips.txt": "route_id,service_id,trip_id\n" +
			"fixed,svc,fixed-trip\n" +
			"flexbb,svc,bb-trip\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,location_group_id,stop_sequence,pickup_type,drop_off_type,start_pickup_drop_off_window,end_pickup_drop_off_window\n" +
			"fixed-trip,08:00:00,08:00:00,X,,1,,,,\n" +
			"fixed-trip,08:10:00,08:10:00,Z,,2,,,,\n" +
			"bb-trip,,,X,,1,2,1,08:00:00,10:00:00\n" +
			"bb-trip,,,,grp_bb,2,1,2,08:00:00,10:00:00\n",
	}
}
