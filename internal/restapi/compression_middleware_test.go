package restapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCompressionConfig(t *testing.T) {
	config := DefaultCompressionConfig()
	assert.Equal(t, 1024, config.MinSize)
	assert.Equal(t, 6, config.Level)
}

func TestCompressionMiddleware(t *testing.T) {
	t.Run("should not compress when payload is smaller than MinSize", func(t *testing.T) {
		smallBody := strings.Repeat("a", 500)
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, err := w.Write([]byte(smallBody))
			require.NoError(t, err)
		})

		handler := CompressionMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.NotEqual(t, "gzip", res.Header.Get("Content-Encoding"))
		assert.Equal(t, smallBody, rec.Body.String())
	})

	t.Run("should compress when payload meets or exceeds MinSize and client supports gzip", func(t *testing.T) {
		largeBody := strings.Repeat("OneBusAway transit information for testing compression. ", 50) // ~2800 bytes
		require.GreaterOrEqual(t, len(largeBody), 1024)

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, err := w.Write([]byte(largeBody))
			require.NoError(t, err)
		})

		handler := CompressionMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))

		// Decompress and verify content
		gzReader, err := gzip.NewReader(rec.Body)
		require.NoError(t, err)
		defer gzReader.Close()

		decompressed, err := io.ReadAll(gzReader)
		require.NoError(t, err)
		assert.Equal(t, largeBody, string(decompressed))
	})

	t.Run("should not compress when client does not accept gzip", func(t *testing.T) {
		largeBody := strings.Repeat("OneBusAway transit information without compression. ", 50)
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, err := w.Write([]byte(largeBody))
			require.NoError(t, err)
		})

		handler := CompressionMiddleware(nextHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		// No Accept-Encoding header
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Empty(t, res.Header.Get("Content-Encoding"))
		assert.Equal(t, largeBody, rec.Body.String())
	})

	t.Run("should respect custom min size and compression level", func(t *testing.T) {
		customConfig := CompressionConfig{
			MinSize: 100,
			Level:   1, // Best speed
		}
		mediumBody := strings.Repeat("x", 200)

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, err := w.Write([]byte(mediumBody))
			require.NoError(t, err)
		})

		handler := NewCompressionMiddleware(customConfig)(nextHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		res := rec.Result()
		defer res.Body.Close()

		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"))

		gzReader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		require.NoError(t, err)
		defer gzReader.Close()

		decompressed, err := io.ReadAll(gzReader)
		require.NoError(t, err)
		assert.Equal(t, mediumBody, string(decompressed))
	})

	t.Run("should handle invalid compression level gracefully with fallback", func(t *testing.T) {
		invalidConfig := CompressionConfig{
			MinSize: 100,
			Level:   99, // Invalid level
		}
		body := strings.Repeat("fallback-test-data-", 20)

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, err := w.Write([]byte(body))
			require.NoError(t, err)
		})

		// Must not panic and should fallback to standard gzhttp handler
		handler := NewCompressionMiddleware(invalidConfig)(nextHandler)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		assert.NotPanics(t, func() {
			handler.ServeHTTP(rec, req)
		})

		res := rec.Result()
		defer res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
}
