package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
)

// Only "/" serves the landing page; any other path 404s instead of returning it with a 200.
func TestIndexRouteServesOnlyRoot(t *testing.T) {
	webUI := &WebUI{Application: &app.Application{Config: appconf.Config{}}}
	mux := http.NewServeMux()
	webUI.SetWebUIRoutes(mux)

	for path, want := range map[string]int{
		"/":                    http.StatusOK,
		"/foo":                 http.StatusNotFound,
		"/api/where/stop/1.pb": http.StatusNotFound,
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("GET %s: got %d, want %d", path, rr.Code, want)
		}
	}
}
