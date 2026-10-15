package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

const alertsPath = "/api/gtfs_realtime/alerts-for-agency/"

func newAlertExportTestApi(t *testing.T) *RestAPI {
	t.Helper()
	api := createTestApiWithClock(t, clock.NewMockClock(alertExportNow))
	notice := exportAlert("25", "notice", routeSelector("151", "25"))
	notice.Alert.Header = []gtfs.AlertText{{Text: "Detour", Language: "en"}}
	api.GtfsManager.MockSetExportAlerts([]internalgtfs.ExportAlert{notice, exportAlert("25", "", routeSelector("151", "25"))})
	t.Cleanup(func() { api.GtfsManager.MockSetExportAlerts(nil) })
	return api
}

func TestAlertsForAgencyFormatsMatch(t *testing.T) {
	api := newAlertExportTestApi(t)

	binaryResp, binaryBody := callGtfsRealtimeEndpoint(t, api, alertsPath+"25.pb?key="+exemptExportKey)
	assert.Equal(t, "application/x-google-protobuf", binaryResp.Header.Get("Content-Type"))
	assert.Contains(t, binaryResp.Header.Get("Cache-Control"), "no-store")
	fromBinary := decodeGtfsRealtimeBody(t, binaryResp, binaryBody)

	textResp, textBody := callGtfsRealtimeEndpoint(t, api, alertsPath+"25.pbtext?key="+exemptExportKey)
	assert.Equal(t, "text/plain; charset=utf-8", textResp.Header.Get("Content-Type"))
	fromText := decodeGtfsRealtimeBody(t, textResp, textBody)

	assert.True(t, proto.Equal(fromBinary, fromText))
	assert.Equal(t, uint64(alertExportNow.Unix()), fromBinary.GetHeader().GetTimestamp())
	require.Len(t, fromBinary.Entity, 2)
	assert.Equal(t, "25_notice", fromBinary.Entity[0].GetId())
	assert.Equal(t, "Detour", fromBinary.Entity[0].GetAlert().GetHeaderText().GetTranslation()[0].GetText())
	assert.Equal(t, "151", fromBinary.Entity[0].GetAlert().GetInformedEntity()[0].GetRouteId())
}

func TestAlertsForAgencyParameters(t *testing.T) {
	api := newAlertExportTestApi(t)
	tests := []struct {
		name         string
		pathID       string
		query        string
		wantStatus   int
		wantEntities int
	}{
		{"unknown agency is an empty feed", "unknown.pb", "", http.StatusOK, 0},
		{"keep agency prefixes", "25.pb", "&removeAgencyIds=false", http.StatusOK, 2},
		{"route filter", "25.pb", "&routeFilterId=151", http.StatusOK, 2},
		{"other route filter", "25.pb", "&routeFilterId=152", http.StatusOK, 0},
		{"unsupported version", "25.pb", "&version=1", http.StatusBadRequest, 0},
		{"missing format suffix", "25", "", http.StatusNotFound, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := callGtfsRealtimeEndpoint(t, api, alertsPath+tt.pathID+"?key="+exemptExportKey+tt.query)
			assert.Contains(t, resp.Header.Get("Cache-Control"), "no-store")
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, tt.wantStatus, resp.StatusCode, string(body))
				return
			}
			assert.Len(t, decodeGtfsRealtimeBody(t, resp, body).Entity, tt.wantEntities)
		})
	}
}

func TestAlertsForAgencyAuthorization(t *testing.T) {
	api := newAlertExportTestApi(t)
	for _, pathID := range []string{"25.pb", "25.pbtext"} {
		missing, _ := callGtfsRealtimeEndpoint(t, api, alertsPath+pathID)
		assert.Equal(t, http.StatusUnauthorized, missing.StatusCode)

		invalid, _ := callGtfsRealtimeEndpoint(t, api, alertsPath+pathID+"?key=wrong")
		assert.Equal(t, http.StatusUnauthorized, invalid.StatusCode)
	}
}
