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

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "root", path: "/", wantStatus: http.StatusOK},
		{name: "unknown top-level path", path: "/foo/bar", wantStatus: http.StatusNotFound},
		{name: "unknown API path", path: "/api/where/nonexistent.json", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rr.Code != tt.wantStatus {
				t.Errorf("GET %s: got %d, want %d", tt.path, rr.Code, tt.wantStatus)
			}
		})
	}
}
