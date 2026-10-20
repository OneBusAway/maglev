package restapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// parityTime is 08:00 in Redding on Thursday 2025-06-12, when all five RABA
// weekday services run.
var parityTime = time.Date(2025, 6, 12, 15, 0, 0, 0, time.UTC)

// parityOrderDependentEndpoints lists endpoints whose response is known to
// change with SQLite's unordered row order, each with the issue that tracks
// it, so the reversed-order pass can skip them until it is fixed. None is
// known today.
var parityOrderDependentEndpoints = map[string]string{}

type parityEndpoint struct {
	name string
	path string
}

type keyedReference struct {
	key  string
	item any
}

func TestParityAPI(t *testing.T) {
	timeMs := parityTime.UnixMilli()
	endpoints := []parityEndpoint{
		{"agencies-with-coverage", "/api/where/agencies-with-coverage.json?key=TEST"},
		{"agency", "/api/where/agency/25.json?key=TEST"},
		{"arrivals-and-departures-stop", fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/25_1033.json?key=TEST&time=%d&minutesBefore=30&minutesAfter=60", timeMs)},
		{"block", "/api/where/block/25_19.json?key=TEST"},
		{"route", "/api/where/route/25_151.json?key=TEST"},
		{"route-ids-for-agency", "/api/where/route-ids-for-agency/25.json?key=TEST"},
		{"routes-for-agency", "/api/where/routes-for-agency/25.json?key=TEST"},
		{"routes-for-location", "/api/where/routes-for-location.json?key=TEST&lat=40.680372&lon=-122.363588"},
		{"schedule-for-route", "/api/where/schedule-for-route/25_161.json?key=TEST&date=2025-06-12"},
		{"schedule-for-stop", "/api/where/schedule-for-stop/25_1033.json?key=TEST&date=2025-06-12"},
		{"shape", "/api/where/shape/25_t580.json?key=TEST"},
		{"stop", "/api/where/stop/25_1033.json?key=TEST"},
		{"stop-ids-for-agency", "/api/where/stop-ids-for-agency/25.json?key=TEST"},
		{"stops-for-location", "/api/where/stops-for-location.json?key=TEST&lat=40.680372&lon=-122.363588"},
		{"stops-for-route", "/api/where/stops-for-route/25_151.json?key=TEST"},
		{"trip", "/api/where/trip/25_84f4520e-88b6-4ee6-8975-856799bc1359.json?key=TEST"},
		{"trip-details", fmt.Sprintf("/api/where/trip-details/25_84f4520e-88b6-4ee6-8975-856799bc1359.json?key=TEST&time=%d", timeMs)},
		{"trips-for-location", fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=40.680372&lon=-122.363588&latSpan=0.3&lonSpan=0.3&time=%d", timeMs)},
		{"trips-for-route", fmt.Sprintf("/api/where/trips-for-route/25_151.json?key=TEST&time=%d&includeSchedule=true", timeMs)},
	}
	api := createTestApiWithFeedAndClock(t, "../../testdata/raba.zip", clock.NewMockClock(parityTime))

	t.Run("default-order", func(t *testing.T) {
		runParityEndpoints(t, api, endpoints, *updateParity, false)
	})

	// With this pragma SQLite returns the rows of a query without ORDER BY in
	// reverse. A response that changes under it relies on row order that
	// nothing guarantees. The in-memory database has a single connection, so
	// the pragma applies to every later query. This pass only compares; it
	// never writes the golden files.
	_, err := api.GtfsManager.GtfsDB.DB.Exec("PRAGMA reverse_unordered_selects=ON")
	require.NoError(t, err)
	t.Run("reversed-unordered-selects", func(t *testing.T) {
		runParityEndpoints(t, api, endpoints, false, true)
	})
}

func runParityEndpoints(t *testing.T, api *RestAPI, endpoints []parityEndpoint, write, skipOrderDependent bool) {
	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			if issue, known := parityOrderDependentEndpoints[endpoint.name]; known && skipOrderDependent {
				t.Skipf("depends on unordered row order: %s", issue)
			}
			assertParityGolden(t, filepath.Join("api", endpoint.name+".json"), parityResponse(t, api, endpoint.path), write)
		})
	}
}

// parityResponse requests the endpoint and returns its body with reference
// lists sorted and decimals rounded.
func parityResponse(t *testing.T, api *RestAPI, path string) any {
	t.Helper()
	resp, raw := callAPIHandler[json.RawMessage](t, api, path)
	require.Equal(t, 200, resp.StatusCode, "%s", path)

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	require.NoError(t, decoder.Decode(&body))
	sortReferences(body)
	return roundDecimals(body)
}

// roundDecimals rounds every non-integer number to 12 significant digits.
// Computed values such as distanceAlongBlock differ in the last bits between
// amd64 and arm64, where Go fuses multiply-adds. Integers are kept exactly.
func roundDecimals(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, field := range typed {
			typed[key] = roundDecimals(field)
		}
	case []any:
		for i, item := range typed {
			typed[i] = roundDecimals(item)
		}
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			if f, err := typed.Float64(); err == nil {
				return json.Number(strconv.FormatFloat(f, 'g', 12, 64))
			}
		}
	}
	return value
}

// sortReferences orders each reference list by ID, then by content. The lists
// are built from maps, so their order is random and carries no meaning.
func sortReferences(body map[string]any) {
	data, _ := body["data"].(map[string]any)
	references, _ := data["references"].(map[string]any)
	for _, list := range references {
		items, _ := list.([]any)
		keyed := make([]keyedReference, len(items))
		for i, item := range items {
			keyed[i] = keyedReference{key: referenceSortKey(item), item: item}
		}
		slices.SortFunc(keyed, func(a, b keyedReference) int { return strings.Compare(a.key, b.key) })
		for i := range keyed {
			items[i] = keyed[i].item
		}
	}
}

func referenceSortKey(item any) string {
	encoded, _ := json.Marshal(item)
	if fields, ok := item.(map[string]any); ok {
		return fmt.Sprint(fields["id"]) + "\x00" + string(encoded)
	}
	return string(encoded)
}
