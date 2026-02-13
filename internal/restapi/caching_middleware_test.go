package restapi

import (
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
		endpoint       string
		expectedHeader string
	}{
		{
			name:           "Static Data (Long Cache)",
			endpoint:       "/api/where/agencies-with-coverage.json?key=TEST",
			expectedHeader: "public, max-age=300", // 5 minutes
		},
		{
			name:           "Real-time Data (Short Cache)",
			endpoint:       "/api/where/current-time.json?key=TEST",
			expectedHeader: "public, max-age=30", // 30 seconds
		},
		{
			name:           "User Reports (No Cache)",
			endpoint:       "/api/where/report-problem-with-stop/123.json?key=TEST",
			expectedHeader: "no-cache, no-store, must-revalidate", // 0 seconds
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.endpoint)
			assert.NoError(t, err)
			defer resp.Body.Close()

			gotHeader := resp.Header.Get("Cache-Control")
			assert.Equal(t, tt.expectedHeader, gotHeader, "Cache-Control header mismatch for %s", tt.endpoint)
		})
	}
}
