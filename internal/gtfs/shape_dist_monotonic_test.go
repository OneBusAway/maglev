package gtfs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/appconf"
)

// shapePointLatLon traces an L: east along a row of latitude, then north. A stop near
// the turn gets a different direction depending on which segment it is matched to, so
// a shape whose distances all collapse to one point is visible in the result.
var shapePointLatLon = [][2]string{
	{"40.000", "-122.000"},
	{"40.000", "-121.995"},
	{"40.000", "-121.990"},
	{"40.005", "-121.985"},
	{"40.010", "-121.980"},
	{"40.015", "-121.980"},
	{"40.020", "-121.980"},
}

// buildDirectionFeed writes a four-stop trip along shapePointLatLon, with the given
// shape_dist_traveled column values. An empty string leaves the cell blank, which
// parses to an absent distance.
func buildDirectionFeed(t *testing.T, stopDistances, shapeDistances []string) []byte {
	t.Helper()
	require.Len(t, stopDistances, 4)
	require.Len(t, shapeDistances, len(shapePointLatLon))

	var stopTimes strings.Builder
	stopTimes.WriteString("trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled\n")
	for i, distance := range stopDistances {
		fmt.Fprintf(&stopTimes, "T1,08:%02d:00,08:%02d:00,S%d,%d,%s\n", i*5, i*5, i+1, i+1, distance)
	}

	var shapes strings.Builder
	shapes.WriteString("shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence,shape_dist_traveled\n")
	for i, distance := range shapeDistances {
		fmt.Fprintf(&shapes, "SH1,%s,%s,%d,%s\n",
			shapePointLatLon[i][0], shapePointLatLon[i][1], i+1, distance)
	}

	files := map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"A,Test Transit,https://test.com,America/Los_Angeles\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"R1,A,1,Test Route,3\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"S1,One,40.000,-122.000\n" +
			"S2,Two,40.000,-121.990\n" +
			"S3,Three,40.010,-121.980\n" +
			"S4,Four,40.020,-121.980\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"WD,1,1,1,1,1,0,0,20250101,20251231\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,shape_id\n" +
			"R1,WD,T1,Out,SH1\n",
		"stop_times.txt": stopTimes.String(),
		"shapes.txt":     shapes.String(),
	}

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := zipWriter.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zipWriter.Close())
	return buf.Bytes()
}

// directionsForFeed imports a feed and returns each stop's computed direction.
func directionsForFeed(t *testing.T, feed []byte) []string {
	t.Helper()
	ctx := context.Background()

	client, err := gtfsdb.NewClient(gtfsdb.Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	parsed, err := gtfsdb.ParseGtfsData(feed, "direction-test")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(ctx, parsed)
	require.NoError(t, err)

	calculator := NewAdvancedDirectionCalculator(client.Queries)
	directions := make([]string, 0, 4)
	for _, stopID := range []string{"S1", "S2", "S3", "S4"} {
		stop, err := client.Queries.GetStop(ctx, stopID)
		require.NoError(t, err)
		directions = append(directions, calculator.CalculateStopDirection(ctx, stopID, stop.Direction))
	}
	return directions
}

// A shape whose shape_dist_traveled does not increase carries no usable position
// information, so matching on it lands every stop on the same point and the whole trip
// reports one segment's orientation. Geographic matching already handles the case where
// the column is absent, and these feeds have to reach it too.
func TestCalculateStopDirection_IgnoresNonIncreasingShapeDistances(t *testing.T) {
	increasingShape := []string{"0", "450", "900", "1400", "2000", "2500", "3000"}
	increasingStops := []string{"0", "900", "2000", "3000"}

	// What the geometry actually says, which geographic matching recovers.
	correct := []string{"E", "E", "NE", "N"}

	tests := []struct {
		name           string
		stopDistances  []string
		shapeDistances []string
		expected       []string
	}{
		{
			name:           "strictly increasing distances are used",
			stopDistances:  increasingStops,
			shapeDistances: increasingShape,
			expected:       correct,
		},
		{
			name:           "column absent falls back to geography",
			stopDistances:  []string{"", "", "", ""},
			shapeDistances: []string{"", "", "", "", "", "", ""},
			expected:       correct,
		},
		{
			// The placeholder feeds this guard exists for.
			name:           "constant zero placeholder",
			stopDistances:  []string{"0", "0", "0", "0"},
			shapeDistances: []string{"0", "0", "0", "0", "0", "0", "0"},
			expected:       correct,
		},
		{
			name:           "constant non-zero placeholder",
			stopDistances:  []string{"100", "100", "100", "100"},
			shapeDistances: []string{"100", "100", "100", "100", "100", "100", "100"},
			expected:       correct,
		},
		{
			name:           "distances decrease partway along",
			stopDistances:  increasingStops,
			shapeDistances: []string{"0", "450", "900", "200", "2000", "2500", "3000"},
			expected:       correct,
		},
		{
			name:           "a stretch of points repeats one value",
			stopDistances:  increasingStops,
			shapeDistances: []string{"0", "450", "900", "900", "900", "2500", "3000"},
			expected:       correct,
		},
		{
			name:           "only one point carries a distance",
			stopDistances:  increasingStops,
			shapeDistances: []string{"", "", "", "1400", "", "", ""},
			expected:       correct,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := directionsForFeed(t, buildDirectionFeed(t, tt.stopDistances, tt.shapeDistances))
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestShapeDistancesCanLocateAStop(t *testing.T) {
	distance := func(values ...any) []gtfsdb.GetShapePointsWithDistanceRow {
		rows := make([]gtfsdb.GetShapePointsWithDistanceRow, 0, len(values))
		for _, v := range values {
			row := gtfsdb.GetShapePointsWithDistanceRow{}
			if f, ok := v.(float64); ok {
				row.ShapeDistTraveled = sql.NullFloat64{Float64: f, Valid: true}
			}
			rows = append(rows, row)
		}
		return rows
	}

	tests := []struct {
		name   string
		rows   []gtfsdb.GetShapePointsWithDistanceRow
		usable bool
	}{
		{"strictly increasing", distance(0.0, 100.0, 200.0), true},
		{"increasing with gaps", distance(0.0, nil, 200.0, nil), true},
		{"all zero", distance(0.0, 0.0, 0.0), false},
		{"constant non-zero", distance(100.0, 100.0, 100.0), false},
		{"decreasing partway", distance(0.0, 200.0, 100.0), false},
		{"one repeat", distance(0.0, 100.0, 100.0, 300.0), false},
		{"single distance", distance(nil, 100.0, nil), false},
		{"none at all", distance(nil, nil), false},
		{"empty", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.usable, shapeDistancesCanLocateAStop(tt.rows))
		})
	}
}

func TestStopDistancesCanLocateStops(t *testing.T) {
	tests := []struct {
		name   string
		values []any
		usable bool
	}{
		{"valid first zero", []any{0.0, 900.0, 2000.0}, true},
		{"increasing with gaps", []any{0.0, nil, 2000.0, nil}, true},
		{"all zero", []any{0.0, 0.0, 0.0}, false},
		{"constant positive", []any{100.0, 100.0}, false},
		{"decreasing", []any{0.0, 900.0, 800.0}, false},
		{"repeat", []any{0.0, 900.0, 900.0}, false},
		{"single value", []any{nil, 900.0, nil}, false},
		{"all missing", []any{nil, nil}, false},
		{"empty", nil, false},
		{"negative", []any{-1.0, 0.0, 900.0}, false},
		{"NaN", []any{0.0, math.NaN(), 900.0}, false},
		{"positive infinity", []any{0.0, math.Inf(1)}, false},
		{"negative infinity", []any{math.Inf(-1), 900.0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]gtfsdb.StopTime, len(tt.values))
			for i, value := range tt.values {
				if distance, ok := value.(float64); ok {
					rows[i].ShapeDistTraveled = sql.NullFloat64{Float64: distance, Valid: true}
				}
			}
			assert.Equal(t, tt.usable, stopDistancesCanLocateStops(rows))
		})
	}
}
