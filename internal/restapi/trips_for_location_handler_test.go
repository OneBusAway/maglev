package restapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/clock"
)

const (
	tripsForLocationLat = 40.5865
	tripsForLocationLon = -122.3917
)

// tripsForLocationURL builds the query URL using the RABA-area lat/lon constants,
// the given spans, and any extra "key=value" query params.
func tripsForLocationURL(latSpan, lonSpan float64, extras ...string) string {
	url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&latSpan=%f&lonSpan=%f",
		tripsForLocationLat, tripsForLocationLon, latSpan, lonSpan)
	for _, e := range extras {
		url += "&" + e
	}
	return url
}

func TestTripsForLocationHandler_DifferentAreas(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name        string
		latSpan     float64
		lonSpan     float64
		minExpected int
		maxExpected int
	}{
		{name: "Transit Center Area", latSpan: 1.0, lonSpan: 1.0, minExpected: 0, maxExpected: 50},
		{name: "Wide Area Coverage", latSpan: 2.0, lonSpan: 3.0, minExpected: 0, maxExpected: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tripsForLocationURL(tt.latSpan, tt.lonSpan, "includeSchedule=true")

			resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.GreaterOrEqual(t, len(model.Data.List), tt.minExpected)
			assert.LessOrEqual(t, len(model.Data.List), tt.maxExpected)

			for _, entry := range model.Data.List {
				assert.NotEmpty(t, entry.TripId)
				assert.NotNil(t, entry.SituationIds)

				if entry.Schedule != nil {
					assert.NotEmpty(t, entry.Schedule.TimeZone)
					for _, st := range entry.Schedule.StopTimes {
						assert.NotEmpty(t, st.StopID)
					}
				}
			}
		})
	}
}

// TestTripsForLocationHandler_ReferencesAreConsistent consolidates the previous
// per-aspect reference tests (stops/routes/agencies cross-references, combined IDs,
// orphaned stops, direction populated) into one fetch + structured assertions.
func TestTripsForLocationHandler_ReferencesAreConsistent(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=true")

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	refs := model.Data.References
	require.NotEmpty(t, refs.Stops, "expected stops in references")

	routeIDs := make(map[string]bool, len(refs.Routes))
	for _, r := range refs.Routes {
		routeIDs[r.ID] = true
	}
	agencyIDs := make(map[string]bool, len(refs.Agencies))
	for _, a := range refs.Agencies {
		agencyIDs[a.ID] = true
	}

	nonEmptyDirections := 0
	for _, stop := range refs.Stops {
		assert.NotEmpty(t, stop.ID, "orphaned stop with empty ID slipped through")
		assert.Contains(t, stop.ID, "_", "stop ID %q should be in {agencyID}_{rawID} format", stop.ID)
		for _, rid := range stop.RouteIDs {
			assert.True(t, routeIDs[rid], "route %q referenced by stop %q must appear in references.routes", rid, stop.ID)
		}
		if stop.Direction != "" {
			nonEmptyDirections++
		}
	}
	assert.Greater(t, nonEmptyDirections, 0, "at least some stops should have a non-empty direction from DirectionCalculator")

	for _, route := range refs.Routes {
		assert.True(t, agencyIDs[route.AgencyID],
			"agency %q referenced by route %q must appear in references.agencies", route.AgencyID, route.ID)
	}
}

func TestTripsForLocationHandler_ScheduleInclusion(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name            string
		includeSchedule bool
	}{
		{name: "With Schedule", includeSchedule: true},
		{name: "Without Schedule", includeSchedule: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tripsForLocationURL(0.1, 0.1, fmt.Sprintf("includeSchedule=%v", tt.includeSchedule))

			resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			for _, entry := range model.Data.List {
				if tt.includeSchedule {
					if assert.NotNil(t, entry.Schedule, "schedule should be present when includeSchedule=true") {
						assert.NotEmpty(t, entry.Schedule.TimeZone)
					}
				} else {
					assert.Nil(t, entry.Schedule, "schedule should be omitted when includeSchedule=false")
				}
			}
		})
	}
}

func TestTripsForLocationHandler_StatusInclusion(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name          string
		includeStatus bool
	}{
		{name: "With Status", includeStatus: true},
		{name: "Without Status", includeStatus: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tripsForLocationURL(0.2, 0.2, fmt.Sprintf("includeStatus=%v", tt.includeStatus))

			resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, model.Data.List, "expected at least one trip in the response to verify status behavior")

			for _, entry := range model.Data.List {
				if tt.includeStatus {
					if assert.NotNil(t, entry.Status, "expected status when includeStatus=true") {
						assert.NotEmpty(t, entry.Status.Phase)
					}
				} else {
					assert.Nil(t, entry.Status, "expected status to be omitted when includeStatus=false")
				}
			}
		})
	}
}

func TestTripsForLocationHandler_MissingParameters(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name string
		url  string
	}{
		{"Missing lat", "/api/where/trips-for-location.json?key=TEST&lon=-122.426966&latSpan=0.01&lonSpan=0.01"},
		{"Missing lon", "/api/where/trips-for-location.json?key=TEST&lat=40.583321&latSpan=0.01&lonSpan=0.01"},
		{"Missing both", "/api/where/trips-for-location.json?key=TEST&latSpan=0.01&lonSpan=0.01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripsForLocationResponse](t, api, tt.url)

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
		})
	}
}

func TestTripsForLocationHandler_IncludeReferences(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	// Verify standard behavior (includeReferences=true implicitly)
	urlTrue := tripsForLocationURL(2.0, 3.0, "includeSchedule=true")
	respTrue, modelTrue := callAPIHandler[TripsForLocationResponse](t, api, urlTrue)
	require.Equal(t, http.StatusOK, respTrue.StatusCode)
	require.NotEmpty(t, modelTrue.Data.List, "list must be populated when includeReferences is true or absent")
	assert.NotEmpty(t, modelTrue.Data.References.Stops, "Stops should be populated when includeReferences is true or absent")
	assert.NotEmpty(t, modelTrue.Data.References.Routes, "Routes should be populated when includeReferences is true or absent")
	assert.NotEmpty(t, modelTrue.Data.References.Agencies, "Agencies should be populated when includeReferences is true or absent")

	// Verify explicit includeReferences=false behavior
	urlFalse := tripsForLocationURL(2.0, 3.0, "includeSchedule=true", "includeReferences=false")
	respFalse, modelFalse := callAPIHandler[TripsForLocationResponse](t, api, urlFalse)
	require.Equal(t, http.StatusOK, respFalse.StatusCode)

	// Ensure the list of trip entries is preserved and matches standard behavior
	assert.NotEmpty(t, modelFalse.Data.List, "list must still be populated when includeReferences=false")
	assert.Equal(t, len(modelTrue.Data.List), len(modelFalse.Data.List), "list length must match whether includeReferences is true or false")

	// Verify the references object is present but all arrays are empty (as per spec)
	assert.Empty(t, modelFalse.Data.References.Agencies, "Agencies must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Routes, "Routes must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Stops, "Stops must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.StopTimes, "StopTimes must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Trips, "Trips must be empty when includeReferences=false")
	assert.Empty(t, modelFalse.Data.References.Situations, "Situations must be empty when includeReferences=false")
}

func TestTripsForLocationHandler_TimeParameter(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	t.Run("Valid Time Parameter", func(t *testing.T) {
		loc, err := time.LoadLocation("America/Los_Angeles")
		require.NoError(t, err)

		targetTime := time.Date(2025, 6, 15, 14, 30, 0, 0, loc)
		targetMidnight := time.Date(targetTime.Year(), targetTime.Month(), targetTime.Day(), 0, 0, 0, 0, loc)
		url := tripsForLocationURL(1.0, 1.0, fmt.Sprintf("includeStatus=true&time=%d", targetTime.UnixMilli()))

		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, model.Data.List, "expected at least one trip entry to verify ServiceDate")
		for _, entry := range model.Data.List {
			assert.Equal(t, targetMidnight.UnixMilli(), entry.ServiceDate, "entry.ServiceDate should match midnight of the requested time parameter")
			if entry.Status != nil {
				assert.Equal(t, targetMidnight.UnixMilli(), entry.Status.ServiceDate.UnixMilli(), "entry.Status.ServiceDate should match midnight of the requested time parameter")
			}
		}
	})

	t.Run("Invalid Time Parameter", func(t *testing.T) {
		url := tripsForLocationURL(1.0, 1.0, "time=invalid-time-format")
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, http.StatusBadRequest, model.Code)
	})
}
