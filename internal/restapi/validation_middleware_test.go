package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositiveIntRule(t *testing.T) {
	rule := PositiveIntRule("maxCount")

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"valid positive", "10", true},
		{"valid one", "1", true},
		{"zero is invalid", "0", false},
		{"negative is invalid", "-5", false},
		{"non-numeric is invalid", "abc", false},
		{"float is invalid", "3.14", false},
		{"large valid", "999999", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg, ok := rule.Validate(tt.value)
			assert.Equal(t, tt.ok, ok)
			if !ok {
				assert.NotEmpty(t, errMsg)
			}
		})
	}
}

func TestIntRangeRule(t *testing.T) {
	rule := IntRangeRule("minutesAfter", 0, 240)

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"within range", "35", true},
		{"at minimum", "0", true},
		{"at maximum", "240", true},
		{"below minimum", "-1", false},
		{"above maximum", "241", false},
		{"non-numeric", "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg, ok := rule.Validate(tt.value)
			assert.Equal(t, tt.ok, ok)
			if !ok {
				assert.NotEmpty(t, errMsg)
			}
		})
	}
}

func TestNonNegativeIntRule(t *testing.T) {
	rule := NonNegativeIntRule("offset")

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"positive", "5", true},
		{"zero", "0", true},
		{"negative", "-1", false},
		{"non-numeric", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg, ok := rule.Validate(tt.value)
			assert.Equal(t, tt.ok, ok)
			if !ok {
				assert.NotEmpty(t, errMsg)
			}
		})
	}
}

func TestValidateQueryParams(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A simple handler that returns 200 OK when reached
	okHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}

	rules := []QueryParamRule{
		PositiveIntRule("maxCount"),
	}

	wrapped := ValidateQueryParams(api, rules, http.HandlerFunc(okHandler))

	t.Run("valid param passes through", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=10", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("absent param passes through", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("empty param value returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		body, err := io.ReadAll(rec.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "maxCount")
	})

	t.Run("invalid param returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=-1", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		body, err := io.ReadAll(rec.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "maxCount")
	})

	t.Run("non-numeric param returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=abc", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("zero param returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=0", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("response matches validationErrorResponse format", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=bad", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var resp struct {
			Code int `json:"code"`
			Text string `json:"text"`
			Data struct {
				FieldErrors map[string][]string `json:"fieldErrors"`
			} `json:"data"`
		}
		err := json.NewDecoder(rec.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.Code)
		assert.NotEmpty(t, resp.Data.FieldErrors["maxCount"])
	})
}

func TestValidateQueryParamsMultipleRules(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	okHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	rules := []QueryParamRule{
		PositiveIntRule("maxCount"),
		NonNegativeIntRule("offset"),
	}

	wrapped := ValidateQueryParams(api, rules, http.HandlerFunc(okHandler))

	t.Run("both valid", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=10&offset=0", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("first invalid", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=-1&offset=0", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("second invalid", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=10&offset=-5", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("both invalid collects all errors", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test?maxCount=-1&offset=-5", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		body, err := io.ReadAll(rec.Body)
		require.NoError(t, err)
		bodyStr := string(body)
		assert.Contains(t, bodyStr, "maxCount")
		assert.Contains(t, bodyStr, "offset")
	})
}
