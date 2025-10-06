package app

import (
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/appconf"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlankKeyIsInvalid(t *testing.T) {
	app := &Application{
		Config: appconf.Config{
			ApiKeys: []string{"key"},
		},
	}
	assert.True(t, app.IsInvalidAPIKey(""))
}

func TestIsInvalidAPIKey(t *testing.T) {
	tests := []struct {
		name          string
		configKeys    []string
		testKey       string
		shouldBeValid bool
	}{
		{
			name:          "Valid key matches configured key",
			configKeys:    []string{"test-key", "another-key"},
			testKey:       "test-key",
			shouldBeValid: true,
		},
		{
			name:          "Valid key matches second configured key",
			configKeys:    []string{"test-key", "another-key"},
			testKey:       "another-key",
			shouldBeValid: true,
		},
		{
			name:          "Invalid key does not match any configured key",
			configKeys:    []string{"test-key", "another-key"},
			testKey:       "wrong-key",
			shouldBeValid: false,
		},
		{
			name:          "Empty key is invalid",
			configKeys:    []string{"test-key"},
			testKey:       "",
			shouldBeValid: false,
		},
		{
			name:          "Key with whitespace does not match trimmed key",
			configKeys:    []string{"test-key"},
			testKey:       " test-key ",
			shouldBeValid: false,
		},
		{
			name:          "Single valid key",
			configKeys:    []string{"only-key"},
			testKey:       "only-key",
			shouldBeValid: true,
		},
		{
			name:          "Case sensitive key comparison",
			configKeys:    []string{"TestKey"},
			testKey:       "testkey",
			shouldBeValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &Application{
				Config: appconf.Config{
					ApiKeys: tt.configKeys,
				},
			}
			result := app.IsInvalidAPIKey(tt.testKey)
			if tt.shouldBeValid {
				assert.False(t, result, "Key should be valid")
			} else {
				assert.True(t, result, "Key should be invalid")
			}
		})
	}
}

func TestRequestHasInvalidAPIKey(t *testing.T) {
	tests := []struct {
		name          string
		configKeys    []string
		queryKey      string
		shouldBeValid bool
	}{
		{
			name:          "Valid API key in query parameter",
			configKeys:    []string{"test-key", "another-key"},
			queryKey:      "test-key",
			shouldBeValid: true,
		},
		{
			name:          "Invalid API key in query parameter",
			configKeys:    []string{"test-key"},
			queryKey:      "invalid-key",
			shouldBeValid: false,
		},
		{
			name:          "Missing API key in query parameter",
			configKeys:    []string{"test-key"},
			queryKey:      "",
			shouldBeValid: false,
		},
		{
			name:          "Valid key with multiple configured keys",
			configKeys:    []string{"key1", "key2", "key3"},
			queryKey:      "key2",
			shouldBeValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &Application{
				Config: appconf.Config{
					ApiKeys: tt.configKeys,
				},
			}

			// Create a test request with the query parameter
			req := httptest.NewRequest(http.MethodGet, "/?key="+tt.queryKey, nil)

			result := app.RequestHasInvalidAPIKey(req)
			if tt.shouldBeValid {
				assert.False(t, result, "Request should have valid API key")
			} else {
				assert.True(t, result, "Request should have invalid API key")
			}
		})
	}
}

func TestRequestHasInvalidAPIKeyWithNoQueryParam(t *testing.T) {
	app := &Application{
		Config: appconf.Config{
			ApiKeys: []string{"test-key"},
		},
	}

	// Request without any query parameters
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	result := app.RequestHasInvalidAPIKey(req)
	assert.True(t, result, "Request without API key should be invalid")
}
