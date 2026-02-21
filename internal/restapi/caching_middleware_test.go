package restapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCacheControlHeaders(t *testing.T) {
	api := createTestApi(t)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	tests := []struct {
		name           string
		method         string
		endpoint       string
		body           []byte
		expectedHeader string
	}{
		{
			name:           "Static Data (Long Cache)",
			method:         http.MethodGet,
			endpoint:       "/api/where/agencies-with-coverage.json?key=TEST",
			expectedHeader: "public, max-age=300",
		},
		{
			name:           "Real-time Data (Short Cache)",
			method:         http.MethodGet,
			endpoint:       "/api/where/current-time.json?key=TEST",
			expectedHeader: "public, max-age=30",
		},
		{
			name:           "User Reports (No Cache)",
			method:         http.MethodPost,
			endpoint:       "/api/where/report-problem-with-stop?key=TEST",
			body:           []byte(`{"compositeID": "12345.json"}`),
			expectedHeader: "no-cache, no-store, must-revalidate",
		},
		{
			name:           "Error Response (No Cache on 404)",
			method:         http.MethodGet,
			endpoint:       "/api/where/stop/nonexistent_stop_id_123",
			expectedHeader: "no-cache, no-store, must-revalidate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, server.URL+tt.endpoint, bytes.NewReader(tt.body))
			assert.NoError(t, err)

			if tt.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := http.DefaultClient.Do(req)
			assert.NoError(t, err)
			defer resp.Body.Close()

			gotHeader := resp.Header.Get("Cache-Control")
			assert.Equal(t, tt.expectedHeader, gotHeader, "Cache-Control header mismatch for %s", tt.endpoint)
		})
	}
}
