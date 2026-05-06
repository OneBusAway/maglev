package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/app"
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
				GtfsManager: newTestManagerNoData(t), // Empty DB — import_metadata has no row, so zero time
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
		manager := newTestManagerNoData(t)
		api := &RestAPI{
			Application: &app.Application{
				GtfsManager: manager,
			},
		}

		// Inject a specific time for the test (truncate to seconds — DB stores unix seconds)
		expectedTime := time.Date(2023, 10, 27, 10, 0, 0, 0, time.UTC)
		api.GtfsManager.SetStaticLastUpdatedForTest(context.Background(), expectedTime)

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
