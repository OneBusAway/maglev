package restapi

import (
	"testing"
	"time"
)

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
