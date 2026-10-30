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

func TestFindSituationAlertPrefersPublishedID(t *testing.T) {
	agency25 := "25"
	agency40 := "40"
	rawCollision := gtfs.Alert{
		ID:               "25_late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency40}},
	}
	published := gtfs.Alert{
		ID:               "late",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &agency25}},
	}

	got, ok := findSituationAlert([]gtfs.Alert{rawCollision, published}, "25", "25_late")
	require.True(t, ok)
	assert.Equal(t, "late", got.ID, "the published 25_late must win over an earlier raw id")

	got, ok = findSituationAlert([]gtfs.Alert{rawCollision}, "25", "25_late")
	require.True(t, ok)
	assert.Equal(t, "25_late", got.ID, "a raw id still matches when nothing publishes it")

	_, ok = findSituationAlert([]gtfs.Alert{published}, "25", "")
	assert.False(t, ok)

	_, ok = findSituationAlert([]gtfs.Alert{published}, "40", "40_late")
	assert.False(t, ok)
}
