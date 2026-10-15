package restapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"slices"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

func TestRecoveryMiddleware_Panic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockClock := clock.NewMockClock(time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC))

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := NewRecoveryMiddleware(logger, mockClock)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var response struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}

	if response.Code != http.StatusInternalServerError {
		t.Errorf("expected code 500 in body, got %d", response.Code)
	}
	if response.Text != "internal server error" {
		t.Errorf("expected text 'internal server error', got %s", response.Text)
	}
	if response.Version != models.APIVersion {
		t.Errorf("expected version %d, got %d", models.APIVersion, response.Version)
	}

	expectedTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	if response.CurrentTime != expectedTime {
		t.Errorf("expected currentTime %d, got %d", expectedTime, response.CurrentTime)
	}
}

func TestRecoveryMiddleware_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockClock := clock.NewMockClock(time.Now())

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok body"))
	})

	handler := NewRecoveryMiddleware(logger, mockClock)(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "ok body" {
		t.Errorf("expected body 'ok body', got %s", body)
	}
}

func TestRecoveryMiddleware_PanicAfterWriteDoesNotOverrideResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockClock := clock.NewMockClock(time.Now())

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic("panic after write")
	})

	handler := NewRecoveryMiddleware(logger, mockClock)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "partial" {
		t.Errorf("expected body 'partial', got %s", body)
	}
}

func TestRecoveryMiddleware_PanicAfterInformationalResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(now)
	tests := []struct {
		name     string
		statuses []int
	}{
		{"continue", []int{http.StatusContinue}},
		{"processing", []int{http.StatusProcessing}},
		{"early hints", []int{http.StatusEarlyHints}},
		{"multiple informational responses", []int{http.StatusProcessing, http.StatusEarlyHints}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, status := range tt.statuses {
					w.WriteHeader(status)
				}
				panic("panic after informational response")
			})

			// ResponseRecorder treats 1xx as final, so exercise a real HTTP server.
			server := httptest.NewServer(NewRecoveryMiddleware(logger, mockClock)(panicHandler))
			defer server.Close()
			var informationalStatuses []int
			trace := &httptrace.ClientTrace{
				Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
					informationalStatuses = append(informationalStatuses, code)
					return nil
				},
			}
			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("failed to read response: %v", err)
			}

			if !slices.Equal(informationalStatuses, tt.statuses) {
				t.Errorf("expected informational statuses %v, got %v", tt.statuses, informationalStatuses)
			}
			if response.StatusCode != http.StatusInternalServerError {
				t.Fatalf("expected final status 500, got %d with body %q", response.StatusCode, body)
			}
			if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
				t.Errorf("expected Content-Type application/json, got %s", contentType)
			}
			var payload models.ResponseModel
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("failed to parse response body: %v", err)
			}
			if payload.Code != http.StatusInternalServerError ||
				payload.Text != "internal server error" ||
				payload.Version != models.APIVersion ||
				payload.CurrentTime != now.UnixMilli() {
				t.Errorf("unexpected error response: %+v", payload)
			}
		})
	}
}

func TestRecoveryMiddleware_PanicAfterFinalHeaderDoesNotOverrideResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockClock := clock.NewMockClock(time.Now())
	for _, status := range []int{http.StatusSwitchingProtocols, http.StatusCreated} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				panic("panic after final header")
			})
			handler := NewRecoveryMiddleware(logger, mockClock)(panicHandler)
			writer := newTestResponseWriter()
			handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/test", nil))

			if !slices.Equal(writer.statuses, []int{status}) {
				t.Errorf("expected only status %d, got %v", status, writer.statuses)
			}
			if body := writer.body.String(); body != "" {
				t.Errorf("expected empty body, got %q", body)
			}
		})
	}
}

func TestRecoveryMiddleware_PanicWithErrorType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockClock := clock.NewMockClock(time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC))

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(fmt.Errorf("typed error"))
	})

	handler := NewRecoveryMiddleware(logger, mockClock)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}

	var response struct {
		Code        int    `json:"code"`
		CurrentTime int64  `json:"currentTime"`
		Text        string `json:"text"`
		Version     int    `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}

	if response.Code != http.StatusInternalServerError {
		t.Errorf("expected code 500 in body, got %d", response.Code)
	}
	if response.Text != "internal server error" {
		t.Errorf("expected text 'internal server error', got %s", response.Text)
	}
	if response.Version != models.APIVersion {
		t.Errorf("expected version %d, got %d", models.APIVersion, response.Version)
	}

	expectedTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	if response.CurrentTime != expectedTime {
		t.Errorf("expected currentTime %d, got %d", expectedTime, response.CurrentTime)
	}
}
