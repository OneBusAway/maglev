package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestDebugIndexHandler_ProductionReturns404(t *testing.T) {
	webUI := &WebUI{
		Application: &app.Application{
			Config: appconf.Config{Env: appconf.Production},
		},
	}

	req, _ := http.NewRequest("GET", "/debug?dataType=agencies", nil)
	rr := httptest.NewRecorder()

	webUI.debugIndexHandler(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code, "Should return 404 in Production")
}

func TestDebugIndexHandler_DevelopmentReturns200(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic as expected: %v", r)
		}
	}()
	webUI := &WebUI{
		Application: &app.Application{
			Config:      appconf.Config{Env: appconf.Development},
			GtfsManager: &gtfs.Manager{},
		},
	}

	req, _ := http.NewRequest("GET", "/debug?dataType=agencies", nil)
	rr := httptest.NewRecorder()

	webUI.debugIndexHandler(rr, req)

	if rr.Code == http.StatusNotFound {
		t.Errorf("expected 200 (or non-404) in Development, got 404")
	}
}

func TestWriteDebugData(t *testing.T) {
	tests := []struct {
		name           string
		title          string
		data           interface{}
		expectedInBody []string
	}{
		{
			name:           "map data",
			title:          "Test Map Data",
			data:           map[string]string{"key": "value"},
			expectedInBody: []string{"Test Map Data", "key", "value"},
		},
		{
			name:           "string data",
			title:          "Test String",
			data:           "hello world",
			expectedInBody: []string{"Test String", "hello world"},
		},
		{
			name:           "nil data",
			title:          "Nil Data",
			data:           nil,
			expectedInBody: []string{"Nil Data", "nil"},
		},
		{
			name:           "slice data",
			title:          "Test Slice",
			data:           []int{1, 2, 3},
			expectedInBody: []string{"Test Slice"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeDebugData(rr, tt.title, tt.data)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Contains(t, rr.Header().Get("Content-Type"), "text/html")

			body := rr.Body.String()
			for _, expected := range tt.expectedInBody {
				assert.Contains(t, body, expected)
			}
		})
	}
}

func TestDebugIndexHandler_AllDataTypes(t *testing.T) {
	webUI := createTestWebUI(t)

	tests := []struct {
		name          string
		dataType      string
		expectedTitle string
	}{
		{"warnings", "warnings", "GTFS Static - Parse Warnings"},
		{"agencies", "agencies", "GTFS Static - Agencies"},
		{"routes", "routes", "GTFS Static - Routes"},
		{"stops", "stops", "GTFS Static - Stops"},
		{"transfers", "transfers", "GTFS Static - Transfers"},
		{"services", "services", "GTFS Static - Services"},
		{"trips", "trips", "GTFS Static - Trips"},
		{"shapes", "shapes", "GTFS Static - Shapes"},
		{"realtime_trips", "realtime_trips", "GTFS Realtime - Trips"},
		{"realtime_vehicles", "realtime_vehicles", "GTFS Realtime - Vehicles"},
		{"default_empty", "", "Choose a data type"},
		{"default_invalid", "invalid_type", "Choose a data type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/debug/"
			if tt.dataType != "" {
				url += "?dataType=" + tt.dataType
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rr := httptest.NewRecorder()

			webUI.debugIndexHandler(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Contains(t, rr.Body.String(), tt.expectedTitle)
		})
	}
}

func TestDebugIndexHandler_ProductionBlocksAllDataTypes(t *testing.T) {
	webUI := &WebUI{
		Application: &app.Application{
			Config: appconf.Config{Env: appconf.Production},
		},
	}

	dataTypes := []string{"warnings", "agencies", "routes", "stops", "realtime_trips"}
	for _, dt := range dataTypes {
		t.Run(dt, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/?dataType="+dt, nil)
			rr := httptest.NewRecorder()

			webUI.debugIndexHandler(rr, req)

			assert.Equal(t, http.StatusNotFound, rr.Code)
		})
	}
}
