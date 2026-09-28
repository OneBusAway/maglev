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
// Routes are recognized by the deprecated nullSafeShortName field, which only models.Route emits.
func looksLikeRouteOrStop(object map[string]any) bool {
	_, hasShortName := object["nullSafeShortName"]
	_, hasRouteIDs := object["routeIds"]
	_, hasLat := object["lat"]
	return hasShortName || (hasRouteIDs && hasLat)
}

// pointerCounts tallies the Route/Stop serializations assertPointers checked.
type pointerCounts struct {
	flex    int
	nonFlex int
}

// assertPointers walks a response and checks every Route/Stop serialization:
// flex entities carry exactly their expected onDemandServiceIds, others carry
// no key at all. It returns how many of each it saw.
func assertPointers(t *testing.T, body []byte, expected map[string][]string) pointerCounts {
	t.Helper()
	var document any
	require.NoError(t, json.Unmarshal(body, &document))

	var counts pointerCounts
	walkJSON(document, func(object map[string]any) {
		if !looksLikeRouteOrStop(object) {
			return
		}
		id, _ := object["id"].(string)
		want, isFlex := expected[id]
		got, hasKey := object["onDemandServiceIds"]
		if !isFlex {
			counts.nonFlex++
			assert.False(t, hasKey, "non-flex entity %s must not carry onDemandServiceIds", id)
			return
		}
		counts.flex++
		require.True(t, hasKey, "flex entity %s is missing onDemandServiceIds", id)
		gotIDs := make([]string, 0)
		for _, v := range got.([]any) {
			gotIDs = append(gotIDs, v.(string))
		}
		assert.Equal(t, want, gotIDs, "pointer on %s", id)
	})
	return counts
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

	manisteeExpected := map[string][]string{"MC_MC1": {"MC_MC1"}, "MC_MC2": {"MC_MC2"}}

	fixtures := []struct {
		name     string
		api      *RestAPI
		expected map[string][]string
		// absenceOnly rows target timed-only entities: they must serialize at
		// least one non-flex route or stop, and none of those may carry the key.
		absenceOnly bool
		endpoints   []string
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
			api:      createTestApiWithGTFSFixture(t, clock.NewMockClock(pointerFixtureClock), "flex-group-deviated.zip", flexfixtures.GroupDeviatedFiles()),
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
		{
			// MC3 is a timed-only route in a flex feed, so neither it nor its
			// stops may point at an on-demand service.
			name:        "manistee timed-only route",
			api:         createTestApiWithFeed(t, models.GetFixturePath(t, "manistee-flex.zip")),
			expected:    manisteeExpected,
			absenceOnly: true,
			endpoints: []string{
				"/api/where/route/MC_MC3.json",
				"/api/where/stops-for-route/MC_MC3.json",
				"/api/where/routes-for-agency/MC.json",
				"/api/where/stop/MC_MC_MCT_Office.json",
				"/api/where/stop/MC_MC_Family_Fare.json",
				"/api/where/stop/MC_MC_Walgreens.json",
				"/api/where/stop/MC_MC_WS_Community_College.json",
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

					counts := assertPointers(t, body, fixture.expected)
					if fixture.absenceOnly {
						assert.Greater(t, counts.nonFlex, 0, "endpoint must serialize at least one timed-only route or stop, else it proves nothing")
						return
					}
					assert.Greater(t, counts.flex, 0, "endpoint must serialize at least one flex route or stop, else it proves nothing")
				})
			}
		})
	}
}

func TestAttachOnDemandPointers_NilSafe(t *testing.T) {
	api := &RestAPI{}
	route := models.Route{ID: "x_y"}
	stop := models.Stop{ID: "x_y"}
	api.attachRouteOnDemandIDs(&route, models.NewEmptyReferences())
	api.attachStopOnDemandIDs(&stop, models.NewEmptyReferences())
	api.attachOnDemandPointersToReferences(models.NewEmptyReferences())
	assert.Nil(t, route.OnDemandServiceIDs)
	assert.Nil(t, stop.OnDemandServiceIDs)
}

func TestAttachOnDemandPointers_IgnoresUnparseableIDs(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	route := models.Route{ID: "no-underscore"}
	api.attachRouteOnDemandIDs(&route, models.NewEmptyReferences())
	assert.Nil(t, route.OnDemandServiceIDs)
}

func TestPointerFlexIndex_SkipsFeedsWithoutOnDemandServices(t *testing.T) {
	assert.Nil(t, createTestApi(t).pointerFlexIndex(), "a fixed-route feed attaches no pointers")
	assert.Nil(t, (&RestAPI{}).pointerFlexIndex(), "a RestAPI without an Application attaches no pointers")
	assert.NotNil(t, alexandriaAPI(t).pointerFlexIndex())
}

func TestAttachRouteOnDemandIDs_FillsEntryAndReferences(t *testing.T) {
	api := createTestApiWithFeed(t, models.GetFixturePath(t, "charlevoix-flex.zip"))
	route := models.Route{ID: "CC_CC1"}
	references := models.NewEmptyReferences()
	references.Routes = append(references.Routes, models.Route{ID: "CC_CC1"})

	api.attachRouteOnDemandIDs(&route, references)

	assert.NotEmpty(t, route.OnDemandServiceIDs)
	assert.Equal(t, route.OnDemandServiceIDs, references.Routes[0].OnDemandServiceIDs)
}
