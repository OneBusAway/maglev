package restapi

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/buildinfo"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
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

	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: filepath.Join(t.TempDir(), "test.db"),
	}
	ctx := context.Background()
	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = gtfsManager.Shutdown(ctx) })

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsConfig:  gtfsConfig,
		GtfsManager: gtfsManager,
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

	serviceDateFrom, ok := entry["serviceDateFrom"].(string)
	assert.True(t, ok)
	assert.Equal(t, "1704096000000", serviceDateFrom)

	serviceDateTo, ok := entry["serviceDateTo"].(string)
	assert.True(t, ok)
	assert.Equal(t, "1767168000000", serviceDateTo)
}

func TestFormatServiceDate(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	assert.NoError(t, err)

	assert.Equal(t, "", formatServiceDate("", loc))
	assert.Equal(t, "", formatServiceDate("invalid", loc))

	expected := time.Date(2026, 10, 16, 0, 0, 0, 0, loc).UnixMilli()
	assert.Equal(t, strconv.FormatInt(expected, 10), formatServiceDate("20261016", loc))
}
