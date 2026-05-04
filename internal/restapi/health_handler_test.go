package restapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
)

func TestHealthHandlerWithNilApplication(t *testing.T) {
	// Create a minimal RestAPI with nil Application to simulate DB unavailable
	api := &RestAPI{
		Application: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	api.healthHandler(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp HealthResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", resp.Status)
	assert.Equal(t, "manager or database not initialized", resp.Detail)
}

func TestHealthHandlerReturnsOK(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(24*time.Hour))

	// Mark the manager as ready (simulating completed initialization)
	manager.MarkReady()

	app := &app.Application{
		GtfsManager: manager,
		Config: appconf.Config{
			RateLimit: 100,
		},
	}

	api := NewRestAPI(app)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp HealthResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "ok", healthResp.Status)
	assert.NotEmpty(t, healthResp.FeedExpiresAt)
	assert.False(t, healthResp.DataExpired)
}

func TestHealthHandlerReturnsExpired(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.SetFeedExpiresAtForTest(context.Background(), time.Now().Add(-24*time.Hour))

	manager.MarkReady()

	app := &app.Application{
		GtfsManager: manager,
		Config: appconf.Config{
			RateLimit: 100,
		},
	}

	api := NewRestAPI(app)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp HealthResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "ok", healthResp.Status)
	assert.NotEmpty(t, healthResp.FeedExpiresAt)
	assert.True(t, healthResp.DataExpired)
}

func TestHealthHandlerStarting(t *testing.T) {
	// Create a minimal manager but DON'T mark as ready to simulate startup phase
	manager := newTestManagerNoData(t)

	app := &app.Application{
		GtfsManager: manager,
		Config: appconf.Config{
			RateLimit: 100,
		},
	}

	api := NewRestAPI(app)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	var healthResp HealthResponse
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "starting", healthResp.Status)
	assert.Equal(t, "GTFS data is being indexed and initialized", healthResp.Detail)
}

func TestHealthHandlerVerboseMode(t *testing.T) {
	manager := newTestManagerNoData(t)
	manager.MarkReady()
	manager.SetStaticLastUpdatedForTest(context.Background(), time.Now().UTC())
	manager.SetFeedUpdateTimeForTest("feed-1", time.Now().UTC())

	app := &app.Application{
		GtfsManager: manager,
		Config: appconf.Config{
			RateLimit: 100,
		},
	}

	api := NewRestAPI(app)
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Test WITHOUT verbose (dataFreshness absent)
	respNoVerbose, err := http.Get(server.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = respNoVerbose.Body.Close() }()
	assert.Equal(t, http.StatusOK, respNoVerbose.StatusCode)

	var healthRespNoVerbose HealthResponse
	err = json.NewDecoder(respNoVerbose.Body).Decode(&healthRespNoVerbose)
	require.NoError(t, err)
	assert.Equal(t, "ok", healthRespNoVerbose.Status)
	assert.Nil(t, healthRespNoVerbose.DataFreshness)

	// Test WITH verbose=true (dataFreshness present)
	respVerbose, err := http.Get(server.URL + "/healthz?verbose=true")
	require.NoError(t, err)
	defer func() { _ = respVerbose.Body.Close() }()
	assert.Equal(t, http.StatusOK, respVerbose.StatusCode)

	var healthRespVerbose HealthResponse
	err = json.NewDecoder(respVerbose.Body).Decode(&healthRespVerbose)
	require.NoError(t, err)
	assert.Equal(t, "ok", healthRespVerbose.Status)
	require.NotNil(t, healthRespVerbose.DataFreshness)
	assert.NotNil(t, healthRespVerbose.DataFreshness.StaticGtfsLastUpdated)
	assert.Contains(t, healthRespVerbose.DataFreshness.RealtimeFeeds, "feed-1")
}
