package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/clock"
	internalgtfs "maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
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

func TestTripsForLocationServiceDateUsesTripAgencyTimezone(t *testing.T) {
	currentTime := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	chicago, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)

	assert.Equal(t,
		time.Date(2026, 8, 10, 0, 0, 0, 0, losAngeles),
		serviceDateMidnight(currentTime, losAngeles))
	assert.Equal(t,
		time.Date(2026, 8, 10, 0, 0, 0, 0, chicago),
		serviceDateMidnight(currentTime, chicago))
	assert.Equal(t, 2*time.Hour,
		serviceDateMidnight(currentTime, losAngeles).Sub(serviceDateMidnight(currentTime, chicago)),
		"a Chicago trip must not use Los Angeles midnight")
}

func TestTripsForLocationHandler_UsesEachTripAgencyTimezone(t *testing.T) {
	currentTime := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)
	api := createTestApiWithGTFSFixture(t, clock.NewMockClock(currentTime),
		"trips-for-location-multi-timezone.zip", multiTimezoneTripsForLocationFiles())
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	latitude := float32(tripsForLocationLat)
	longitude := float32(tripsForLocationLon)
	for _, trip := range []struct {
		tripID  string
		routeID string
	}{
		{tripID: "la-trip", routeID: "la-route"},
		{tripID: "chicago-trip", routeID: "chicago-route"},
	} {
		api.GtfsManager.MockAddVehicleWithOptions("vehicle-"+trip.tripID, trip.tripID, trip.routeID,
			internalgtfs.MockVehicleOptions{
				Timestamp: &currentTime,
				Position: &gtfs.Position{
					Latitude:  &latitude,
					Longitude: &longitude,
				},
			})
	}

	url := tripsForLocationURL(0.1, 0.1,
		"includeSchedule=true",
		"includeStatus=true",
		fmt.Sprintf("time=%d", currentTime.UnixMilli()))
	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, model.Data.List, 2)

	losAngeles, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	chicago, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	expectedMidnights := map[string]time.Time{
		"la_la-trip":           serviceDateMidnight(currentTime, losAngeles),
		"chicago_chicago-trip": serviceDateMidnight(currentTime, chicago),
	}
	expectedTimezones := map[string]string{
		"la_la-trip":           losAngeles.String(),
		"chicago_chicago-trip": chicago.String(),
	}

	for _, entry := range model.Data.List {
		expectedMidnight, found := expectedMidnights[entry.TripId]
		require.True(t, found, "unexpected trip %q", entry.TripId)
		assert.Equal(t, expectedMidnight.UnixMilli(), entry.ServiceDate)
		require.NotNil(t, entry.Schedule)
		assert.Equal(t, expectedTimezones[entry.TripId], entry.Schedule.TimeZone)
		assert.Empty(t, entry.Schedule.NextTripId,
			"trips with a shared block ID must remain isolated by agency")
		assert.Empty(t, entry.Schedule.PreviousTripId,
			"trips with a shared block ID must remain isolated by agency")
		require.NotNil(t, entry.Status)
		assert.Equal(t, expectedMidnight.UnixMilli(), entry.Status.ServiceDate.UnixMilli())
	}
}

func multiTimezoneTripsForLocationFiles() map[string]string {
	return map[string]string{
		"agency.txt": "agency_id,agency_name,agency_url,agency_timezone\n" +
			"la,Los Angeles Transit,http://example.com/la,America/Los_Angeles\n" +
			"chicago,Chicago Transit,http://example.com/chicago,America/Chicago\n",
		"routes.txt": "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"la-route,la,LA,Los Angeles Route,3\n" +
			"chicago-route,chicago,CH,Chicago Route,3\n",
		"calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"shared-service,1,1,1,1,1,1,1,20240101,20991231\n",
		"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\n" +
			"la-stop,Los Angeles Stop,40.5865,-122.3917\n" +
			"chicago-stop,Chicago Stop,40.5865,-122.3917\n",
		"trips.txt": "route_id,service_id,trip_id,trip_headsign,block_id\n" +
			"la-route,shared-service,la-trip,Los Angeles,shared-block\n" +
			"chicago-route,shared-service,chicago-trip,Chicago,shared-block\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\n" +
			"la-trip,05:00:00,05:00:00,la-stop,1\n" +
			"chicago-trip,07:00:00,07:00:00,chicago-stop,1\n",
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
	require.NotEmpty(t, refs.Trips, "expected trips in references because includeTrip defaults to true when omitted")

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

	for _, trip := range refs.Trips {
		assert.NotEmpty(t, trip.ID, "trip with empty ID found in references")
		assert.Contains(t, trip.ID, "_", "trip ID %q should be in {agencyID}_{rawID} format", trip.ID)
		assert.True(t, routeIDs[trip.RouteID], "route %q referenced by trip %q must appear in references.routes", trip.RouteID, trip.ID)
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
		name           string
		statusParam    string
		expectedStatus bool
	}{
		{name: "With Status (Explicit true)", statusParam: "includeStatus=true", expectedStatus: true},
		{name: "With Status (Integer 1)", statusParam: "includeStatus=1", expectedStatus: true},
		{name: "With Status (Uppercase TRUE)", statusParam: "includeStatus=TRUE", expectedStatus: true},
		{name: "Without Status (Explicit false)", statusParam: "includeStatus=false", expectedStatus: false},
		{name: "Without Status (Invalid value)", statusParam: "includeStatus=invalid_value", expectedStatus: false},
		{name: "Without Status (Default/Omitted)", statusParam: "", expectedStatus: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tripsForLocationURL(0.2, 0.2)
			if tt.statusParam != "" {
				url += "&" + tt.statusParam
			}

			resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, model.Data.List, "expected at least one trip in the response to verify status behavior")

			for _, entry := range model.Data.List {
				if tt.expectedStatus {
					if assert.NotNil(t, entry.Status, "expected status when includeStatus=true/1/TRUE") {
						assert.NotEmpty(t, entry.Status.Phase)
					}
				} else {
					assert.Nil(t, entry.Status, "expected status to be omitted when includeStatus=false/invalid/omitted")
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
		{"Missing/zero coordinates (lat=0&lon=0)", "/api/where/trips-for-location.json?key=TEST&lat=0&lon=0&latSpan=0.01&lonSpan=0.01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripsForLocationResponse](t, api, tt.url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
			assert.True(t, model.Data.OutOfRange)
			assert.Empty(t, model.Data.List)
		})
	}
}

func TestTripsForLocationHandler_ParseAndValidateRequest(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name                string
		queryString         string
		expectedIncludeTrip bool
	}{
		{
			name:                "includeTrip omitted defaults to true",
			queryString:         "lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1",
			expectedIncludeTrip: true,
		},
		{
			name:                "includeTrip=false returns false",
			queryString:         "lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1&includeTrip=false",
			expectedIncludeTrip: false,
		},
		{
			name:                "includeTrip=true returns true",
			queryString:         "lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1&includeTrip=true",
			expectedIncludeTrip: true,
		},
		{
			name:                "includeTrip=invalid_value safely defaults to false",
			queryString:         "lat=40.5865&lon=-122.3917&latSpan=0.1&lonSpan=0.1&includeTrip=invalid_value",
			expectedIncludeTrip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/where/trips-for-location.json?"+tt.queryString, nil)

			parsedReq, fieldErrors, err := api.parseAndValidateRequest(req)

			assert.Empty(t, fieldErrors)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedIncludeTrip, parsedReq.IncludeTrip)
		})
	}
}

func TestTripsForLocationHandler_TripInclusion(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name         string
		includeParam string
		expected     bool
	}{
		{name: "With Trip (Default/Omitted)", includeParam: "", expected: true},
		{name: "With Trip (Explicit true)", includeParam: "includeTrip=true", expected: true},
		{name: "Without Trip (Explicit false)", includeParam: "includeTrip=false", expected: false},
		{name: "Without Trip (Invalid value)", includeParam: "includeTrip=invalid_value", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tripsForLocationURL(2.0, 3.0, "includeSchedule=true")
			if tt.includeParam != "" {
				url += "&" + tt.includeParam
			}

			resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			if tt.expected {
				assert.NotEmpty(t, model.Data.References.Trips, "trips should be present in references when includeTrip is true or omitted")
			} else {
				assert.Empty(t, model.Data.References.Trips, "trips should be omitted when includeTrip=false or invalid")
			}
		})
	}
}

func TestTripsForLocationHandler_BoundsClamping(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	tests := []struct {
		name string
		url  string
	}{
		{"Radius over 10km (15km)", "/api/where/trips-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=15000"},
		{"Radius exceeding max 20km (25km)", "/api/where/trips-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&radius=25000"},
		{"Spans exceeding max 5 degrees (6.0)", "/api/where/trips-for-location.json?key=TEST&lat=40.583321&lon=-122.426966&latSpan=6.0&lonSpan=6.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripsForLocationResponse](t, api, tt.url)

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, http.StatusOK, model.Code)
			assert.False(t, model.Data.LimitExceeded)
			assert.NotEmpty(t, model.Data.List, "clamped search should return trip results in the test area")
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

func TestTripsForLocationHandler_RadiusAndPrecedence(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	t.Run("Radius Only without Spans", func(t *testing.T) {
		url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&radius=5000", tripsForLocationLat, tripsForLocationLon)
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.False(t, model.Data.LimitExceeded)
		assert.False(t, model.Data.OutOfRange)
		assert.NotNil(t, model.Data.List)
	})

	t.Run("Radius Precedence over Spans", func(t *testing.T) {
		// Supplying both a very tiny radius (1m) and a very large span (2 degrees).
		// Per OBA spec, radius takes precedence over span when both are supplied.
		// A 2 degree span would return trips, but a 1m radius will return empty.
		url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&radius=1&latSpan=2.0&lonSpan=2.0", tripsForLocationLat, tripsForLocationLon)
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.False(t, model.Data.OutOfRange)
		assert.Empty(t, model.Data.List, "Expected empty list since 1m radius precedence should filter out trips otherwise caught by the 2 degree span")
	})

	t.Run("Default Radius Fallback when no Spans or Radius", func(t *testing.T) {
		// When neither radius nor valid spans (>0) are specified, BoundsFromParams defaults to 10km (DefaultSearchRadiusInMeters).
		url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f", tripsForLocationLat, tripsForLocationLon)
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.False(t, model.Data.OutOfRange)
		assert.NotNil(t, model.Data.List)
	})
}

func TestTripsForLocationHandler_InvalidParametersAndValidationErrors(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name string
		url  string
	}{
		{"Invalid lat out of range (>90)", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=95.0&lon=-122.39&latSpan=0.1&lonSpan=0.1"},
		{"Invalid lat non-numeric", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=invalid&lon=-122.39&latSpan=0.1&lonSpan=0.1"},
		{"Invalid lon out of range (<-180)", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=40.58&lon=-185.0&latSpan=0.1&lonSpan=0.1"},
		{"Invalid lon non-numeric", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=40.58&lon=invalid&latSpan=0.1&lonSpan=0.1"},
		{"Negative radius", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=40.58&lon=-122.39&radius=-100"},
		{"Negative latSpan", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=40.58&lon=-122.39&latSpan=-1.0&lonSpan=0.1"},
		{"Negative lonSpan", "/api/where/trips-for-location.json?key=org.onebusaway.iphone&lat=40.58&lon=-122.39&latSpan=0.1&lonSpan=-1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[TripsForLocationResponse](t, api, tt.url)

			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
			assert.NotEmpty(t, model.Text)
			assert.Equal(t, models.APIVersion, model.Version)
		})
	}
}

func TestTripsForLocationHandler_TimeParameterVariations(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	t.Run("Date String YYYY-MM-DD", func(t *testing.T) {
		loc, err := time.LoadLocation("America/Los_Angeles")
		require.NoError(t, err)

		dateStr := "2025-06-15"
		parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, loc)
		require.NoError(t, err)
		targetMidnight := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, loc)

		url := tripsForLocationURL(1.0, 1.0, fmt.Sprintf("includeStatus=true&time=%s", dateStr))
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, model.Data.List, "expected at least one trip entry to verify ServiceDate")
		for _, entry := range model.Data.List {
			assert.Equal(t, targetMidnight.UnixMilli(), entry.ServiceDate, "entry.ServiceDate should match midnight of the requested date string")
			if entry.Status != nil {
				assert.Equal(t, targetMidnight.UnixMilli(), entry.Status.ServiceDate.UnixMilli())
			}
		}
	})

	t.Run("Query Time Far Outside Service Window", func(t *testing.T) {
		// Querying at epoch millis for Jan 1, 2010. ServiceDate on all returned active trips must reflect this historical timestamp.
		const historicalMillis = int64(1262332800000)
		url := tripsForLocationURL(1.0, 1.0, fmt.Sprintf("time=%d", historicalMillis))
		resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, http.StatusOK, model.Code)
		assert.False(t, model.Data.OutOfRange)
		require.NotEmpty(t, model.Data.List, "expected at least one entry to verify ServiceDate equality")
		for _, entry := range model.Data.List {
			assert.Equal(t, historicalMillis, entry.ServiceDate, "entry.ServiceDate should match the historical timestamp parameter")
		}
	})
}

func TestTripsForLocationHandler_ZeroVehiclesOrStaticOnly(t *testing.T) {
	// Create API with only static GTFS data (no real-time vehicles injected)
	api := createTestApi(t)
	defer api.Shutdown()

	url := tripsForLocationURL(1.0, 1.0, "includeSchedule=true")
	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.False(t, model.Data.OutOfRange)
	assert.Empty(t, model.Data.List, "without real-time vehicle positions, active trips list should be empty per OBA real-time behavior")
	assert.NotNil(t, model.Data.References.Stops, "references slice should not be nil even when empty")
	assert.NotNil(t, model.Data.References.Routes, "references slice should not be nil even when empty")
	assert.NotNil(t, model.Data.References.Agencies, "references slice should not be nil even when empty")
	assert.NotNil(t, model.Data.References.Trips, "references slice should not be nil even when empty")
}

func TestTripsForLocationHandler_IncludeTripFalseWithReferencesTrue(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=true", "includeTrip=false")
	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, model.Data.List)
	assert.NotEmpty(t, model.Data.References.Stops, "Stops should still be populated when includeTrip=false")
	assert.NotEmpty(t, model.Data.References.Routes, "Routes should still be populated when includeTrip=false")
	assert.NotEmpty(t, model.Data.References.Agencies, "Agencies should still be populated when includeTrip=false")
	assert.Empty(t, model.Data.References.Trips, "Trips should be omitted from references when includeTrip=false")
}

func TestTripsForLocationHandler_ScheduleDetailsAndDistances(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	time.Sleep(500 * time.Millisecond)

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=true")
	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List)

	for _, entry := range model.Data.List {
		require.NotNil(t, entry.Schedule, "schedule should be present when includeSchedule=true")
		assert.NotEmpty(t, entry.Schedule.StopTimes, "stop times should be populated")

		var prevDist float64 = -1
		for _, st := range entry.Schedule.StopTimes {
			assert.NotEmpty(t, st.StopID)
			assert.GreaterOrEqual(t, st.DistanceAlongTrip, 0.0, "distance along trip should be non-negative")
			if prevDist >= 0 {
				assert.GreaterOrEqual(t, st.DistanceAlongTrip, prevDist, "distances should be non-decreasing along the trip sequence")
			}
			prevDist = st.DistanceAlongTrip
		}

		if entry.Schedule.NextTripId != "" {
			assert.Contains(t, entry.Schedule.NextTripId, "_", "next trip ID must be in combined {agencyID}_{rawID} format")
		}
		if entry.Schedule.PreviousTripId != "" {
			assert.Contains(t, entry.Schedule.PreviousTripId, "_", "previous trip ID must be in combined {agencyID}_{rawID} format")
		}
	}
}

func TestTripsForLocationHandler_ContextCancellation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	t.Run("Context Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately to simulate a client cancellation
		req := httptest.NewRequest(http.MethodGet, tripsForLocationURL(1.0, 1.0), nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		api.tripsForLocationHandler(rec, req)

		// When context is canceled, clientCanceledResponse logs Info and does not write a header or body.
		assert.Empty(t, rec.Body.String(), "no body should be written when client cancels request")
	})

	t.Run("Context Deadline Exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 0) // Deadline already exceeded
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, tripsForLocationURL(1.0, 1.0), nil).WithContext(ctx)
		rec := httptest.NewRecorder()

		api.tripsForLocationHandler(rec, req)

		// When context deadline is exceeded, clientCanceledResponse writes 504 Gateway Timeout.
		assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
		assert.Contains(t, rec.Body.String(), "gateway timeout")
	})
}

// TestTripsForLocationHandler_SituationReferences verifies that every
// situationId emitted on a list entry resolves to an entry in
// references.situations.
func TestTripsForLocationHandler_SituationReferences(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// createTestApiWithRealTimeData returns before the first feed poll lands, and
	// this endpoint selects trips from live vehicles, so wait for one to arrive.
	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	// Real-time alerts carry the raw (un-prefixed) agency ID from the feed.
	rawAgencyID := "25"
	api.GtfsManager.AddAlertForTest(gtfs.Alert{
		ID:               "test-alert-trips-for-location",
		InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: &rawAgencyID}},
		Header:           []gtfs.AlertText{{Text: "Test Agency Alert", Language: "en"}},
	})

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, tripsForLocationURL(2.0, 3.0))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected trips so situation references can be asserted")

	referenced := make(map[string]bool, len(model.Data.References.Situations))
	for _, situation := range model.Data.References.Situations {
		referenced[situation.ID] = true
	}

	var emitted []string
	for _, entry := range model.Data.List {
		for _, id := range entry.SituationIds {
			emitted = append(emitted, id)
			assert.True(t, referenced[id], "situationId %q must resolve to a situation reference", id)
		}
	}
	require.Contains(t, emitted, "25_test-alert-trips-for-location",
		"expected the seeded alert to surface as a situationId")
}

// TestTripsForLocationHandler_ScheduleStopsAreReferenced verifies that every
// stop named by an entry's schedule resolves to an entry in references.stops.
func TestTripsForLocationHandler_ScheduleStopsAreReferenced(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// createTestApiWithRealTimeData returns before the first feed poll lands, and
	// this endpoint selects trips from live vehicles, so wait for one to arrive.
	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=true", "includeStatus=true")

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected trips so stop references can be asserted")

	referenced := make(map[string]bool, len(model.Data.References.Stops))
	for _, stop := range model.Data.References.Stops {
		referenced[stop.ID] = true
	}

	pointedAt := make(map[string]bool)
	sawStopTime := false
	for _, entry := range model.Data.List {
		require.NotNil(t, entry.Schedule, "includeSchedule=true should populate the schedule")
		if entry.Status != nil {
			pointedAt[entry.Status.ClosestStop] = true
			pointedAt[entry.Status.NextStop] = true
		}
		for _, stopTime := range entry.Schedule.StopTimes {
			pointedAt[stopTime.StopID] = true
			sawStopTime = true
			assert.True(t, referenced[stopTime.StopID],
				"stop %q named by trip %q must appear in references.stops", stopTime.StopID, entry.TripId)
		}
	}
	require.True(t, sawStopTime, "expected at least one scheduled stop time to assert against")

	// And nothing extra: a reference stamped with an ID no entry used would
	// resolve for no one.
	for _, stop := range model.Data.References.Stops {
		assert.True(t, pointedAt[stop.ID],
			"reference %q is not the ID any entry referred to that stop by", stop.ID)
	}
}

// TestTripsForLocationHandler_StatusStopsAreReferenced covers the path where a
// status is the only thing naming a stop: with includeSchedule=false there are
// no stop times to collect, so closestStop and nextStop are the whole of what
// references.stops has to resolve.
func TestTripsForLocationHandler_StatusStopsAreReferenced(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=false", "includeStatus=true")

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected trips so status stop references can be asserted")

	referenced := make(map[string]bool, len(model.Data.References.Stops))
	for _, stop := range model.Data.References.Stops {
		referenced[stop.ID] = true
	}

	sawStatusStop := false
	for _, entry := range model.Data.List {
		require.Nil(t, entry.Schedule, "includeSchedule=false should leave the schedule out")
		if entry.Status == nil {
			continue
		}
		for _, stopID := range []string{entry.Status.ClosestStop, entry.Status.NextStop} {
			if stopID == "" {
				continue
			}
			sawStatusStop = true
			assert.True(t, referenced[stopID],
				"stop %q named by trip %q's status must appear in references.stops", stopID, entry.TripId)
		}
	}
	require.True(t, sawStatusStop, "expected at least one status stop to assert against")
}

// TestTripsForLocationHandler_UnreferencedStopsAreOmitted verifies that stops
// merely inside the search bounds are not emitted as references when nothing in
// the response points at them.
func TestTripsForLocationHandler_UnreferencedStopsAreOmitted(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// createTestApiWithRealTimeData returns before the first feed poll lands, and
	// this endpoint selects trips from live vehicles, so wait for one to arrive.
	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	url := tripsForLocationURL(2.0, 3.0, "includeSchedule=false", "includeStatus=false")

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, url)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "expected trips so the reference set is meaningful")
	assert.Empty(t, model.Data.References.Stops,
		"no schedule or status means nothing in the response refers to a stop")
}

// TestTripsForLocationHandler_CandidateStopsAreNotCapped verifies that the
// candidate stop set is not truncated, which previously dropped trips serving
// only stops beyond the cap. The RABA fixture's own vehicles all happen to
// serve stops early in the in-bounds ordering, so a mock vehicle is placed on
// a trip that serves only a stop past the old cap: if the cap ever returns,
// that stop drops out of the candidate set and this trip disappears from the
// response.
func TestTripsForLocationHandler_CandidateStopsAreNotCapped(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.RealClock{})
	defer cleanup()

	// createTestApiWithRealTimeData returns before the first feed poll lands, and
	// this endpoint selects trips from live vehicles, so wait for one to arrive.
	require.Eventually(t, func() bool {
		return len(api.GtfsManager.GetRealTimeVehicles()) > 0
	}, 10*time.Second, 20*time.Millisecond, "real-time vehicles never loaded")

	ctx := context.Background()
	params := &internalgtfs.LocationParams{
		Lat:     tripsForLocationLat,
		Lon:     tripsForLocationLon,
		LatSpan: 2.0,
		LonSpan: 3.0,
	}

	uncapped := api.GtfsManager.GetStopsInBounds(ctx, params, 0, true)
	require.Greater(t, len(uncapped), models.DefaultMaxCountForStops,
		"fixture must hold more in-bounds stops than the old cap for this test to mean anything")

	cappedStopIDs := make(map[string]bool, models.DefaultMaxCountForStops)
	for _, stop := range uncapped[:models.DefaultMaxCountForStops] {
		cappedStopIDs[stop.ID] = true
	}

	lateStop, lateTripID, lateTripRouteID := findTripServingOnlyLateStop(
		t, ctx, api, uncapped[models.DefaultMaxCountForStops:], cappedStopIDs)

	lat, lon := float32(lateStop.Lat), float32(lateStop.Lon)
	api.GtfsManager.MockAddVehicleWithOptions("cap-regression-vehicle", lateTripID, lateTripRouteID,
		internalgtfs.MockVehicleOptions{
			Position: &gtfs.Position{Latitude: &lat, Longitude: &lon},
		})

	bounds := internalgtfs.BoundsFromParams(params, true)

	resp, model := callAPIHandler[TripsForLocationResponse](t, api, tripsForLocationURL(2.0, 3.0))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	returned := make(map[string]bool, len(model.Data.List))
	for _, entry := range model.Data.List {
		_, tripID, err := utils.ExtractAgencyIDAndCodeID(entry.TripId)
		require.NoError(t, err)
		returned[tripID] = true
	}

	assert.True(t, returned[lateTripID],
		"trip %q serves only stop %q, which lies beyond the old %d-stop cap, and must still be returned",
		lateTripID, lateStop.ID, models.DefaultMaxCountForStops)

	// Every real-time vehicle positioned inside the bounds must be represented.
	for _, vehicle := range api.GtfsManager.GetRealTimeVehicles() {
		if vehicle.Trip == nil || vehicle.Position == nil ||
			vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
			continue
		}
		lat, lon := float64(*vehicle.Position.Latitude), float64(*vehicle.Position.Longitude)
		if lat < bounds.MinLat || lat > bounds.MaxLat || lon < bounds.MinLon || lon > bounds.MaxLon {
			continue
		}

		tripID := vehicle.Trip.ID.ID
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}

		assert.True(t, returned[tripID], "in-bounds vehicle's trip %q must be returned", tripID)
	}
}

// findTripServingOnlyLateStop returns a stop past the old cap together with a
// trip that serves it but serves none of the stops within the old cap, so the
// trip is only discoverable through the uncapped candidate stop set.
func findTripServingOnlyLateStop(
	t *testing.T,
	ctx context.Context,
	api *RestAPI,
	lateStops []gtfsdb.Stop,
	cappedStopIDs map[string]bool,
) (stop gtfsdb.Stop, tripID string, routeID string) {
	t.Helper()

	for _, stop := range lateStops {
		tripIDs, err := api.GtfsManager.GtfsDB.Queries.GetTripIDsForStops(ctx, []string{stop.ID})
		require.NoError(t, err)

		for _, candidateTripID := range tripIDs {
			stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, candidateTripID)
			require.NoError(t, err)

			servesCappedStop := false
			for _, stopTime := range stopTimes {
				if cappedStopIDs[stopTime.StopID] {
					servesCappedStop = true
					break
				}
			}
			if servesCappedStop {
				continue
			}

			trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, candidateTripID)
			require.NoError(t, err)
			return stop, candidateTripID, trip.RouteID
		}
	}

	require.Fail(t, "no trip in the fixture serves only stops beyond the old cap")
	return gtfsdb.Stop{}, "", ""
}

// TestCandidateTripIDsForStops_BatchesLargeStopSets covers the uncapped stop
// set exceeding one query's bind variable budget: the batches together must
// return what a single query would.
func TestCandidateTripIDsForStops_BatchesLargeStopSets(t *testing.T) {
	api := createTestApi(t)
	ctx := context.Background()

	stops, err := api.GtfsManager.GtfsDB.Queries.ListStops(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stops)

	stopIDs := make([]string, 0, idsPerBatchedQuery+len(stops))
	for len(stopIDs) <= idsPerBatchedQuery {
		for _, stop := range stops {
			stopIDs = append(stopIDs, stop.ID)
		}
	}
	require.Greater(t, len(stopIDs), idsPerBatchedQuery,
		"the input must span more than one batch for this test to mean anything")

	batched, err := api.candidateTripIDsForStops(ctx, stopIDs)
	require.NoError(t, err)

	singleQuery, err := api.GtfsManager.GtfsDB.Queries.GetTripIDsForStops(ctx, stopIDs[:len(stops)])
	require.NoError(t, err)

	assert.ElementsMatch(t, singleQuery, uniqueStrings(batched))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
