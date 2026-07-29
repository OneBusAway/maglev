package restapi

import (
	"context"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

type contextKey string

const RequestIDKey contextKey = "request_id"

var validRequestIDRegex = regexp.MustCompile(`^[a-zA-Z0-9-._:]+$`)

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")

		if reqID == "" || len(reqID) > 128 || !validRequestIDRegex.MatchString(reqID) {
			reqID = uuid.NewString()
		}

		w.Header().Set("X-Request-ID", reqID)

		ctx := context.WithValue(r.Context(), RequestIDKey, reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
