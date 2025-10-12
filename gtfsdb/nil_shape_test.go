package gtfsdb

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// createMinimalGTFSWithoutShapes creates a minimal GTFS zip file that has trips WITHOUT shape_id
// This simulates real-world GTFS feeds where shapes.txt is optional
func createMinimalGTFSWithoutShapes(t *testing.T) []byte {
	t.Helper()

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)

	// agency.txt - required
	agencyFile, err := zipWriter.Create("agency.txt")
	require.NoError(t, err)
	_, err = agencyFile.Write([]byte(`agency_id,agency_name,agency_url,agency_timezone
TEST_AGENCY,Test Transit,https://test.com,America/Los_Angeles
`))
	require.NoError(t, err)

	// routes.txt - required
	routesFile, err := zipWriter.Create("routes.txt")
	require.NoError(t, err)
	_, err = routesFile.Write([]byte(`route_id,agency_id,route_short_name,route_long_name,route_type
ROUTE1,TEST_AGENCY,1,Test Route,3
`))
	require.NoError(t, err)

	// stops.txt - required
	stopsFile, err := zipWriter.Create("stops.txt")
	require.NoError(t, err)
	_, err = stopsFile.Write([]byte(`stop_id,stop_name,stop_lat,stop_lon
STOP1,First Stop,40.7128,-74.0060
STOP2,Second Stop,40.7580,-73.9855
`))
	require.NoError(t, err)

	// calendar.txt - required (or calendar_dates.txt)
	calendarFile, err := zipWriter.Create("calendar.txt")
	require.NoError(t, err)
	_, err = calendarFile.Write([]byte(`service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date
WEEKDAY,1,1,1,1,1,0,0,20250101,20251231
`))
	require.NoError(t, err)

	// trips.txt - required, but WITHOUT shape_id column (or with empty shape_id)
	tripsFile, err := zipWriter.Create("trips.txt")
	require.NoError(t, err)
	_, err = tripsFile.Write([]byte(`route_id,service_id,trip_id,trip_headsign
ROUTE1,WEEKDAY,TRIP1,Downtown
ROUTE1,WEEKDAY,TRIP2,Uptown
`))
	require.NoError(t, err)

	// stop_times.txt - required
	stopTimesFile, err := zipWriter.Create("stop_times.txt")
	require.NoError(t, err)
	_, err = stopTimesFile.Write([]byte(`trip_id,arrival_time,departure_time,stop_id,stop_sequence
TRIP1,08:00:00,08:00:00,STOP1,1
TRIP1,08:15:00,08:15:00,STOP2,2
TRIP2,09:00:00,09:00:00,STOP2,1
TRIP2,09:15:00,09:15:00,STOP1,2
`))
	require.NoError(t, err)

	// NOTE: No shapes.txt file - this is intentional and valid per GTFS spec
	// shapes.txt is optional, and trips don't need to reference shapes

	err = zipWriter.Close()
	require.NoError(t, err)

	return buf.Bytes()
}

// TestProcessGTFSWithoutShapes tests importing GTFS data where trips have no shape_id
// This test should FAIL with a nil pointer panic before the fix
func TestProcessGTFSWithoutShapes(t *testing.T) {
	// Create in-memory database
	config := Config{
		DBPath:  ":memory:",
		Env:     appconf.Test,
		verbose: true,
	}

	client, err := NewClient(config)
	require.NoError(t, err, "Failed to create client")
	defer func() { _ = client.Close() }()

	// Create minimal GTFS data without shapes
	gtfsData := createMinimalGTFSWithoutShapes(t)

	// This should NOT panic - trips without shapes are valid
	ctx := context.Background()
	err = client.processAndStoreGTFSDataWithSource(gtfsData, "test-source-no-shapes")
	require.NoError(t, err, "Should be able to import GTFS data without shapes")

	// Verify trips were imported successfully
	trips, err := client.Queries.ListTrips(ctx)
	require.NoError(t, err, "Should be able to retrieve trips")
	require.Equal(t, 2, len(trips), "Should have imported 2 trips")

	// Verify that trips have empty/null shape_id
	for _, trip := range trips {
		require.False(t, trip.ShapeID.Valid, "Trip should have null shape_id")
	}
}
