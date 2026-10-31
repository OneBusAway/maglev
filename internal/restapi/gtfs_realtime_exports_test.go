package restapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// gtfsRealtimeExportPaths are the GTFS-RT export endpoints, which share
// their request checks and response writing.
var gtfsRealtimeExportPaths = []string{
	"/api/gtfs_realtime/vehicle-positions-for-agency/",
	"/api/gtfs_realtime/alerts-for-agency/",
}

// newGtfsRealtimeExportsTestAPI returns an API whose RABA agency has 30
// vehicles and 30 alerts, enough for either feed to be gzipped.
func newGtfsRealtimeExportsTestAPI(t *testing.T) *RestAPI {
	t.Helper()
	var vehicles []gtfs.Vehicle
	var alerts []gtfs.Alert
	for i := range 30 {
		vehicles = append(vehicles, positionedVehicle(fmt.Sprintf("V%02d", i), 47+float32(i)/100))
		alerts = append(alerts, rabaAlert(fmt.Sprintf("A%02d", i), "Stop closed for construction on Main Street"))
	}
	api := newAlertsTestAPI(t, alerts...)
	api.GtfsManager.MockSetAgencyVehicles("_test", testdata.Raba.ID, vehicles)
	return api
}

func exportURL(path, agencyIDAndSuffix string) string {
	return path + agencyIDAndSuffix + "?key=" + vehiclePositionsKey
}

func TestGtfsRealtimeExports_NotProtobuf(t *testing.T) {
	api := newGtfsRealtimeExportsTestAPI(t)
	for _, path := range gtfsRealtimeExportPaths {
		for _, suffix := range []string{".pbtext", ".json", ""} {
			t.Run(path+suffix, func(t *testing.T) {
				resp, _ := getRaw(t, api, exportURL(path, testdata.Raba.ID+suffix), nil)
				assert.Equal(t, http.StatusNotFound, resp.StatusCode)
			})
		}
	}
}

func TestGtfsRealtimeExports_QueryParameters(t *testing.T) {
	api := newGtfsRealtimeExportsTestAPI(t)
	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"epoch milliseconds time", "&time=1792287624289", http.StatusOK},
		{"legacy time format", "&time=2026-10-18_10-00-00", http.StatusOK},
		{"unparseable time", "&time=abc", http.StatusBadRequest},
		{"routeFilterId is ignored", "&routeFilterId=x", http.StatusOK},
		{"removeAgencyIds is ignored", "&removeAgencyIds=false", http.StatusOK},
	}
	for _, path := range gtfsRealtimeExportPaths {
		_, plain := getRaw(t, api, exportURL(path, testdata.Raba.ID+".pb"), nil)
		for _, tt := range tests {
			t.Run(path+" "+tt.name, func(t *testing.T) {
				resp, body := getRaw(t, api, exportURL(path, testdata.Raba.ID+".pb")+tt.query, nil)
				require.Equal(t, tt.wantStatus, resp.StatusCode)
				if tt.wantStatus == http.StatusOK {
					assert.Equal(t, plain, body, "the parameter must not change the feed")
				}
			})
		}
	}
}

func TestGtfsRealtimeExports_RequireKey(t *testing.T) {
	api := newGtfsRealtimeExportsTestAPI(t)
	for _, path := range gtfsRealtimeExportPaths {
		t.Run(path, func(t *testing.T) {
			resp, _ := getRaw(t, api, path+testdata.Raba.ID+".pb", nil)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}

func TestGtfsRealtimeExports_InvalidAgencyID(t *testing.T) {
	api := newGtfsRealtimeExportsTestAPI(t)
	for _, path := range gtfsRealtimeExportPaths {
		for _, agencyID := range []string{"", "%3Cx%3E"} {
			t.Run(path+agencyID, func(t *testing.T) {
				resp, _ := getRaw(t, api, exportURL(path, agencyID+".pb"), nil)
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			})
		}
	}
}

func TestGtfsRealtimeExports_GzipKeepsTheETag(t *testing.T) {
	api := newGtfsRealtimeExportsTestAPI(t)
	for _, path := range gtfsRealtimeExportPaths {
		t.Run(path, func(t *testing.T) {
			url := exportURL(path, testdata.Raba.ID+".pb")
			gzipped := http.Header{"Accept-Encoding": {"gzip"}}

			// Setting Accept-Encoding by hand turns off the client's transparent
			// decompression, so Content-Encoding stays visible.
			compressed, _ := getRaw(t, api, url, gzipped)
			identity, _ := getRaw(t, api, url, http.Header{"Accept-Encoding": {"identity"}})
			require.Equal(t, "gzip", compressed.Header.Get("Content-Encoding"))
			assert.Equal(t, identity.Header.Get("ETag"), compressed.Header.Get("ETag"))

			gzipped.Set("If-None-Match", compressed.Header.Get("ETag"))
			notModified, _ := getRaw(t, api, url, gzipped)
			assert.Equal(t, http.StatusNotModified, notModified.StatusCode)
		})
	}
}
