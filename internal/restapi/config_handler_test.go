package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/buildinfo"
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

	_, _, model := serveAndRetrieveEndpoint(t, "/api/where/config.json?key=TEST")

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
}
