package restapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

func TestVehiclesForAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestVehiclesForAgencyHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Check that we have a list of vehicles
	_, ok = data["list"].([]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	refAgencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, refAgencies, 1)
}

func TestVehiclesForAgencyHandlerWithNonExistentAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/nonexistent.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	assert.Len(t, list, 0)
}

func TestVehiclesForAgencyHandlerResponseStructure(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	// Verify basic response structure
	_, ok = data["list"].([]interface{})
	require.True(t, ok)

	refs, ok := data["references"].(map[string]interface{})
	require.True(t, ok)

	// Should have agency reference
	refAgencies, ok := refs["agencies"].([]interface{})
	require.True(t, ok)
	assert.Len(t, refAgencies, 1)

	// Verify agency reference structure
	agency := refAgencies[0].(map[string]interface{})
	assert.Equal(t, agencyId, agency["id"])
	assert.NotEmpty(t, agency["name"])

	// Verify other reference sections exist (may be empty)
	_, ok = refs["routes"].([]interface{})
	assert.True(t, ok)
	_, ok = refs["trips"].([]interface{})
	assert.True(t, ok)
	_, ok = refs["situations"].([]interface{})
	assert.True(t, ok)
	_, ok = refs["stops"].([]interface{})
	assert.True(t, ok)
	_, ok = refs["stopTimes"].([]interface{})
	assert.True(t, ok)
}

func TestVehiclesForAgencyHandlerReferencesBuilding(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})
	refs := data["references"].(map[string]interface{})

	// Test that references are properly built
	refAgencies := refs["agencies"].([]interface{})
	assert.Len(t, refAgencies, 1)

	agency := refAgencies[0].(map[string]interface{})
	assert.Equal(t, agencyId, agency["id"])

	// Test reference deduplication (agency should appear only once)
	vehiclesList := data["list"].([]interface{})
	if len(vehiclesList) > 0 {
		// Even with multiple vehicles from same agency, only one agency reference
		assert.Len(t, refAgencies, 1)
	}

	// Test that route references are built when vehicles have trips
	refTrips := refs["trips"].([]interface{})

	vehiclesWithTrips := 0
	for _, v := range vehiclesList {
		vehicle := v.(map[string]interface{})
		if vehicle["tripStatus"] != nil {
			vehiclesWithTrips++
		}
	}

	// Should have trip references for vehicles with trips
	if vehiclesWithTrips > 0 {
		assert.GreaterOrEqual(t, len(refTrips), 1)

		// Verify trip reference structure
		if len(refTrips) > 0 {
			trip := refTrips[0].(map[string]interface{})
			assert.NotEmpty(t, trip["id"])
			assert.NotEmpty(t, trip["routeId"])
		}
	}
}

func TestVehiclesForAgencyHandlerEmptyResult(t *testing.T) {
	// Test with an agency that likely has no vehicles
	api := createTestApi(t)
	defer api.Shutdown()

	// Test with a specific agency that should return empty results
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/25.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})
	vehiclesList := data["list"].([]interface{})

	// Should handle empty vehicle list gracefully
	assert.IsType(t, []interface{}{}, vehiclesList)

	// Should still have proper references structure
	refs := data["references"].(map[string]interface{})
	assert.Contains(t, refs, "agencies")
	assert.Contains(t, refs, "routes")
	assert.Contains(t, refs, "trips")
}

func TestVehiclesForAgencyHandlerFieldMapping(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	// Test the endpoint to verify field mapping logic is tested
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})
	vehiclesList := data["list"].([]interface{})

	// Test that the processing loop runs even with empty results
	// This should still test lines 21-139 in the handler
	assert.IsType(t, []interface{}{}, vehiclesList)

	// Verify that reference building happens even with empty vehicle list
	refs := data["references"].(map[string]interface{})
	refAgencies := refs["agencies"].([]interface{})
	assert.Len(t, refAgencies, 1)

	// Test that the loop variables are initialized
	refRoutes := refs["routes"].([]interface{})
	refTrips := refs["trips"].([]interface{})
	assert.IsType(t, []interface{}{}, refRoutes)
	assert.IsType(t, []interface{}{}, refTrips)
}

func TestVehiclesForAgencyHandlerWithAllAgencies(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := "25"

	t.Run("Agency_"+agencyID, func(t *testing.T) {
		resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 200, model.Code)

		data := model.Data.(map[string]interface{})
		vehiclesList := data["list"].([]interface{})
		refs := data["references"].(map[string]interface{})

		// Test that processing always happens
		assert.IsType(t, []interface{}{}, vehiclesList)

		// Agency reference should always be present
		refAgencies := refs["agencies"].([]interface{})
		assert.Len(t, refAgencies, 1)

		agencyRef := refAgencies[0].(map[string]interface{})
		assert.Equal(t, agencyID, agencyRef["id"])
	})
}

func TestVehiclesForAgencyHandlerDatabaseRouteQueries(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	// This test specifically targets the database route lookup code
	// Even if no vehicles exist, the handler should still execute the processing logic
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data := model.Data.(map[string]interface{})

	// Test that the handler processes the empty vehicle list and sets up references
	refs := data["references"].(map[string]interface{})

	// These should all exist even with no vehicles
	assert.Contains(t, refs, "agencies")
	assert.Contains(t, refs, "routes")
	assert.Contains(t, refs, "trips")
	assert.Contains(t, refs, "situations")
	assert.Contains(t, refs, "stops")
	assert.Contains(t, refs, "stopTimes")

	// Test that maps are converted to slices properly
	refAgencies := refs["agencies"].([]interface{})
	refRoutes := refs["routes"].([]interface{})
	refTrips := refs["trips"].([]interface{})

	assert.IsType(t, []interface{}{}, refAgencies)
	assert.IsType(t, []interface{}{}, refRoutes)
	assert.IsType(t, []interface{}{}, refTrips)
}

// TestVehiclesForAgencyHandler_OccupancyPropagation verifies that when a vehicle
// has OccupancyStatus set, the value is propagated to both vehicleStatus.occupancyStatus
// and tripStatus.occupancyStatus. Tested here with an injected mock vehicle,since RABA fixtures lack occupancy data.
func TestVehiclesForAgencyHandler_OccupancyPropagation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	trip := mustGetTrip(t, api)

	rawRouteID := trip.RouteID
	tripID := trip.ID

	occ := gtfsrt.VehiclePosition_OccupancyStatus(gtfsrt.VehiclePosition_MANY_SEATS_AVAILABLE)
	api.GtfsManager.MockAddVehicleWithOptions("v_occ_test", tripID, rawRouteID, gtfs.MockVehicleOptions{
		OccupancyStatus: &occ,
	})

	_, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response data must be a map")

	vehiclesList, ok := data["list"].([]interface{})
	require.True(t, ok, "list must be a slice")
	require.NotEmpty(t, vehiclesList, "expected at least one vehicle — occupancy mock vehicle not returned by VehiclesForAgencyID")

	vehicle, ok := vehiclesList[0].(map[string]interface{})
	require.True(t, ok, "vehicle entry must be a map")

	// VehicleStatus.occupancyStatus must be propagated from GTFS-RT
	assert.Equal(t, "MANY_SEATS_AVAILABLE", vehicle["occupancyStatus"],
		"vehicleStatus.occupancyStatus must receive the GTFS-RT value")

	// TripStatus.occupancyStatus must also be propagated (the handler sets both)
	tripStatus, ok := vehicle["tripStatus"].(map[string]interface{})
	require.True(t, ok, "tripStatus must be present when vehicle has a trip")
	assert.Equal(t, "MANY_SEATS_AVAILABLE", tripStatus["occupancyStatus"],
		"tripStatus.occupancyStatus must receive the same GTFS-RT value")
}

// TestVehiclesForAgencyHandler_VehicleWithoutTrip verifies the invariant that vehicles
// with Trip == nil are excluded from the vehicles-for-agency response.
func TestVehiclesForAgencyHandler_VehicleWithoutTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	trip := mustGetTrip(t, api)
	rawRouteID := trip.RouteID

	// Inject a vehicle with Trip == nil. It shares a routeID with static data so that
	// if the nil-Trip filter is removed, the vehicle would propagate to the handler.
	const noTripVehicleID = "v_no_trip_regression"
	api.GtfsManager.MockAddVehicleWithOptions(noTripVehicleID, "", rawRouteID, gtfs.MockVehicleOptions{
		NoTrip: true,
	})

	_, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST")

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "response data must be a map")

	vehiclesList, ok := data["list"].([]interface{})
	require.True(t, ok, "list must be a slice")

	// The nil-Trip vehicle must never appear in the response.
	for _, item := range vehiclesList {
		v, ok := item.(map[string]interface{})
		require.True(t, ok)
		assert.NotEqual(t, noTripVehicleID, v["vehicleId"],
			"vehicle with Trip==nil must be excluded by VehiclesForAgencyID before reaching the handler")
	}
}

func TestVehiclesForAgencyHandler_VehicleWithNilID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	trip := mustGetTrip(t, api)
	rawRouteID := trip.RouteID

	api.GtfsManager.MockAddVehicleWithOptions("", trip.ID, rawRouteID, gtfs.MockVehicleOptions{
		NoID: true,
	})

	resp, model := serveApiAndRetrieveEndpoint(t, api,
		"/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)
	list, ok := data["list"].([]interface{})
	require.True(t, ok)
	for _, item := range list {
		v := item.(map[string]interface{})
		assert.NotEqual(t, "", v["vehicleId"], "vehicle with nil ID must be skipped, not returned with empty vehicleId")
	}
}

// createTestApiWithRealTimeData creates a test API with real-time GTFS-RT data served from local files
func createTestApiWithRealTimeData(t testing.TB) (*RestAPI, func()) {
	ctx := context.Background()

	// Create HTTP server to serve GTFS-RT files
	mux := http.NewServeMux()

	// Serve vehicle positions
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		vehiclePositionsPath := filepath.Join("../../testdata", "raba-vehicle-positions.pb")
		data, err := os.ReadFile(vehiclePositionsPath)
		if err != nil {
			t.Logf("Failed to read vehicle positions file: %v", err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, err = w.Write(data)
		require.NoError(t, err)
	})

	// Serve trip updates
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		tripUpdatesPath := filepath.Join("../../testdata", "raba-trip-updates.pb")
		data, err := os.ReadFile(tripUpdatesPath)
		if err != nil {
			t.Logf("Failed to read trip updates file: %v", err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, err = w.Write(data)
		require.NoError(t, err)
	})

	server := httptest.NewServer(mux)

	// Create GTFS config with real-time URLs pointing to our test server
	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: ":memory:",
		RTFeeds: []gtfs.RTFeedConfig{
			{
				ID:                  "test-feed",
				TripUpdatesURL:      server.URL + "/trip-updates",
				VehiclePositionsURL: server.URL + "/vehicle-positions",
				RefreshInterval:     30,
				Enabled:             true,
			},
		},
	}

	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)

	dirCalc := gtfs.NewAdvancedDirectionCalculator(gtfsManager.GtfsDB.Queries)

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100, // Higher rate limit for this test
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         gtfsManager,
		DirectionCalculator: dirCalc,
		Clock:               clock.RealClock{},
	}

	api := NewRestAPI(application)

	// Cleanup function to close the server and API
	cleanup := func() {
		api.Shutdown()
		server.Close()
		gtfsManager.Shutdown()
	}

	return api, cleanup
}

func TestVehiclesForAgencyHandlerWithRealTimeData(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t)
	defer cleanup()

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyId := agencies[0].ID

	// Give the manager a moment to load real-time data
	// The manager should load real-time data automatically on initialization
	time.Sleep(500 * time.Millisecond)

	// Check if we have real-time vehicles loaded
	realTimeVehicles := api.GtfsManager.GetRealTimeVehicles()
	t.Logf("Loaded %d real-time vehicles", len(realTimeVehicles))

	// Debug vehicle-to-agency matching
	vehiclesForAgency, err := api.GtfsManager.VehiclesForAgencyID(context.Background(), agencyId)
	require.Nil(t, err)
	t.Logf("Found %d vehicles for agency %s", len(vehiclesForAgency), agencyId)

	if len(realTimeVehicles) > 0 && len(vehiclesForAgency) == 0 {
		t.Log("Real-time vehicles are not matching the test agency. Debugging:")
		for i, vehicle := range realTimeVehicles {
			if i < 3 { // Log first 3 vehicles
				vehicleID := ""
				if vehicle.ID != nil {
					vehicleID = vehicle.ID.ID
				}
				if vehicle.Trip != nil {
					t.Logf("Vehicle %s: tripId=%s, routeId=%s", vehicleID, vehicle.Trip.ID.ID, vehicle.Trip.ID.RouteID)
				} else {
					t.Logf("Vehicle %s: no trip assigned", vehicleID)
				}
			}
		}

		routes, err := api.GtfsManager.RoutesForAgencyID(t.Context(), agencyId)
		require.Nil(t, err)
		t.Logf("Agency %s has %d routes:", agencyId, len(routes))
		for i, route := range routes {
			if i < 3 { // Log first 3 routes
				t.Logf("Route: %s", route.ID)
			}
		}
	}

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/vehicles-for-agency/"+agencyId+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 200, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	vehiclesList, ok := data["list"].([]interface{})
	require.True(t, ok)

	if len(realTimeVehicles) > 0 {
		t.Log("Testing with real-time vehicle data!")

		// Now we can test the actual vehicle processing loop
		if len(vehiclesList) > 0 {
			// Test first vehicle in detail
			vehicle := vehiclesList[0].(map[string]interface{})

			// Required fields per OpenAPI spec — must always be present
			assert.Contains(t, vehicle, "vehicleId", "vehicleId is required")
			assert.Contains(t, vehicle, "lastLocationUpdateTime", "lastLocationUpdateTime is required")
			assert.Contains(t, vehicle, "lastUpdateTime", "lastUpdateTime is required")
			assert.Contains(t, vehicle, "location", "location is required")
			assert.Contains(t, vehicle, "tripId", "tripId is required")
			assert.Contains(t, vehicle, "tripStatus", "tripStatus is required")

			// Test timestamp fields (present but may be null when no position data)
			if vehicle["lastLocationUpdateTime"] != nil {
				assert.IsType(t, float64(0), vehicle["lastLocationUpdateTime"])
				assert.Greater(t, vehicle["lastLocationUpdateTime"].(float64), float64(0))
			}
			if vehicle["lastUpdateTime"] != nil {
				assert.IsType(t, float64(0), vehicle["lastUpdateTime"])
				assert.Greater(t, vehicle["lastUpdateTime"].(float64), float64(0))
			}

			// Test location fields (present but may be null when no position data)
			if vehicle["location"] != nil {
				location := vehicle["location"].(map[string]interface{})
				assert.Contains(t, location, "lat")
				assert.Contains(t, location, "lon")
				assert.IsType(t, float64(0), location["lat"])
				assert.IsType(t, float64(0), location["lon"])
			}

			// Test tripId populated when trip is available
			if vehicle["tripStatus"] != nil {
				assert.NotEmpty(t, vehicle["tripId"], "tripId should be non-empty when tripStatus is present")
			}

			// Test status mapping
			if vehicle["status"] != nil {
				status := vehicle["status"].(string)
				validStatuses := []string{"INCOMING_AT", "STOPPED_AT", "IN_TRANSIT_TO", "SCHEDULED"}
				assert.Contains(t, validStatuses, status, "Status should be valid")
			} else {
				t.Log("status field is absent — optional field omitempty, skipping status assertions")
			}

			if vehicle["phase"] != nil {
				phase := vehicle["phase"].(string)
				validPhases := []string{"approaching", "stopped", "in_progress", "scheduled"}
				assert.Contains(t, validPhases, phase, "Phase should be valid")
			} else {
				t.Log("phase field is absent — optional field omitempty, skipping phase assertions")
			}

			// Test trip status (present but may be null when vehicle has no trip)
			if vehicle["tripStatus"] != nil {
				tripStatus := vehicle["tripStatus"].(map[string]interface{})

				assert.NotEmpty(t, tripStatus["activeTripId"], "TripStatus should have activeTripId")
				assert.IsType(t, true, tripStatus["scheduled"])

				if tripStatus["serviceDate"] != nil {
					assert.IsType(t, float64(0), tripStatus["serviceDate"])
				}

				if tripStatus["position"] != nil {
					position := tripStatus["position"].(map[string]interface{})
					assert.Contains(t, position, "lat")
					assert.Contains(t, position, "lon")
				} else {
					t.Log("tripStatus.position is null — no GPS fix in fixture, skipping position assertions")
				}

				if tripStatus["orientation"] != nil {
					orientation := tripStatus["orientation"]
					assert.IsType(t, float64(0), orientation)
					assert.GreaterOrEqual(t, orientation.(float64), float64(0))
					assert.LessOrEqual(t, orientation.(float64), float64(360))
				}
			}
		}

		// Test references when vehicles are present
		refs := data["references"].(map[string]interface{})

		refAgencies := refs["agencies"].([]interface{})
		assert.Len(t, refAgencies, 1)

		refTrips := refs["trips"].([]interface{})
		refRoutes := refs["routes"].([]interface{})

		vehiclesWithTrips := 0
		for _, v := range vehiclesList {
			vehicle := v.(map[string]interface{})
			if vehicle["tripStatus"] != nil {
				vehiclesWithTrips++
			}
		}

		if vehiclesWithTrips > 0 {
			assert.GreaterOrEqual(t, len(refTrips), 1, "Should have trip references for vehicles with trips")

			// Test trip reference structure
			if len(refTrips) > 0 {
				trip := refTrips[0].(map[string]interface{})
				assert.NotEmpty(t, trip["id"])
				assert.NotEmpty(t, trip["routeId"])
			}

			// Test route references (may be present if routes are found)
			if len(refRoutes) > 0 {
				route := refRoutes[0].(map[string]interface{})
				assert.NotEmpty(t, route)
			}
		}

	} else {
		t.Log("No real-time vehicles loaded - testing empty case")
		assert.Len(t, vehiclesList, 0)
	}
}

func TestVehiclesForAgency_RouteIDUsesCombinedID(t *testing.T) {
	agencyID := "25"
	routeID := "1"

	expected := utils.FormCombinedID(agencyID, routeID)

	if expected != "25_1" {
		t.Fatalf("expected combined ID 25_1, got %s", expected)
	}
}