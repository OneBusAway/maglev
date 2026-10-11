package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

func newGtfsRealtimeTestRequest(pathID, rawQuery string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/gtfs_realtime/vehicle-positions-for-agency/"+pathID+"?"+rawQuery, nil)
	r.SetPathValue("id", pathID)
	return r
}

func TestParseGtfsRealtimeAgencyPath(t *testing.T) {
	tests := []struct {
		pathID     string
		wantAgency string
		wantFormat gtfsRealtimeFormat
		wantOK     bool
	}{
		{"25.pb", "25", gtfsRealtimeBinary, true},
		{"25.pbtext", "25", gtfsRealtimeText, true},
		{"agency.with.dots.pb", "agency.with.dots", gtfsRealtimeBinary, true},
		{"25", "", gtfsRealtimeBinary, false},
		{"25.json", "", gtfsRealtimeBinary, false},
	}
	for _, tt := range tests {
		t.Run(tt.pathID, func(t *testing.T) {
			agencyID, format, ok := parseGtfsRealtimeAgencyPath(tt.pathID)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantAgency, agencyID)
			assert.Equal(t, tt.wantFormat, format)
		})
	}
}

func TestParseGtfsRealtimeExportRequest(t *testing.T) {
	now := time.Date(2026, 10, 9, 19, 30, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(now))
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	tests := []struct {
		name        string
		agencyID    string
		query       string
		wantUnix    int64
		wantRemove  bool
		wantRouteID string
	}{
		{"absent time uses clock", "25", "", now.Unix(), true, ""},
		{"empty time uses clock", "25", "time=", now.Unix(), true, ""},
		{"epoch millis", "25", "time=1791581968000", 1791581968, true, ""},
		{"fractional millis truncate", "25", "time=1791581968999", 1791581968, true, ""},
		{"epoch zero", "25", "time=0", 0, true, ""},
		{"date-time in agency zone", "25", "time=2026-10-09_12-00-00", time.Date(2026, 10, 9, 12, 0, 0, 0, losAngeles).Unix(), true, ""},
		{"date in agency zone", "25", "time=2026-10-09", time.Date(2026, 10, 9, 0, 0, 0, 0, losAngeles).Unix(), true, ""},
		{"unknown agency uses UTC", "unknown", "time=2026-10-09_12-00-00", time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).Unix(), true, ""},
		{"removeAgencyIds empty", "25", "removeAgencyIds=", now.Unix(), true, ""},
		{"removeAgencyIds TRUE", "25", "removeAgencyIds=TRUE", now.Unix(), true, ""},
		{"removeAgencyIds False", "25", "removeAgencyIds=False", now.Unix(), false, ""},
		{"raw route filter", "25", "routeFilterId=2LINE", now.Unix(), true, "2LINE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, fieldErrors, err := api.parseGtfsRealtimeExportRequest(
				newGtfsRealtimeTestRequest(tt.agencyID+".pb", tt.query), tt.agencyID, gtfsRealtimeBinary)
			require.NoError(t, err)
			require.Empty(t, fieldErrors)
			assert.Equal(t, tt.agencyID, req.AgencyID)
			assert.Equal(t, tt.wantUnix, req.Time.Unix())
			assert.Equal(t, tt.wantRemove, req.RemoveAgencyIDs)
			assert.Equal(t, tt.wantRouteID, req.RouteFilterID)
		})
	}
}

func TestParseGtfsRealtimeExportRequestRejectsInvalidParameters(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(time.Date(2026, 10, 9, 19, 30, 0, 0, time.UTC)))
	tests := []struct {
		name      string
		query     string
		wantField string
	}{
		{"malformed time", "time=bad-time", "time"},
		{"negative epoch", "time=-1000", "time"},
		{"pre-epoch date", "time=1969-12-31", "time"},
		{"invalid boolean", "removeAgencyIds=maybe", "removeAgencyIds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fieldErrors, err := api.parseGtfsRealtimeExportRequest(
				newGtfsRealtimeTestRequest("25.pb", tt.query), "25", gtfsRealtimeBinary)
			require.NoError(t, err)
			assert.Contains(t, fieldErrors, tt.wantField)
		})
	}
}
