package restapi

import (
	_ "embed"
	"github.com/stretchr/testify/assert"
	"net/http"
	"os"
	"testing"
	"time"
)

//go:embed git/git_properties.txt
var git_Properties string

func TestConfigDataGeneration(t *testing.T) {
	_, err := os.Stat("git/git_properties.txt")
	assert.True(t, err == nil, "Something is wrong with git/git_properties.txt file")
}

func TestConfigRequiresValidApiKey(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/config.json?key=invalid")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestConfig(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/config.json?key=TEST")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check the content type
	assert.Equal(t, resp.Header.Get("Content-Type"), "application/json")

	// Check basic response structure
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, 2, model.Version)

	// Get the current time to compare with response time
	now := time.Now().UnixNano() / int64(time.Millisecond)

	// The response time should be within a reasonable range of the current time
	// Let's say 5 seconds (5000 milliseconds)
	assert.False(t, model.CurrentTime < now-5000 || model.CurrentTime > now+5000)

	// Test the data structure
	// First, we need to cast the interface{} to the expected type
	responseData, ok := model.Data.(map[string]interface{})
	assert.True(t, ok, "could not cast data to expected type")

	// Check that entry exists
	entry, ok := responseData["entry"].(map[string]interface{})
	assert.True(t, ok, "could not find entry in response data")
	GitProperties, ok := entry["gitProperties"].(map[string]interface{})
	assert.True(t, ok, "could not find gitProperties in response data")

	dirty, ok := GitProperties["git.dirty"].(string)
	assert.True(t, ok, "could not find git.dirty in gitProperties")
	assert.True(t, dirty == "true" || dirty == "false", "git.dirty should be true or false string")

	_, ok = entry["id"].(string)
	assert.True(t, ok, "could not find id in entry")
	_, ok = entry["name"].(string)
	assert.True(t, ok, "could not find name in entry")
	_, ok = entry["serviceDateFrom"].(string)
	assert.True(t, ok, "could not find serviceDateFrom in entry")
	_, ok = entry["serviceDateTo"].(string)
	assert.True(t, ok, "could not find serviceDateTo in entry")

	// Check that references exist and have the expected structure
	references, ok := responseData["references"].(map[string]interface{})
	assert.True(t, ok, "could not find references in response data")

	// Check that all expected arrays exist in references
	referencesFields := []string{"agencies", "routes", "situations", "stopTimes", "stops", "trips"}
	for _, field := range referencesFields {
		array, ok := references[field].([]interface{})
		assert.True(t, ok, "could not find %s array in references", field)
		assert.Equal(t, 0, len(array), "expected empty %s array, got length %d", field, len(array))
	}
}
