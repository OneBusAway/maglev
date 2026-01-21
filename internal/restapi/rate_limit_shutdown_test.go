package restapi

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_Shutdown(t *testing.T) {
	middleware := NewRateLimitMiddleware(10, time.Second)

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

func TestRateLimitMiddleware_GoroutineActuallyExits(t *testing.T) {
	// Force garbage collection to clean up any lingering goroutines from previous tests
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	
	// Get baseline goroutine count
	initial := runtime.NumGoroutine()

	middleware := NewRateLimitMiddleware(10, time.Second)
	time.Sleep(50 * time.Millisecond) // Give goroutine time to start

	afterCreate := runtime.NumGoroutine()
	assert.Greater(t, afterCreate, initial, "cleanup goroutine should have started")

	middleware.Stop()
	time.Sleep(50 * time.Millisecond) // Give goroutine time to exit

	afterStop := runtime.NumGoroutine()
	assert.LessOrEqual(t, afterStop, initial, "cleanup goroutine should have exited")
}

