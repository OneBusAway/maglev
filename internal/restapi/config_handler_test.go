package restapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/buildinfo"
	"maglev.onebusaway.org/internal/clock"
)

func TestConfigHandler(t *testing.T) {
	originalCommit := buildinfo.CommitHash
	originalVersion := buildinfo.Version
	originalBranch := buildinfo.Branch

	defer func() {
		buildinfo.CommitHash = originalCommit
		buildinfo.Version = originalVersion
		buildinfo.Branch = originalBranch
	}()

	buildinfo.CommitHash = "test-hash-1234567"
	buildinfo.Version = "1.0.0-test"
	buildinfo.Branch = "feature/testing"

	manager := newTestManagerNoData(t)
	importTime := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	manager.SetStaticLastUpdatedForTest(context.Background(), importTime)

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}

	api := NewRestAPI(application)
	t.Cleanup(api.Shutdown)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/config.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	dataMap, ok := model.Data.(map[string]any)
	assert.True(t, ok)

	entry, ok := dataMap["entry"].(map[string]any)
	assert.True(t, ok)

	gitProps, ok := entry["gitProperties"].(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, "test-hash-1234567", gitProps["git.commit.id"])
	assert.Equal(t, "1.0.0-test", gitProps["git.build.version"])
	assert.Equal(t, "feature/testing", gitProps["git.branch"])

	// importTime = 2026-11-02 12:00:00 UTC -> 1793620800000 ms
	// serviceDateTo = importTime + 30 days -> 1796212800000 ms
	assert.Equal(t, "1793620800000", entry["serviceDateFrom"])
	assert.Equal(t, "1796212800000", entry["serviceDateTo"])
}

func TestConfigHandler_NoImportData(t *testing.T) {
	manager := newTestManagerNoData(t)

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsManager: manager,
		Clock:       clock.RealClock{},
	}

	api := NewRestAPI(application)
	t.Cleanup(api.Shutdown)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/config.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dataMap, ok := model.Data.(map[string]any)
	assert.True(t, ok)

	entry, ok := dataMap["entry"].(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, "", entry["serviceDateFrom"])
	assert.Equal(t, "", entry["serviceDateTo"])
}

func TestConfigHandler_DatabaseError(t *testing.T) {
	api := apiWithClosedDB(t)
	t.Cleanup(api.Shutdown)

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/config.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	dataMap, ok := model.Data.(map[string]any)
	assert.True(t, ok)

	entry, ok := dataMap["entry"].(map[string]any)
	assert.True(t, ok)

	assert.Equal(t, "", entry["serviceDateFrom"])
	assert.Equal(t, "", entry["serviceDateTo"])
}
