package restapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"maglev.onebusaway.org/internal/clock"
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
	if response.Version != 1 {
		t.Errorf("expected version 1, got %d", response.Version)
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
	if response.Version != 1 {
		t.Errorf("expected version 1, got %d", response.Version)
	}

	expectedTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	if response.CurrentTime != expectedTime {
		t.Errorf("expected currentTime %d, got %d", expectedTime, response.CurrentTime)
	}
}
