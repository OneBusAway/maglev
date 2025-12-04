package restapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_Shutdown(t *testing.T) {
	middleware := NewRateLimitMiddleware(10, time.Second)
	defer middleware.Stop()

	assert.NotNil(t, middleware)
	assert.NotNil(t, middleware.Handler())

	done := make(chan struct{})
	go func() {
		middleware.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown took too long")
	}
}

func TestRateLimitMiddleware_ShutdownIdempotent(t *testing.T) {
	middleware := NewRateLimitMiddleware(10, time.Second)

	middleware.Stop()
	middleware.Stop()
	middleware.Stop()
}

func TestRestAPI_Shutdown(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	done := make(chan struct{})
	go func() {
		api.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("API shutdown took too long")
	}
}

func TestRestAPI_ShutdownIdempotent(t *testing.T) {
	api := createTestApi(t)

	api.Shutdown()
	api.Shutdown()
	api.Shutdown()
}

