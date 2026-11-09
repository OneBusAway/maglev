package restapi

import (
	"archive/zip"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
)

// createConsolidatedTestApi serves RABA with 5027 absorbed into 5008, and
// 3022 absorbed into a stop no feed has, so its stop times are dropped.
func createConsolidatedTestApi(t *testing.T) *RestAPI {
	t.Helper()
	path := filepath.Join(t.TempDir(), "StopConsolidation.txt")
	require.NoError(t, os.WriteFile(path, []byte("25_5008 25_5027\n25_gone 25_3022\n"), 0o600))
	return createTestApiWithGtfsConfig(t, gtfs.Config{
		GtfsURL:              filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath:         ":memory:",
		StopConsolidationURL: path,
	}, clock.RealClock{})
}

// Production Puget Sound returns 1_16140 for stop/29_40: the absorbed ID
// must match the file's agency_stop token, and the response names the
// canonical stop by its own agency. It returns not found for an absorbed
// stop whose canonical stop is gone. Other endpoints treat an absorbed ID
// like any missing stop.
func TestStopHandler_ConsolidatedStops(t *testing.T) {
	api := createConsolidatedTestApi(t)

	tests := []struct {
		name     string
		endpoint string
		wantCode int
		wantID   string
	}{
		{name: "canonical stop", endpoint: "/api/where/stop/25_5008.json?key=TEST", wantCode: http.StatusOK, wantID: "25_5008"},
		{name: "absorbed stop resolves to its canonical stop", endpoint: "/api/where/stop/25_5027.json?key=TEST", wantCode: http.StatusOK, wantID: "25_5008"},
		{name: "absorbed stop with no stored canonical", endpoint: "/api/where/stop/25_3022.json?key=TEST", wantCode: http.StatusNotFound},
		{name: "absorbed stop elsewhere", endpoint: "/api/where/arrivals-and-departures-for-stop/25_5027.json?key=TEST", wantCode: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)

			require.Equal(t, tt.wantCode, resp.StatusCode)
			if tt.wantID == "" {
				return
			}
			entry, ok := model.Data.(map[string]any)["entry"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.wantID, entry["id"])
			assert.Equal(t, "Hartnell Ave at Bechelli Ln", entry["name"])
		})
	}
}

// createTwoAgencyConsolidatedTestApi serves a feed whose default agency, 1,
// runs no routes: agency 40's route serves stops A and B, and the file
// absorbs 1_B into 1_A. Java names every stop after the feed's default
// agency, whatever agency serves it.
func createTwoAgencyConsolidatedTestApi(t *testing.T) *RestAPI {
	t.Helper()
	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.zip")
	require.NoError(t, os.WriteFile(feedPath, gtfsZip(t, map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"1,Default Agency,http://example.com,America/Los_Angeles\n" +
			"40,Route Agency,http://example.com,America/Los_Angeles\n",
		"routes.txt":   "route_id,agency_id,route_short_name,route_type\nr40,40,R,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nsvc,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt":    "stop_id,stop_name,stop_lat,stop_lon\nA,Main St,47.0,-122.0\nB,Main St,47.00001,-122.0\nC,Elm St,47.01,-122.0\n",
		"trips.txt":    "route_id,service_id,trip_id\nr40,svc,t1\nr40,svc,t2\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"t1,08:00:00,08:00:00,A,1\nt1,08:10:00,08:10:00,C,2\n" +
			"t2,09:00:00,09:00:00,B,1\nt2,09:10:00,09:10:00,C,2\n",
	}), 0o600))
	consolidationPath := filepath.Join(dir, "StopConsolidation.txt")
	require.NoError(t, os.WriteFile(consolidationPath, []byte("1_A 1_B\n"), 0o600))
	return createTestApiWithGtfsConfig(t, gtfs.Config{
		GtfsURL:              feedPath,
		GTFSDataPath:         ":memory:",
		StopConsolidationURL: consolidationPath,
	}, clock.RealClock{})
}

func gtfsZip(t *testing.T, files map[string]string) []byte {
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

// The absorbed ID is matched against the file's agency_stop token, and
// the response names the canonical stop by the agency the file gives it,
// not by the agency of the routes serving it.
func TestStopHandler_ConsolidatedStopUsesFileAgencies(t *testing.T) {
	api := createTwoAgencyConsolidatedTestApi(t)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/1_B.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	entry, ok := model.Data.(map[string]any)["entry"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "1_A", entry["id"])
	assert.Equal(t, []any{"40_r40"}, entry["routeIds"])

	resp, _ = serveApiAndRetrieveEndpoint(t, api, "/api/where/stop/40_B.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
