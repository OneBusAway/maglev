package restapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/clock"
)

func TestBuildSituationReferencesCoverage(t *testing.T) {
	api := &RestAPI{}

	alerts := []gtfs.Alert{
		{
			ID: "alert1",
			// We intentionally omit Cause, Effect, and Headers to keep the test simple.
			// Go will use their zero-values, which will safely trigger the default
			// fallback cases in the mapping functions and ensure 100% path coverage.
		},
	}

	// Call it as a method on the API struct, passing alerts first
	refs := api.BuildSituationReferences(alerts)

	assert.Len(t, refs, 1)
	assert.Equal(t, "alert1", refs[0].ID)
	assert.Equal(t, "UNKNOWN_CAUSE", refs[0].Reason) // 0-value defaults to UNKNOWN_CAUSE
}

func TestResponseHelpersCoverage(t *testing.T) {
	// We must inject the MockClock inside the embedded Application struct
	// so models.ResponseCurrentTime doesn't panic
	api := &RestAPI{
		Application: &app.Application{
			Clock: clock.NewMockClock(time.Now()),
		},
	}

	// The functions require a dummy request object
	req := httptest.NewRequest("GET", "/", nil)

	w := httptest.NewRecorder()
	api.sendNull(w, req)
	assert.Equal(t, 200, w.Code)

	w2 := httptest.NewRecorder()
	api.sendNotFound(w2, req)
	assert.Equal(t, 404, w2.Code)

	w3 := httptest.NewRecorder()
	api.sendUnauthorized(w3, req)
	assert.Equal(t, 401, w3.Code)

	w4 := httptest.NewRecorder()
	api.sendError(w4, req, 500, "error")
	assert.Equal(t, 500, w4.Code)
}
