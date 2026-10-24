package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

const javaParityDir = "../../testdata/java_parity"

// javaParityFeedID matches the stop, route and service IDs of the synthetic
// feed. Keep it in step with FEED_ID in testdata/java_parity/capture.py.
var javaParityFeedID = regexp.MustCompile(`^(40|1)_(A|B|B2|C|U|STA|R1|R40|ALL)$`)

// knownJavaDifferences lists requests whose IDs still differ from Java, with
// the reason. Remove an entry once Maglev matches.
var knownJavaDifferences = map[string]string{
	"trip-details/1_R1-0-08?serviceDate=1792825200000&time=1792861260000": "no schedule-based trip status without a vehicle",
	"trips-for-route/1_R1?time=1792861260000&includeSchedule=true":        "no schedule-based trip status without a vehicle; Java also returns another route's trip",
	"schedule-for-route/1_R1?date=2026-10-24":                             "stop references list every route at the stop; Java lists only the requested route and leaves stop time serviceIds empty",
	"schedule-for-route/40_R40?date=2026-10-24":                           "stop references list every route at the stop; Java lists only the requested route and leaves stop time serviceIds empty",
}

// javaParityResult is what capture.py records for one request.
type javaParityResult struct {
	Found bool     `json:"found"`
	IDs   []string `json:"ids"`
}

// TestJavaParity_SyntheticTwoAgencyFeed replays each request in
// testdata/java_parity/java_ids.json against the synthetic feed and compares
// the stop, route and service IDs in the response with Java OneBusAway's.
// testdata/java_parity/README.md explains how to recapture them.
func TestJavaParity_SyntheticTwoAgencyFeed(t *testing.T) {
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	queryTime := clock.NewMockClock(time.Date(2026, 10, 24, 10, 1, 0, 0, losAngeles))
	api := createTestApiWithGTFSFixture(t, queryTime, "java-parity.zip", javaParityFeedFiles(t))

	raw, err := os.ReadFile(filepath.Join(javaParityDir, "java_ids.json"))
	require.NoError(t, err)
	var javaResults map[string]javaParityResult
	require.NoError(t, json.Unmarshal(raw, &javaResults))

	for request, want := range javaResults {
		t.Run(request, func(t *testing.T) {
			if reason, known := knownJavaDifferences[request]; known {
				t.Skip(reason)
			}
			assert.Equal(t, want, maglevParityResult(t, api, request))
		})
	}
}

func javaParityFeedFiles(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(javaParityDir, "feed", "*.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	files := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		files[filepath.Base(path)] = string(content)
	}
	return files
}

func maglevParityResult(t *testing.T, api *RestAPI, request string) javaParityResult {
	t.Helper()
	path, query, _ := strings.Cut(request, "?")
	endpoint := fmt.Sprintf("/api/where/%s.json?key=TEST", path)
	if query != "" {
		endpoint += "&" + query
	}
	_, model := serveApiAndRetrieveEndpoint(t, api, endpoint)

	data, isObject := model.Data.(map[string]any)
	if model.Code != http.StatusOK || model.Data == nil || (isObject && len(data) == 0) {
		return javaParityResult{Found: false, IDs: []string{}}
	}
	ids := map[string]bool{}
	collectFeedIDs(model.Data, "", ids)
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	slices.Sort(sorted)
	return javaParityResult{Found: true, IDs: sorted}
}

// collectFeedIDs records "key=value" for every feed ID in node, keyed by the
// nearest enclosing object key, as capture.py does.
func collectFeedIDs(node any, key string, ids map[string]bool) {
	switch value := node.(type) {
	case map[string]any:
		for childKey, child := range value {
			collectFeedIDs(child, childKey, ids)
		}
	case []any:
		for _, child := range value {
			collectFeedIDs(child, key, ids)
		}
	case string:
		if javaParityFeedID.MatchString(value) {
			ids[key+"="+value] = true
		}
	}
}
