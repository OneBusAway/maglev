package restapi

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testResponseWriter struct {
	header    http.Header
	statuses  []int
	finalCode int
	body      bytes.Buffer
}

// newTestResponseWriter creates a test response writer that tracks informational
// and final HTTP response statuses separately.
func newTestResponseWriter() *testResponseWriter {
	return &testResponseWriter{
		header: make(http.Header),
	}
}

// Header returns the response headers for the test writer.
func (w *testResponseWriter) Header() http.Header {
	return w.header
}

// WriteHeader records the response status and keeps informational 1xx responses
// separate from the final response status.
func (w *testResponseWriter) WriteHeader(code int) {
	w.statuses = append(w.statuses, code)

	// 1xx responses except 101 are informational.
	if code >= http.StatusContinue &&
		code < http.StatusOK &&
		code != http.StatusSwitchingProtocols {
		return
	}

	if w.finalCode == 0 {
		w.finalCode = code
	}
}

// Write records the response body and commits a 200 status when no final status
// has been written yet.
func (w *testResponseWriter) Write(b []byte) (int, error) {
	if w.finalCode == 0 {
		w.WriteHeader(http.StatusOK)
	}

	return w.body.Write(b)
}

// TestStatusCapturingWriter verifies final status tracking across explicit,
// implicit, and informational HTTP response statuses.
func TestStatusCapturingWriter(t *testing.T) {
	tests := []struct {
		name           string
		handler        func(t *testing.T, w *statusCapturingWriter)
		expectedStatus int
	}{
		{
			name: "preserves first status",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusNotFound)
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name: "write commits 200",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				_, err := w.Write([]byte("ok"))
				require.NoError(t, err)

				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "records explicit status",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusCreated)
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "write preserves explicit status",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusCreated)

				_, err := w.Write([]byte("ok"))
				require.NoError(t, err)
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "preserves final status after informational response",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusCreated)
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "write commits final status after informational response",
			handler: func(t *testing.T, w *statusCapturingWriter) {
				w.WriteHeader(http.StatusEarlyHints)

				_, err := w.Write([]byte("ok"))
				require.NoError(t, err)
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newTestResponseWriter()
			w := newStatusCapturingWriter(rec)

			tt.handler(t, w)

			assert.Equal(t, tt.expectedStatus, w.statusCode)
			assert.Equal(t, tt.expectedStatus, rec.finalCode)
		})
	}
}
