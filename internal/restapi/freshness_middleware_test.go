package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/gtfs"
)

func TestFreshnessMiddleware(t *testing.T) {
	// A simple dummy handler to simulate the next step in the middleware chain
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	t.Run("GtfsManager is nil", func(t *testing.T) {
		api := &RestAPI{
			Application: &app.Application{
				GtfsManager: nil, // Explicitly nil
			},
		}

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		handler := api.FreshnessMiddleware(dummyHandler)
		handler.ServeHTTP(rr, req)

		if rr.Header().Get("X-Data-Last-Updated") != "" {
			t.Errorf("Expected no X-Data-Last-Updated header, got %q", rr.Header().Get("X-Data-Last-Updated"))
		}
	})

	t.Run("GtfsManager exists but lastUpdated is zero", func(t *testing.T) {
		api := &RestAPI{
			Application: &app.Application{
				GtfsManager: &gtfs.Manager{}, // Default empty manager (time.Time is zero)
			},
		}

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		handler := api.FreshnessMiddleware(dummyHandler)
		handler.ServeHTTP(rr, req)

		if rr.Header().Get("X-Data-Last-Updated") != "" {
			t.Errorf("Expected no X-Data-Last-Updated header, got %q", rr.Header().Get("X-Data-Last-Updated"))
		}
	})

	t.Run("GtfsManager has valid lastUpdated", func(t *testing.T) {
		api := &RestAPI{
			Application: &app.Application{
				GtfsManager: &gtfs.Manager{},
			},
		}

		// Inject a specific time for the test
		expectedTime := time.Date(2023, 10, 27, 10, 0, 0, 0, time.UTC)
		api.GtfsManager.SetStaticLastUpdatedForTest(expectedTime)

		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		handler := api.FreshnessMiddleware(dummyHandler)
		handler.ServeHTTP(rr, req)

		expectedHeader := expectedTime.Format(time.RFC3339)
		actualHeader := rr.Header().Get("X-Data-Last-Updated")

		if actualHeader != expectedHeader {
			t.Errorf("Expected header %q, got %q", expectedHeader, actualHeader)
		}
	})
}
