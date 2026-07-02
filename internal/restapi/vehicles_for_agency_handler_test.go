package restapi

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogtfs "github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

// vehiclesForAgencyURL builds the /vehicles-for-agency URL with key=TEST baked in.
// Extra query params are merged from optional url.Values arguments.
func vehiclesForAgencyURL(agencyID string, params ...url.Values) string {
	q := url.Values{"key": {"TEST"}}
	for _, p := range params {
		maps.Copy(q, p)
	}
	return "/api/where/vehicles-for-agency/" + agencyID + ".json?" + q.Encode()
}

// fetchRawData returns the response "data" object as raw JSON keys so tests can
// assert field presence, not just decoded zero values.
func fetchRawData(t testing.TB, api *RestAPI, endpoint string) map[string]json.RawMessage {
	t.Helper()
	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	return envelope.Data
}

func TestVehiclesForAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api,
		"/api/where/vehicles-for-agency/"+testdata.Raba.ID+".json?key=invalid")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestVehiclesForAgencyHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
	// Without injected real-time vehicles, the handler returns an empty list.
	assert.Empty(t, model.Data.List)
}

func TestVehiclesForAgencyHandlerWithNonExistentAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL("nonexistent"))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Empty(t, model.Data.List)

	data := fetchRawData(t, api, vehiclesForAgencyURL("nonexistent"))
	raw, ok := data["outOfRange"]
	require.True(t, ok, "outOfRange key must be present in the response payload")
	assert.JSONEq(t, "false", string(raw), "unknown agency must return outOfRange=false (Extension 3a)")
}

// TestVehiclesForAgencyHandler_OutOfRangeFalseForKnownAgency verifies the success
// path emits outOfRange=false for an agency served by this instance.
func TestVehiclesForAgencyHandler_OutOfRangeFalseForKnownAgency(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	data := fetchRawData(t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	raw, ok := data["outOfRange"]
	require.True(t, ok, "outOfRange key must be present in the response payload")
	assert.JSONEq(t, "false", string(raw), "known agency must return outOfRange=false")
}

// TestVehiclesForAgencyHandler_OccupancyPropagation verifies that when a vehicle
// has OccupancyStatus set, the value is propagated to both vehicleStatus.occupancyStatus
// and tripStatus.occupancyStatus. Tested here with an injected mock vehicle, since
// RABA fixtures lack occupancy data.
func TestVehiclesForAgencyHandler_OccupancyPropagation(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	occ := gtfsrt.VehiclePosition_OccupancyStatus(gtfsrt.VehiclePosition_MANY_SEATS_AVAILABLE)
	api.GtfsManager.MockAddVehicleWithOptions("v_occ_test", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		OccupancyStatus: &occ,
	})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	var vehicle *models.VehicleStatus
	for i := range model.Data.List {
		if model.Data.List[i].VehicleID == "v_occ_test" {
			vehicle = &model.Data.List[i]
			break
		}
	}
	require.NotNil(t, vehicle, "occupancy mock vehicle not returned by VehiclesForAgencyID")
	assert.Equal(t, "MANY_SEATS_AVAILABLE", vehicle.OccupancyStatus,
		"vehicleStatus.occupancyStatus must receive the GTFS-RT value")
	require.NotNil(t, vehicle.TripStatus, "tripStatus must be present when vehicle has a trip")
	assert.Equal(t, "MANY_SEATS_AVAILABLE", vehicle.TripStatus.OccupancyStatus,
		"tripStatus.occupancyStatus must receive the same GTFS-RT value")
}

// TestVehiclesForAgencyHandler_VehicleWithoutTrip verifies the invariant that vehicles
// with Trip == nil are excluded from the vehicles-for-agency response.
func TestVehiclesForAgencyHandler_VehicleWithoutTrip(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	// Inject a vehicle with Trip == nil. It shares a routeID with static data so that
	// if the nil-Trip filter is removed, the vehicle would propagate to the handler.
	const noTripVehicleID = "v_no_trip_regression"
	api.GtfsManager.MockAddVehicleWithOptions(noTripVehicleID, "", trip.RouteID, gtfs.MockVehicleOptions{
		NoTrip: true,
	})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	for _, v := range model.Data.List {
		assert.NotEqual(t, noTripVehicleID, v.VehicleID,
			"vehicle with Trip==nil must be excluded by VehiclesForAgencyID before reaching the handler")
	}
}

func TestVehiclesForAgencyHandler_VehicleWithNilID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		NoID: true,
	})

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	for _, v := range model.Data.List {
		assert.NotEqual(t, "", v.VehicleID, "vehicle with nil ID must be skipped, not returned with empty vehicleId")
	}
}

// TestVehiclesForAgencyHandler_SituationsPopulatedInReferences verifies that route-level
// alerts are reflected in references.situations for vehicles serving that route.
func TestVehiclesForAgencyHandler_SituationsPopulatedInReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	rawTripID := trip.ID
	rawRouteID := trip.RouteID

	const alertID = "alert-vehicles-test"
	// MockAddAlert must precede MockAddVehicleWithOptions: it triggers rebuildMergedRealtimeLocked,
	// which rebuilds realTimeVehicles from feedVehicles (empty), wiping any vehicle added first.
	api.GtfsManager.MockAddAlert("feed-0", gogtfs.Alert{
		ID: alertID,
		InformedEntities: []gogtfs.AlertInformedEntity{
			{RouteID: &rawRouteID},
		},
	})
	api.GtfsManager.MockAddVehicleWithOptions("v_situation_test", rawTripID, rawRouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	require.NotEmpty(t, model.Data.List, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotEmpty(t, model.Data.References.Situations, "expected at least one situation in references")
	found := false
	for _, sit := range model.Data.References.Situations {
		if sit.ID == alertID {
			found = true
			break
		}
	}
	assert.True(t, found, "expected situation with id %q in references.situations", alertID)
}

// TestVehiclesForAgencyHandler_AgencySituationsPopulatedInReferences verifies that
// agency-wide alerts are reflected in references.situations.
func TestVehiclesForAgencyHandler_AgencySituationsPopulatedInReferences(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	agencyID := testdata.Raba.ID

	const alertID = "alert-agency-wide-test"
	api.GtfsManager.MockAddAlert("feed-0", gogtfs.Alert{
		ID: alertID,
		InformedEntities: []gogtfs.AlertInformedEntity{
			{AgencyID: &agencyID},
		},
	})
	api.GtfsManager.MockAddVehicleWithOptions("v_agency_alert_test", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(agencyID))

	require.NotEmpty(t, model.Data.List, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotEmpty(t, model.Data.References.Situations, "expected agency-wide alert in references.situations")
	found := false
	for _, sit := range model.Data.References.Situations {
		if sit.ID == alertID {
			found = true
			break
		}
	}
	assert.True(t, found, "expected situation with id %q in references.situations", alertID)
}

func TestVehiclesForAgencyHandler_RouteIDUsesCombinedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_route_id_test", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	require.NotEmpty(t, model.Data.References.Trips,
		"expected at least one trip reference — mock vehicle was not returned by VehiclesForAgencyID")
	expectedRouteID := testdata.Raba.ID + "_" + trip.RouteID
	found := false
	for _, tr := range model.Data.References.Trips {
		if tr.RouteID == expectedRouteID {
			found = true
			break
		}
	}
	assert.True(t, found,
		"expected a trip reference with routeId=%q (combined agencyID_routeID format)", expectedRouteID)
}

// vehiclesForAgencyContainsID reports whether the response list contains a vehicle
// with the given ID.
func vehiclesForAgencyContainsID(list []models.VehicleStatus, vehicleID string) bool {
	for i := range list {
		if list[i].VehicleID == vehicleID {
			return true
		}
	}
	return false
}

// ageFilterClock is a fixed reference time used by the ageInSeconds tests so the
// fresh/stale vehicle timestamps are deterministic relative to api.Clock.Now().
var ageFilterClock = time.Date(2025, 6, 8, 21, 10, 0, 0, time.UTC)

// TestVehiclesForAgencyHandler_AgeInSecondsFiltersStale verifies that a positive
// ageInSeconds excludes vehicles whose last update is older than the cutoff while
// retaining fresh ones.
func TestVehiclesForAgencyHandler_AgeInSecondsFiltersStale(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(ageFilterClock))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	freshTS := ageFilterClock.Add(-30 * time.Second)
	staleTS := ageFilterClock.Add(-10 * time.Minute)
	api.GtfsManager.MockAddVehicleWithOptions("v_fresh", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		Timestamp: &freshTS,
	})
	api.GtfsManager.MockAddVehicleWithOptions("v_stale", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		Timestamp: &staleTS,
	})

	params := url.Values{"ageInSeconds": {"60"}}
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.True(t, vehiclesForAgencyContainsID(model.Data.List, "v_fresh"),
		"vehicle updated within ageInSeconds must be retained")
	assert.False(t, vehiclesForAgencyContainsID(model.Data.List, "v_stale"),
		"vehicle older than ageInSeconds must be excluded")
}

// TestVehiclesForAgencyHandler_AgeInSecondsZeroFiltersStrictly verifies that an
// explicit ageInSeconds=0 applies a strict 0-second cutoff, excluding stale vehicles.
func TestVehiclesForAgencyHandler_AgeInSecondsZeroFiltersStrictly(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(ageFilterClock))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	staleTS := ageFilterClock.Add(-10 * time.Minute)
	api.GtfsManager.MockAddVehicleWithOptions("v_stale_zero", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		Timestamp: &staleTS,
	})

	params := url.Values{"ageInSeconds": {"0"}}
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.False(t, vehiclesForAgencyContainsID(model.Data.List, "v_stale_zero"),
		"ageInSeconds=0 must apply a strict cutoff and exclude stale vehicles")
}

// TestVehiclesForAgencyHandler_AgeInSecondsNegativeNoFilter verifies that a
// negative ageInSeconds disables the staleness filter.
func TestVehiclesForAgencyHandler_AgeInSecondsNegativeNoFilter(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(ageFilterClock))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	staleTS := ageFilterClock.Add(-10 * time.Minute)
	api.GtfsManager.MockAddVehicleWithOptions("v_stale_neg", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		Timestamp: &staleTS,
	})

	params := url.Values{"ageInSeconds": {"-5"}}
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.True(t, vehiclesForAgencyContainsID(model.Data.List, "v_stale_neg"),
		"negative ageInSeconds must disable the filter and return all vehicles")
}

// TestVehiclesForAgencyHandler_AgeInSecondsAbsentNoFilter verifies that omitting
// ageInSeconds returns all vehicles regardless of staleness.
func TestVehiclesForAgencyHandler_AgeInSecondsAbsentNoFilter(t *testing.T) {
	api := createTestApiWithClock(t, clock.NewMockClock(ageFilterClock))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	staleTS := ageFilterClock.Add(-10 * time.Minute)
	api.GtfsManager.MockAddVehicleWithOptions("v_stale_absent", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		Timestamp: &staleTS,
	})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	assert.True(t, vehiclesForAgencyContainsID(model.Data.List, "v_stale_absent"),
		"absent ageInSeconds must return all vehicles regardless of age")
}

// TestVehiclesForAgencyHandler_IncludeReferencesFalse verifies that
// includeReferences=false empties the references block while keeping the list.
func TestVehiclesForAgencyHandler_IncludeReferencesFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_refs_false", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	params := url.Values{"includeReferences": {"false"}}
	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, model.Data.List, "list must still be populated when includeReferences=false")

	refs := model.Data.References
	assert.Empty(t, refs.Agencies, "agencies should be empty when includeReferences=false")
	assert.Empty(t, refs.Routes, "routes should be empty when includeReferences=false")
	assert.Empty(t, refs.Trips, "trips should be empty when includeReferences=false")
	assert.Empty(t, refs.Stops, "stops should be empty when includeReferences=false")
	assert.Empty(t, refs.Situations, "situations should be empty when includeReferences=false")
}

// TestVehiclesForAgencyHandler_IncludeReferencesDefault verifies that references
// are populated when includeReferences is absent or explicitly true.
func TestVehiclesForAgencyHandler_IncludeReferencesDefault(t *testing.T) {
	tests := []struct {
		name   string
		params []url.Values
	}{
		{"absent", nil},
		{"explicit true", []url.Values{{"includeReferences": {"true"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := createTestApi(t)
			defer api.Shutdown()
			t.Cleanup(api.GtfsManager.MockResetRealTimeData)

			trip := mustGetTrip(t, api)
			api.GtfsManager.MockAddVehicleWithOptions("v_refs_default", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

			_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, tc.params...))

			assert.NotEmpty(t, model.Data.References.Agencies,
				"agencies should be populated when includeReferences is true/absent")
		})
	}
}

// vehiclesRealTimeDataClock is pinned just past the latest timestamp in
// testdata/raba-vehicle-positions.pb (2025-06-08 21:08:26 UTC) so vehicles fall
// inside the handler's 15-minute stale-vehicle window. With clock.RealClock{},
// the .pb data is hours/days stale and every vehicle is filtered out — defeating
// the point of the test.
var vehiclesRealTimeDataClock = time.Date(2025, 6, 8, 21, 10, 0, 0, time.UTC)

// TestVehiclesForAgencyHandlerWithRealTimeData verifies that .pb file loading
// integrates with the handler end-to-end: vehicles parse, get filtered by the
// stale-vehicle window, and pass the handler's per-vehicle validation.
func TestVehiclesForAgencyHandlerWithRealTimeData(t *testing.T) {
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(vehiclesRealTimeDataClock))
	defer cleanup()

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.ElementsMatch(t, []models.AgencyReference{testdata.Raba}, model.Data.References.Agencies)
	require.NotEmpty(t, model.Data.List, "expected real-time vehicles when clock is inside the .pb fixture window")

	validStatuses := []string{"INCOMING_AT", "STOPPED_AT", "IN_TRANSIT_TO", "SCHEDULED", ""}
	validPhases := []string{"approaching", "stopped", "in_progress", "scheduled", ""}
	for i, vehicle := range model.Data.List {
		assert.NotEmpty(t, vehicle.VehicleID, "list[%d].vehicleId", i)
		assert.Contains(t, validStatuses, vehicle.Status, "list[%d].status", i)
		assert.Contains(t, validPhases, vehicle.Phase, "list[%d].phase", i)
		if vehicle.TripStatus != nil {
			assert.NotEmpty(t, vehicle.TripID, "list[%d].tripId should be non-empty when tripStatus is present", i)
			assert.NotEmpty(t, vehicle.TripStatus.ActiveTripID, "list[%d].tripStatus.activeTripId", i)
			assert.GreaterOrEqual(t, vehicle.TripStatus.Orientation, float64(0), "list[%d].tripStatus.orientation >= 0", i)
			assert.LessOrEqual(t, vehicle.TripStatus.Orientation, float64(360), "list[%d].tripStatus.orientation <= 360", i)
		}
	}
}

// createTestApiWithRealTimeData creates a test API with real-time GTFS-RT data served
// from local .pb files.
func createTestApiWithRealTimeData(t testing.TB, c clock.Clock) (*RestAPI, func()) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		if err != nil {
			t.Logf("Failed to read vehicle positions file: %v", err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, err = w.Write(data)
		require.NoError(t, err)
	})
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
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
			RateLimit: 100,
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         gtfsManager,
		DirectionCalculator: dirCalc,
		Clock:               c,
	}

	api := NewRestAPI(application)

	cleanup := func() {
		api.Shutdown()
		server.Close()
		gtfsManager.Shutdown()
	}
	return api, cleanup
}
