package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/metrics"
)

func TestMetricsHandler_NilMetrics(t *testing.T) {
	handler := MetricsHandler(nil)

	// Should return a pass-through middleware
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := handler(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestMetricsHandler_RecordsMetrics(t *testing.T) {
	m := metrics.New()
	handler := MetricsHandler(m)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	wrapped := handler(inner)

	req := httptest.NewRequest("POST", "/api/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "created", rec.Body.String())
}

func TestMetricsHandler_DefaultStatusCode(t *testing.T) {
	m := metrics.New()
	handler := MetricsHandler(m)

	// Handler that writes body without calling WriteHeader
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("implicit 200"))
	})

	wrapped := handler(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Should default to 200
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMetricsHandler_UnmatchedPath(t *testing.T) {
	m := metrics.New()
	handler := MetricsHandler(m)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	wrapped := handler(inner)

	// Request without r.Pattern set (simulating unmatched route)
	req := httptest.NewRequest("GET", "/unknown/path", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMetricsResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &metricsResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	w.WriteHeader(http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, w.statusCode)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMetricsResponseWriter_InitialStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &metricsResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	// Without calling WriteHeader, statusCode should be 200
	assert.Equal(t, http.StatusOK, w.statusCode)
}

func TestMetricsHandler_Integration(t *testing.T) {
	m := metrics.New()

	// Create a simple mux with a registered route
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	// Wrap with metrics handler
	handler := MetricsHandler(m)(mux)

	// Create test server
	server := httptest.NewServer(handler)
	defer server.Close()

	// Make a request
	resp, err := http.Get(server.URL + "/api/test")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMetricsHandler_VariousStatusCodes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
	}{
		{"OK", http.StatusOK},
		{"Created", http.StatusCreated},
		{"BadRequest", http.StatusBadRequest},
		{"NotFound", http.StatusNotFound},
		{"InternalServerError", http.StatusInternalServerError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := metrics.New()
			handler := MetricsHandler(m)

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
			})

			wrapped := handler(inner)

			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			assert.Equal(t, tc.statusCode, rec.Code)
		})
	}
}
