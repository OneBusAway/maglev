package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
)

// feedAlertsForTest wraps alerts with a feed agency for tests that call the
// situation helpers directly, mirroring what rebuildMergedRealtimeLocked does
// from the configured agency-ids.
func feedAlertsForTest(feedAgencyID string, alerts []gtfs.Alert) []internalgtfs.FeedAlert {
	out := make([]internalgtfs.FeedAlert, 0, len(alerts))
	for _, alert := range alerts {
		out = append(out, internalgtfs.FeedAlert{Alert: alert, FeedAgencyID: feedAgencyID})
	}
	return out
}

// TestQualifyEntityIDFeedAgencyFallback pins qualifyEntityID's resolution
// order: database agency first, an already-qualified ID left untouched, then
// the feed's first agency for unknown IDs, and the raw ID when no agency is
// available at all.
func TestQualifyEntityIDFeedAgencyFallback(t *testing.T) {
	rawToAgency := map[string]string{"known-route": "25"}
	knownAgencies := map[string]struct{}{}

	tests := []struct {
		name         string
		id           string
		feedAgencyID string
		want         string
	}{
		{"unknown entity uses the feed's first agency", "unknown-route", "40", "40_unknown-route"},
		{"database agency wins over the feed's", "known-route", "40", "25_known-route"},
		{"already qualified ID resolves through the database", "25_known-route", "40", "25_known-route"},
		{"an unknown ID already carrying the feed's prefix stays as is", "40_unknown-route", "40", "40_unknown-route"},
		{"no agency at all keeps the raw ID", "unknown-route", "", "unknown-route"},
		{"empty ID stays empty", "", "40", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, qualifyEntityID(tt.id, tt.feedAgencyID, rawToAgency, knownAgencies))
		})
	}
}

// TestSituationHandler_FeedAgencyFallback covers #1920: for IDs absent from
// the static feed, the feed's first configured agency wins over the entity
// selector's agency, as Java's GtfsRealtimeEntitySource does. The no-agency
// case below carries no selector, so it exercises the first-static-agency
// fallback; Maglev's extra selector step for unconfigured feeds is a
// declared difference in the PR description, not Java parity.
func TestSituationHandler_FeedAgencyFallback(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	api.GtfsManager.MockSetFeedAgencyIDs("feed-with-agencies", "25")

	selectorAgencyID := "40"
	unknownRouteID := "unknown-route"
	api.GtfsManager.MockAddAlert("feed-with-agencies", gtfs.Alert{
		ID: "feed-agency-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{
			AgencyID: &selectorAgencyID,
			RouteID:  &unknownRouteID,
		}},
	})

	unknownStopID := "unknown-stop"
	api.GtfsManager.MockAddAlert("feed-without-agencies", gtfs.Alert{
		ID: "no-selector-agency-alert",
		InformedEntities: []gtfs.AlertInformedEntity{{
			StopID: &unknownStopID,
		}},
	})

	t.Run("the feed's first agency wins over the selector's", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_feed-agency-alert.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, model.Data.Entry.AllAffects, 1)
		assert.Equal(t, "25_unknown-route", model.Data.Entry.AllAffects[0].RouteID)
		assert.Equal(t, "40", model.Data.Entry.AllAffects[0].AgencyID,
			"the selector's agency is still reported on the affect")
	})

	t.Run("an alert with no agency anywhere still emits a combined ID", func(t *testing.T) {
		resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/25_no-selector-agency-alert.json?key=TEST")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Len(t, model.Data.Entry.AllAffects, 1)
		assert.Equal(t, "25_unknown-stop", model.Data.Entry.AllAffects[0].StopID)
	})
}

// TestSituationFeedAgency_NoSiblingEntityFallback pins the removal of the old
// sibling-entity fallback: on a feed without agency-ids, an entity that names
// no agency must not inherit the agency named by another informed entity of
// the same alert. Java never looked at siblings either; such an entity falls
// through to the first static agency instead.
func TestSituationFeedAgency_NoSiblingEntityFallback(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	selectorAgencyID := "40"
	unknownRouteID := "unknown-route"
	unknownStopID := "unknown-stop"
	api.GtfsManager.MockAddAlert("feed-without-agencies", gtfs.Alert{
		ID: "sibling-agency-alert",
		InformedEntities: []gtfs.AlertInformedEntity{
			{
				AgencyID: &selectorAgencyID,
				RouteID:  &unknownRouteID,
			},
			{
				StopID: &unknownStopID,
			},
		},
	})

	resp, model := callAPIHandler[SituationResponse](t, api, "/api/where/situation/40_sibling-agency-alert.json?key=TEST")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.Entry.AllAffects, 2)
	assert.Equal(t, "40_unknown-route", model.Data.Entry.AllAffects[0].RouteID,
		"the declaring entity keeps its own agency (declared difference)")
	assert.Equal(t, "25_unknown-stop", model.Data.Entry.AllAffects[1].StopID,
		"the agency-less entity uses the first static agency, not its sibling's 40")
}
