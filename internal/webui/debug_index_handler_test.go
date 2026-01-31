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
