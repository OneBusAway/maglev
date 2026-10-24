package restapi

import (
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

// Production Puget Sound returns 1_16140 for stop/29_40, and not found for
// an absorbed stop whose canonical stop is gone. Other endpoints treat an
// absorbed ID like any missing stop.
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
