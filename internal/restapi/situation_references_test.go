package restapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	gogtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

// collectSituationIDs walks a decoded response and returns every situationIds
// value found anywhere in it, so one assertion covers entries, nested statuses
// and top-level lists alike.
func collectSituationIDs(node any, found *[]string) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "situationIds" {
				if ids, ok := value.([]any); ok {
					for _, id := range ids {
						if s, ok := id.(string); ok {
							*found = append(*found, s)
						}
					}
				}
				continue
			}
			collectSituationIDs(value, found)
		}
	case []any:
		for _, item := range typed {
			collectSituationIDs(item, found)
		}
	}
}

func referencedSituationIDs(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()

	data, _ := body["data"].(map[string]any)
	references, _ := data["references"].(map[string]any)
	situations, _ := references["situations"].([]any)

	ids := make(map[string]bool, len(situations))
	for _, situation := range situations {
		entry, ok := situation.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := entry["id"].(string); ok {
			ids[id] = true
		}
	}
	return ids
}

// TestSituationIDsResolveToReferences covers every endpoint that emits
// situationIds: each ID must resolve to an entry in references.situations, or
// clients are left holding a dangling pointer.
func TestSituationIDsResolveToReferences(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// Agency-scoped so it matches whichever trips and stops each endpoint returns.
	rawAgencyID := "25"
	api.GtfsManager.AddAlertForTest(gogtfs.Alert{
		ID:               "situation-resolution-alert",
		InformedEntities: []gogtfs.AlertInformedEntity{{AgencyID: &rawAgencyID}},
		Header:           []gogtfs.AlertText{{Text: "Test Agency Alert", Language: "en"}},
	})

	tripID, stopID := anyTripAndStop(t, api)
	vehicleID := anyRealTimeVehicleID(t, api)

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "trip-details",
			url:  fmt.Sprintf("/api/where/trip-details/25_%s.json?key=TEST&includeStatus=true", tripID),
		},
		{
			// Pinned to a weekday inside the RABA fixture's calendar range, which
			// ended in 2025; under the real clock the stop has no arrivals and the
			// case would assert nothing.
			name: "arrivals-and-departures-for-stop",
			url: fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/25_%s.json?key=TEST&time=%d&minutesAfter=240",
				stopID, time.Date(2025, 6, 12, 19, 0, 0, 0, time.UTC).UnixMilli()),
		},
		{
			name: "trip-for-vehicle",
			url:  fmt.Sprintf("/api/where/trip-for-vehicle/25_%s.json?key=TEST&includeStatus=true", vehicleID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := callAPIHandler[map[string]any](t, api, tt.url)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var emitted []string
			collectSituationIDs(body, &emitted)
			referenced := referencedSituationIDs(t, body)

			sawSituationID := false
			for _, id := range emitted {
				sawSituationID = true
				assert.True(t, referenced[id],
					"situationId %q must resolve to an entry in references.situations", id)
			}
			require.True(t, sawSituationID, "expected the seeded alert to surface as a situationId")
		})
	}
}

// anyTripAndStop returns a trip from the fixture together with one of its stops.
func anyTripAndStop(t *testing.T, api *RestAPI) (string, string) {
	t.Helper()

	ctx := context.Background()
	trips, err := api.GtfsManager.GtfsDB.Queries.ListTrips(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, trips, "fixture must contain trips")

	stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trips[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, stopTimes, "fixture trip must serve stops")

	return trips[0].ID, stopTimes[0].StopID
}

func anyRealTimeVehicleID(t *testing.T, api *RestAPI) string {
	t.Helper()

	// Must have a trip: an idle vehicle takes trip-for-vehicle's 404 branch,
	// which never reaches the situations this test is about.
	for _, vehicle := range api.GtfsManager.GetRealTimeVehicles() {
		if vehicle.ID != nil && vehicle.ID.ID != "" && vehicle.Trip != nil && vehicle.Trip.ID.ID != "" {
			return vehicle.ID.ID
		}
	}
	t.Fatal("fixture must contain a real-time vehicle running a trip")
	return ""
}

func TestSituationID(t *testing.T) {
	tests := []struct {
		name     string
		alertID  string
		agencyID string
		want     string
	}{
		{
			name:     "Bare alert ID gets the agency prefix",
			alertID:  "92239",
			agencyID: "1",
			want:     "1_92239",
		},
		{
			// Puget Sound's feed is exported by an OBA instance, so its alert
			// IDs already carry the prefix. Upstream reports "1_92239".
			name:     "Already-prefixed alert ID is left alone",
			alertID:  "1_92239",
			agencyID: "1",
			want:     "1_92239",
		},
		{
			name:     "A different agency's prefix is not mistaken for ours",
			alertID:  "40_92239",
			agencyID: "1",
			want:     "1_40_92239",
		},
		{
			name:     "Unknown agency leaves the ID untouched",
			alertID:  "92239",
			agencyID: "",
			want:     "92239",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, situationID(tt.alertID, tt.agencyID))
		})
	}
}
