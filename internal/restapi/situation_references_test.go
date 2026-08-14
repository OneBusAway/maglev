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
	"maglev.onebusaway.org/gtfsdb"
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

	// createTestApiWithRealTimeData returns before the first feed poll lands, and
	// anyRealTimeVehicleID needs a vehicle to pick from.
	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	// Agency-scoped so it matches whichever trips and stops each endpoint returns.
	rawAgencyID := "25"
	api.GtfsManager.AddAlertForTest(gogtfs.Alert{
		ID:               "situation-resolution-alert",
		InformedEntities: []gogtfs.AlertInformedEntity{{AgencyID: &rawAgencyID}},
		Header:           []gogtfs.AlertText{{Text: "Test Agency Alert", Language: "en"}},
	})
	const seededSituationID = "25_situation-resolution-alert"

	tripID, stopID := anyTripAndStop(t, api)
	vehicleID := anyRealTimeVehicleID(t, api)

	// Pinned to a weekday inside the RABA fixture's calendar range, which ended in
	// 2025; under the real clock the stop has no arrivals and the cases keyed off
	// it would assert nothing.
	serviceTime := time.Date(2025, 6, 12, 19, 0, 0, 0, time.UTC)
	// The handler reads serviceDate in the agency's timezone, so midnight has to
	// be that day's local midnight — UTC midnight lands on the 11th in Pacific.
	agencyLocation, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	localServiceTime := serviceTime.In(agencyLocation)
	serviceDate := time.Date(localServiceTime.Year(), localServiceTime.Month(), localServiceTime.Day(),
		0, 0, 0, 0, agencyLocation)

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "trip-details",
			url:  fmt.Sprintf("/api/where/trip-details/25_%s.json?key=TEST&includeStatus=true", tripID),
		},
		{
			name: "arrivals-and-departures-for-stop",
			url: fmt.Sprintf("/api/where/arrivals-and-departures-for-stop/25_%s.json?key=TEST&time=%d&minutesAfter=240",
				stopID, serviceTime.UnixMilli()),
		},
		{
			// The singular endpoint identifies one arrival, so it needs the trip
			// and service date the stop is being asked about.
			name: "arrival-and-departure-for-stop",
			url: fmt.Sprintf("/api/where/arrival-and-departure-for-stop/25_%s.json?key=TEST&tripId=25_%s&serviceDate=%d",
				stopID, tripID, serviceDate.UnixMilli()),
		},
		{
			name: "trip-for-vehicle",
			url:  fmt.Sprintf("/api/where/trip-for-vehicle/25_%s.json?key=TEST&includeStatus=true", vehicleID),
		},
		{
			// Without a status there are no situations resolved alongside it to
			// reuse, so the entry's own IDs must still resolve.
			name: "trip-details without status",
			url:  fmt.Sprintf("/api/where/trip-details/25_%s.json?key=TEST&includeStatus=false", tripID),
		},
		{
			name: "trip-for-vehicle without status",
			url:  fmt.Sprintf("/api/where/trip-for-vehicle/25_%s.json?key=TEST&includeStatus=false", vehicleID),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := callAPIHandler[map[string]any](t, api, tt.url)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var emitted []string
			collectSituationIDs(body, &emitted)
			referenced := referencedSituationIDs(t, body)

			for _, id := range emitted {
				assert.True(t, referenced[id],
					"situationId %q must resolve to an entry in references.situations", id)
			}
			require.Contains(t, emitted, seededSituationID,
				"expected the seeded alert to surface as a situationId")
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

// TestTripSituationRefsAgencyFallback covers both paths tripSituationRefs takes
// for a trip it has already loaded: reading the agency out of routeAgencyMap,
// and falling back to a lookup when that map has no entry for the trip's route.
// Both must produce the same combined-form ID — an unresolved agency would emit
// the bare alert ID, which resolves against nothing the response carries.
func TestTripSituationRefsAgencyFallback(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	ctx := context.Background()
	tripID, _ := anyTripAndStop(t, api)
	trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	require.NoError(t, err)

	rawAgencyID := "25"
	api.GtfsManager.AddAlertForTest(gogtfs.Alert{
		ID:               "trip-situation-refs-alert",
		InformedEntities: []gogtfs.AlertInformedEntity{{AgencyID: &rawAgencyID}},
		Header:           []gogtfs.AlertText{{Text: "Test Agency Alert", Language: "en"}},
	})
	const wantSituationID = "25_trip-situation-refs-alert"

	tripsByID := map[string]gtfsdb.Trip{tripID: trip}

	tests := []struct {
		name           string
		routeAgencyMap map[string]string
	}{
		{name: "route agency known", routeAgencyMap: map[string]string{trip.RouteID: rawAgencyID}},
		{name: "route agency unknown", routeAgencyMap: map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := api.tripSituationRefs(ctx, tripID, tripsByID, tt.routeAgencyMap)

			assert.Contains(t, situationIDsFromRefs(refs), wantSituationID,
				"the situation ID must carry the agency prefix on both paths")
		})
	}
}

// TestSituationRefsFromAlertsAgencyScope covers an alert reachable through both
// a route and a stop that belong to different agencies. Both paths must produce
// the same ID, or references.situations carries the alert twice and an entry's
// situationIds resolve to only one of the two.
func TestSituationRefsFromAlertsAgencyScope(t *testing.T) {
	informedAgencyID := "1"
	stopID := "10190"

	tests := []struct {
		name             string
		alert            gogtfs.Alert
		callerAgencyIDs  []string
		wantSituationIDs []string
		wantCollected    []string
	}{
		{
			name: "The alert's own agency wins over whichever lookup found it",
			alert: gogtfs.Alert{
				ID:               "86736",
				InformedEntities: []gogtfs.AlertInformedEntity{{AgencyID: &informedAgencyID}, {StopID: &stopID}},
			},
			callerAgencyIDs:  []string{"1", "40"},
			wantSituationIDs: []string{"1_86736", "1_86736"},
			wantCollected:    []string{"1_86736"},
		},
		{
			// Known limitation rather than desired behaviour: with no agency to
			// read off the alert, the two paths have nothing in common to agree
			// on. Every alert in the feeds maglev serves names one.
			name: "An alert naming no agency falls back to the caller's",
			alert: gogtfs.Alert{
				ID:               "86736",
				InformedEntities: []gogtfs.AlertInformedEntity{{StopID: &stopID}},
			},
			callerAgencyIDs:  []string{"1", "40"},
			wantSituationIDs: []string{"1_86736", "40_86736"},
			wantCollected:    []string{"1_86736", "40_86736"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// One collector across both paths, as a handler reaching the same
			// alert through a route and through a stop would have.
			collected := newSituationCollector()

			for i, callerAgencyID := range tt.callerAgencyIDs {
				refs := situationRefsFromAlerts([]gogtfs.Alert{tt.alert}, callerAgencyID)
				require.Len(t, refs, 1)
				assert.Equal(t, tt.wantSituationIDs[i], refs[0].ID,
					"alert reached with caller agency %q", callerAgencyID)
				collected.addRefs(refs)
			}

			collectedIDs := make([]string, 0, len(collected.refs))
			for _, ref := range collected.refs {
				collectedIDs = append(collectedIDs, ref.ID)
			}
			assert.Equal(t, tt.wantCollected, collectedIDs,
				"references.situations must carry the alert once per distinct ID")
		})
	}
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
