package restapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
)

// recoveryResponseWriter wraps http.ResponseWriter to detect if headers were sent.
type recoveryResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (rw *recoveryResponseWriter) WriteHeader(code int) {
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

// NewRecoveryMiddleware returns middleware that recovers from panics in handlers,
// logs the panic with stack trace, and returns HTTP 500 (JSON) if no response was sent.
func NewRecoveryMiddleware(logger *slog.Logger, c clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := &recoveryResponseWriter{ResponseWriter: w}
			defer func() {
				if rec := recover(); rec != nil {
					stack := debug.Stack()
					var err error
					if e, ok := rec.(error); ok {
						err = e
					} else {
						err = fmt.Errorf("%v", rec)
					}
					logging.LogError(logger, "handler panic recovered", err, slog.String("path", r.URL.Path), slog.String("stack", string(stack)))
					if !rw.wroteHeader {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusInternalServerError)
						response := struct {
							Code        int    `json:"code"`
							CurrentTime int64  `json:"currentTime"`
							Text        string `json:"text"`
							Version     int    `json:"version"`
						}{
							Code:        http.StatusInternalServerError,
							CurrentTime: models.ResponseCurrentTime(c),
							Text:        "internal server error",
							Version:     1,
						}
						_ = json.NewEncoder(w).Encode(response)
					}
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}
