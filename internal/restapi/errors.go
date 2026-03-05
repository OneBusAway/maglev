package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"maglev.onebusaway.org/internal/logging"
	"maglev.onebusaway.org/internal/models"
)

// invalidAPIKeyResponse sends a 401 Unauthorized response with the required format
// for invalid API key errors
func (api *RestAPI) invalidAPIKeyResponse(w http.ResponseWriter, r *http.Request) {
	// Create response with the specific format required
	response := struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}{
		Code:        http.StatusUnauthorized,
		CurrentTime: models.ResponseCurrentTime(api.Clock),
		Text:        "permission denied",
		Version:     1, // Note: This is version 1, not 2 as in a successful response. Probably a mistake, but back-compat.
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		logging.LogError(api.Logger, "failed to encode invalid API key response", err)
	}
}

func (api *RestAPI) serverErrorResponse(w http.ResponseWriter, r *http.Request, err error) {
	// Context cancellation and deadline errors represent request lifecycle termination,
	// not internal server failures. Delegate to the dedicated handler to correctly
	// handle context.Canceled (no response — client disconnected) and
	// context.DeadlineExceeded (504 Gateway Timeout).
	//
	// This applies both to explicit ctx.Err() checks and to downstream operations
	// (e.g., DB queries) that propagate context errors via their return values.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		api.clientCanceledResponse(w, r, err)
		return
	}
	logging.LogError(api.Logger, "internal server error", err, slog.String("path", r.URL.Path))
	// Send a 500 Internal Server Error response
	response := struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}{
		Code:        http.StatusInternalServerError,
		CurrentTime: models.ResponseCurrentTime(api.Clock),
		Text:        "internal server error",
		Version:     1,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	encoderErr := json.NewEncoder(w).Encode(response)
	if encoderErr != nil {
		logging.LogError(api.Logger, "failed to encode server error response", encoderErr)
	}
}

// validationErrorResponse sends a 400 Bad Request response with field-specific validation errors
func (api *RestAPI) validationErrorResponse(w http.ResponseWriter, r *http.Request, fieldErrors map[string][]string) {
	errorText := "validation error"
	for _, errs := range fieldErrors {
		if len(errs) > 0 {
			errorText = errs[0]
			break
		}
	}

	response := struct {
		Code        int         `json:"code"`
		CurrentTime int64       `json:"currentTime"`
		Text        string      `json:"text"`
		Version     int         `json:"version"`
		Data        interface{} `json:"data"`
	}{
		Code:        http.StatusBadRequest,
		CurrentTime: models.ResponseCurrentTime(api.Clock),
		Text:        errorText,
		Version:     2,
		Data: struct {
			FieldErrors map[string][]string `json:"fieldErrors"`
		}{
			FieldErrors: fieldErrors,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		logging.LogError(api.Logger, "failed to encode validation error response", err)
	}
}

// clientCanceledResponse handles context cancellation cleanly.
//
// For context.Canceled (client disconnected): logs at Info level and returns without
// writing a response body — the connection is already gone, so any write is futile.
//
// For context.DeadlineExceeded (server-side timeout): logs at Warn level and writes
// 504 Gateway Timeout, which is semantically correct — the server took too long,
// this is not a server fault (500) nor a client fault.
func (api *RestAPI) clientCanceledResponse(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		api.Logger.Info("request canceled by client",
			"path", r.URL.Path,
			"method", r.Method,
		)
		return

	case errors.Is(err, context.DeadlineExceeded):
		api.Logger.Warn("request deadline exceeded",
			"path", r.URL.Path,
			"method", r.Method,
		)
		response := struct {
			Code        int    `json:"code"`
			CurrentTime int64  `json:"currentTime"`
			Text        string `json:"text"`
			Version     int    `json:"version"`
		}{
			Code:        http.StatusGatewayTimeout,
			CurrentTime: models.ResponseCurrentTime(api.Clock),
			Text:        "gateway timeout",
			Version:     2,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			api.Logger.Error("failed to encode gateway timeout response", "error", err)
		}
		return

	default:
		api.Logger.Warn("clientCanceledResponse called with unexpected error type",
			"error", err,
			"path", r.URL.Path,
			"method", r.Method,
		)
	}
}
