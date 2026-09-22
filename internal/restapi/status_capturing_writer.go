package restapi

import "net/http"

type statusCapturingWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

// newStatusCapturingWriter creates a response writer that captures the final HTTP status.
func newStatusCapturingWriter(w http.ResponseWriter) *statusCapturingWriter {
	return &statusCapturingWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

// WriteHeader records the first final HTTP status while allowing informational
// 1xx responses to be forwarded without finalizing the response.
func (w *statusCapturingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}

	if code >= http.StatusContinue && code < http.StatusOK &&
		code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code)
		return
	}

	w.wroteHeader = true
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Write writes the response body and commits an implicit 200 OK when needed.
func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(b)
}
