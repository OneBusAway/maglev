package gtfsdb

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
)

// createGTFSWithShapeDistTraveled builds a feed that populates shape_dist_traveled
// and leaves it empty on one trip, so an import can be checked for both cases at
// once. TRIP1 starts at distance 0, which is the value every feed that supplies the
// column uses for a trip's first stop, and SHAPE1's first point does the same.
func createGTFSWithShapeDistTraveled(t *testing.T) []byte {
	t.Helper()

	files := map[string]string{
		"agency.txt": `agency_id,agency_name,agency_url,agency_timezone
TEST_AGENCY,Test Transit,https://test.com,America/Los_Angeles
`,
		"routes.txt": `route_id,agency_id,route_short_name,route_long_name,route_type
ROUTE1,TEST_AGENCY,1,Test Route,3
`,
		"stops.txt": `stop_id,stop_name,stop_lat,stop_lon
STOP1,First Stop,40.7128,-74.0060
STOP2,Second Stop,40.7580,-73.9855
`,
		"calendar.txt": `service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date
WEEKDAY,1,1,1,1,1,0,0,20250101,20251231
`,
		"trips.txt": `route_id,service_id,trip_id,trip_headsign,shape_id
ROUTE1,WEEKDAY,TRIP1,Downtown,SHAPE1
ROUTE1,WEEKDAY,TRIP2,Uptown,SHAPE1
`,
		// TRIP1 supplies distances including 0; TRIP2 leaves them empty.
		"stop_times.txt": `trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled
TRIP1,08:00:00,08:00:00,STOP1,1,0
TRIP1,08:15:00,08:15:00,STOP2,2,1500
TRIP2,09:00:00,09:00:00,STOP2,1,
TRIP2,09:15:00,09:15:00,STOP1,2,
`,
		"shapes.txt": `shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence,shape_dist_traveled
SHAPE1,40.7128,-74.0060,1,0
SHAPE1,40.7350,-73.9950,2,750
SHAPE1,40.7580,-73.9855,3,1500
`,
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

// A shape_dist_traveled of 0 is a real position, the start of the shape, and has to
// stay distinguishable from the column being absent. The direction calculator picks
// its matching strategy from whether the value is null
// (internal/gtfs/advanced_direction_calculator.go), so storing a supplied 0 as null
// silently moves the first stop of every trip onto the geographic fallback.
func TestStoreGtfsData_PreservesZeroShapeDistTraveled(t *testing.T) {
	client, err := NewClient(Config{DBPath: ":memory:", Env: appconf.Test})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	parsed, err := ParseGtfsData(createGTFSWithShapeDistTraveled(t), "test-shape-dist")
	require.NoError(t, err)
	_, err = client.StoreGtfsData(t.Context(), parsed)
	require.NoError(t, err)

	t.Run("supplied zero is stored, not null", func(t *testing.T) {
		var isNull bool
		var value float64
		require.NoError(t, client.DB.QueryRowContext(t.Context(),
			`SELECT shape_dist_traveled IS NULL, COALESCE(shape_dist_traveled, -1)
			   FROM stop_times WHERE trip_id = 'TRIP1' AND stop_sequence = 1`,
		).Scan(&isNull, &value))
		assert.False(t, isNull, "the feed gave this stop a distance of 0, so it must not be null")
		assert.Equal(t, 0.0, value)
	})

	t.Run("supplied non-zero is unaffected", func(t *testing.T) {
		var value float64
		require.NoError(t, client.DB.QueryRowContext(t.Context(),
			`SELECT shape_dist_traveled FROM stop_times
			   WHERE trip_id = 'TRIP1' AND stop_sequence = 2`).Scan(&value))
		assert.Equal(t, 1500.0, value)
	})

	t.Run("absent stays null", func(t *testing.T) {
		var nullCount int
		require.NoError(t, client.DB.QueryRowContext(t.Context(),
			`SELECT COUNT(*) FROM stop_times
			   WHERE trip_id = 'TRIP2' AND shape_dist_traveled IS NULL`).Scan(&nullCount))
		assert.Equal(t, 2, nullCount, "a feed that omits the value must still store null")
	})

	t.Run("first shape point keeps its zero", func(t *testing.T) {
		var isNull bool
		require.NoError(t, client.DB.QueryRowContext(t.Context(),
			`SELECT shape_dist_traveled IS NULL FROM shapes
			   WHERE shape_id = 'SHAPE1' ORDER BY shape_pt_sequence LIMIT 1`).Scan(&isNull))
		assert.False(t, isNull, "the shape's first point is at distance 0, not an absent distance")
	})
}
