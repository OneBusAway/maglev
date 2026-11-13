package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractIDFromParams(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "Basic ID",
			id:   "123",
			want: "123",
		},
		{
			name: "ID with JSON extension",
			id:   "456.json",
			want: "456",
		},
		{
			name: "ID with multiple dots",
			id:   "789.data.json",
			want: "789.data",
		},
		{
			name: "ID with .json substring in name",
			id:   "station.json_123.json",
			want: "station.json_123",
		},
		{
			name: "ID with multiple .json extensions removes only trailing",
			id:   "1_foo.json.json",
			want: "1_foo.json",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test handler that will set up our path params
			mux := http.NewServeMux()

			var result string
			mux.HandleFunc("GET /api/test/{id}", func(w http.ResponseWriter, r *http.Request) {
				// This is where PathValue works correctly
				result = ExtractIDFromParams(r)
				w.WriteHeader(http.StatusOK)
			})

			// Create a request to test with
			req := httptest.NewRequest(http.MethodGet, "/api/test/"+tc.id, nil)
			rr := httptest.NewRecorder()

			// Process the request through our mux to set up path params
			mux.ServeHTTP(rr, req)

			// Assert the result
			assert.Equal(t, tc.want, result, "ExtractIDFromParams should correctly extract and clean the ID")
		})
	}
}
