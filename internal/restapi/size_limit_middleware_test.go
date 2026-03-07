package restapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSizeLimitMiddleware_WithinLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "hello world", string(body))
		w.WriteHeader(http.StatusOK)
	})

	middleware := SizeLimitMiddleware(1 << 20) // 1MB limit
	wrappedHandler := middleware(handler)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world"))
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSizeLimitMiddleware_ExceedsLimit(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		// io.ReadAll should return an error when the limit is exceeded
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "request body too large")

	})

	limit := int64(10)
	middleware := SizeLimitMiddleware(limit)
	wrappedHandler := middleware(handler)

	payload := make([]byte, 20)
	for i := range payload {
		payload[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)
}
