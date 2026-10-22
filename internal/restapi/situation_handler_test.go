package restapi

import (
	"net/http"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/models"
)

type SituationResponse EntryResponse[models.Situation]

func TestSituationHandler(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agency25 := "25"
	agency40 := "40"
	api.GtfsManager.MockAddAlert("feed-b", gtfs.Alert{
		ID:               "dup",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency40}},
		Header:           []gtfs.AlertText{{Text: "dup from 40", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "dup",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "dup from 25", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "twenty five late", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "25_already",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "already qualified", Language: "en"}},
	})
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
		Header:           []gtfs.AlertText{{Text: "nameless", Language: "en"}},
	})
	routeOnly := "1"
	api.GtfsManager.MockAddAlert("feed-a", gtfs.Alert{
		ID:               "detour",
		InformedEntities: []gtfs.AlertInformedEntity{{RouteID: &routeOnly}},
		Header:           []gtfs.AlertText{{Text: "route only", Language: "en"}},
	})

	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantID      string
		wantSummary string
	}{
		{
			name:        "route-only alert",
			path:        "/api/where/situation/25_detour.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_detour",
			wantSummary: "route only",
		},
		{
			name:        "qualified id",
			path:        "/api/where/situation/25_late.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_late",
			wantSummary: "twenty five late",
		},
		{
			name:        "raw id that is already qualified",
			path:        "/api/where/situation/25_already.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_already",
			wantSummary: "already qualified",
		},
		{
			name:        "same raw id on another feed",
			path:        "/api/where/situation/25_dup.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "25_dup",
			wantSummary: "dup from 25",
		},
		{
			name:        "the other feed keeps its own alert",
			path:        "/api/where/situation/40_dup.json?key=TEST",
			wantStatus:  http.StatusOK,
			wantID:      "40_dup",
			wantSummary: "dup from 40",
		},
		{
			name:       "malformed id",
			path:       "/api/where/situation/1110.json?key=TEST",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown id",
			path:       "/api/where/situation/25_missing.json?key=TEST",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[SituationResponse](t, api, tt.path)

			require.Equal(t, tt.wantStatus, resp.StatusCode)
			require.Equal(t, tt.wantStatus, model.Code)
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, "no-cache, no-store, must-revalidate", resp.Header.Get("Cache-Control"))
				return
			}

			assert.Equal(t, "public, max-age=30", resp.Header.Get("Cache-Control"))
			assert.Equal(t, tt.wantID, model.Data.Entry.ID)
			require.NotNil(t, model.Data.Entry.Summary)
			assert.Equal(t, tt.wantSummary, model.Data.Entry.Summary.Value)
			assert.Empty(t, model.Data.References.Agencies)
			assert.Empty(t, model.Data.References.Routes)
			assert.Empty(t, model.Data.References.Situations)
		})
	}
}

func TestAlertMatchesSituationID(t *testing.T) {
	agency := "25"
	route := "1"
	alert := gtfs.Alert{
		ID:               "late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency}},
	}
	routeOnly := gtfs.Alert{
		ID:               "detour",
		InformedEntities: []gtfs.AlertInformedEntity{{RouteID: &route}},
	}
	prefixed := gtfs.Alert{ID: "25_late"}
	nameless := gtfs.Alert{ID: ""}

	assert.True(t, alertMatchesSituationID(alert, "25", "25_late"))
	assert.True(t, alertMatchesSituationID(routeOnly, "25", "25_detour"))
	assert.False(t, alertMatchesSituationID(routeOnly, "40", "25_detour"))
	assert.True(t, alertMatchesSituationID(prefixed, "25", "25_late"))
	assert.False(t, alertMatchesSituationID(alert, "25", "40_late"))
	assert.False(t, alertMatchesSituationID(nameless, "25", "25_late"))
	assert.False(t, alertMatchesSituationID(alert, "25", ""))
}
