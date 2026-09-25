package restapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/flexfixtures"
	"maglev.onebusaway.org/internal/models"
)

// pointerFixtureClock is 09:10 UTC on Thursday 2025-06-12: her-trip (09:00–09:40)
// is active for trips-for-route/-location and h2's 09:20 arrival is inside the
// default arrivals window.
var pointerFixtureClock = time.Date(2025, 6, 12, 9, 10, 0, 0, time.UTC)

const pointerFixtureServiceDateMillis = "1749686400000" // 2025-06-12T00:00:00Z

// groupDeviatedFilesWithHermannShape gives her-trip a shape through h1–h3.
// Scheduled trips-for-location places a vehicle-less trip by projecting its
// schedule onto its shape, so a shapeless trip is never found in any box.
func groupDeviatedFilesWithHermannShape() map[string]string {
	files := flexfixtures.GroupDeviatedFiles()
	files["trips.txt"] = "route_id,service_id,trip_id,block_id,shape_id\n" +
		"rufbus,svc,ruf-trip,,\n" +
		"hermann,svc,her-trip,her-block,her-shape\n" +
		"hermann,svc,her-trip-2,her-block,\n" +
		"winstop,svc,win-trip,,\n"
	files["shapes.txt"] = "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\n" +
		"her-shape,44.3100,-94.4600,1\n" +
		"her-shape,44.3200,-94.4500,2\n" +
		"her-shape,44.3300,-94.4400,3\n"
	return files
}

// walkJSON calls visit for every JSON object in the document.
func walkJSON(value any, visit func(object map[string]any)) {
	switch v := value.(type) {
	case map[string]any:
		visit(v)
		for _, child := range v {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range v {
			walkJSON(child, visit)
		}
	}
}

// looksLikeRouteOrStop reports whether an object is a serialized models.Route or models.Stop.
func looksLikeRouteOrStop(object map[string]any) bool {
	_, hasShortName := object["nullSafeShortName"]
	_, hasRouteIDs := object["routeIds"]
	_, hasLat := object["lat"]
	return hasShortName || (hasRouteIDs && hasLat)
}

// assertPointers walks a response and checks every Route/Stop serialization:
// flex entities carry exactly their expected onDemandServiceIds, others carry
// no key at all. It returns the number of flex entities seen.
func assertPointers(t *testing.T, body []byte, expected map[string][]string) int {
	t.Helper()
	var document any
	require.NoError(t, json.Unmarshal(body, &document))

	hits := 0
	walkJSON(document, func(object map[string]any) {
		if !looksLikeRouteOrStop(object) {
			return
		}
		id, _ := object["id"].(string)
		want, isFlex := expected[id]
		got, hasKey := object["onDemandServiceIds"]
		if !isFlex {
			assert.False(t, hasKey, "non-flex entity %s must not carry onDemandServiceIds", id)
			return
		}
		hits++
		require.True(t, hasKey, "flex entity %s is missing onDemandServiceIds", id)
		gotIDs := make([]string, 0)
		for _, v := range got.([]any) {
			gotIDs = append(gotIDs, v.(string))
		}
		assert.Equal(t, want, gotIDs, "pointer on %s", id)
	})
	return hits
}

func TestWhereEndpoints_CarryOnDemandPointers(t *testing.T) {
	charlevoixExpected := map[string][]string{
		"CC_CC1": {"CC_CC1"}, "CC_CC2_med": {"CC_CC2_med"}, "CC_CC3": {"CC_CC3"}, "CC_CC4": {"CC_CC4"},
		"CC_CC_Ironton_Ferry_West": {"CC_CC3"}, "CC_CC_Ironton_Ferry_East": {"CC_CC3"},
	}
	deviatedExpected := map[string][]string{
		"gd_rufbus": {"gd_rufbus"}, "gd_hermann": {"gd_hermann"}, "gd_winstop": {"gd_winstop"},
		"gd_s1": {"gd_rufbus"}, "gd_s2": {"gd_rufbus"}, "gd_s3": {"gd_rufbus"},
		"gd_h1": {"gd_hermann"}, "gd_h2": {"gd_hermann"},
		"gd_w1": {"gd_winstop"},
		// gd_h3 is only ever a timed drop-off after other timed stops, so no rule references it.
	}

	fixtures := []struct {
		name      string
		api       *RestAPI
		expected  map[string][]string
		endpoints []string
	}{
		{
			name:     "charlevoix",
			api:      createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip")),
			expected: charlevoixExpected,
			endpoints: []string{
				"/api/where/stop/CC_CC_Ironton_Ferry_West.json",
				"/api/where/route/CC_CC1.json",
				"/api/where/routes-for-agency/CC.json",
				"/api/where/stops-for-agency/CC.json",
				"/api/where/search/route.json?input=Charlevoix",
				"/api/ondemand/service/CC_CC3.json?geometryDetail=none",
				"/api/ondemand/services-for-agency/CC.json?geometryDetail=none",
			},
		},
		{
			name:     "group and deviated",
			api:      createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-group-deviated.zip", groupDeviatedFilesWithHermannShape()),
			expected: deviatedExpected,
			endpoints: []string{
				"/api/where/stop/gd_s1.json",
				"/api/where/stop/gd_h1.json",
				"/api/where/route/gd_hermann.json",
				"/api/where/routes-for-agency/gd.json",
				"/api/where/routes-for-location.json?lat=44.32&lon=-94.45&radius=3000",
				"/api/where/stops-for-location.json?lat=44.32&lon=-94.45&radius=3000",
				"/api/where/stops-for-agency/gd.json",
				"/api/where/stops-for-route/gd_hermann.json",
				"/api/where/search/stop.json?input=New",
				"/api/where/search/route.json?input=Hermann",
				"/api/where/trip/gd_her-trip.json",
				"/api/where/trip-details/gd_her-trip.json",
				"/api/where/trips-for-route/gd_hermann.json",
				"/api/where/trips-for-location.json?lat=44.32&lon=-94.45&latSpan=0.1&lonSpan=0.1",
				"/api/where/schedule-for-stop/gd_h1.json",
				"/api/where/schedule-for-route/gd_hermann.json?date=2025-06-12",
				"/api/where/arrivals-and-departures-for-stop/gd_h2.json",
				"/api/where/arrival-and-departure-for-stop/gd_h2.json?tripId=gd_her-trip&serviceDate=" + pointerFixtureServiceDateMillis,
				"/api/where/block/gd_her-block.json",
				"/api/ondemand/services-for-location.json?lat=44.32&lon=-94.45&radius=3000&geometryDetail=none",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(fixture.api.SetupAPIRoutes())
			defer server.Close()
			for _, endpoint := range fixture.endpoints {
				t.Run(endpoint, func(t *testing.T) {
					separator := "?"
					if strings.Contains(endpoint, "?") {
						separator = "&"
					}
					resp, err := http.Get(server.URL + endpoint + separator + "key=TEST")
					require.NoError(t, err)
					defer func() { _ = resp.Body.Close() }()
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

					hits := assertPointers(t, body, fixture.expected)
					assert.Greater(t, hits, 0, "endpoint must serialize at least one flex route or stop, else it proves nothing")
				})
			}
		})
	}
}

func TestAttachOnDemandPointers_NilSafe(t *testing.T) {
	api := &RestAPI{}
	route := models.Route{ID: "x_y"}
	stop := models.Stop{ID: "x_y"}
	api.attachRouteOnDemandIDs(&route)
	api.attachStopOnDemandIDs(&stop)
	api.attachOnDemandPointersToReferences(models.NewEmptyReferences())
	assert.Nil(t, route.OnDemandServiceIDs)
	assert.Nil(t, stop.OnDemandServiceIDs)
}

func TestAttachOnDemandPointers_IgnoresUnparseableIDs(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	route := models.Route{ID: "no-underscore"}
	api.attachRouteOnDemandIDs(&route)
	assert.Nil(t, route.OnDemandServiceIDs)
}
