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
	"strconv"
	"testing"
	"time"

	gogtfs "github.com/OneBusAway/go-gtfs"
	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
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
	require.NotNil(t, api.GtfsManager, "api.GtfsManager should not be nil!")

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

// findVehicleStatusByID returns the entry with the given vehicleId, or nil.
func findVehicleStatusByID(list []models.VehicleStatus, vehicleID string) *models.VehicleStatus {
	for i := range list {
		if list[i].VehicleID == vehicleID {
			return &list[i]
		}
	}
	return nil
}

// TestVehiclesForAgencyHandler_LimitExceededAlwaysFalse verifies the endpoint
// returns all vehicles with limitExceeded=false (no result cap).
func TestVehiclesForAgencyHandler_LimitExceededAlwaysFalse(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_le_1", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})
	api.GtfsManager.MockAddVehicleWithOptions("v_le_2", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})
	api.GtfsManager.MockAddVehicleWithOptions("v_le_3", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	assert.False(t, model.Data.LimitExceeded, "limitExceeded must always be false")
	assert.Len(t, model.Data.List, 3, "all matching vehicles must be returned")
}

// TestVehiclesForAgencyHandler_IgnoresMaxCountAndOffset verifies that maxCount and
// offset do not truncate the result; all vehicles are returned.
func TestVehiclesForAgencyHandler_IgnoresMaxCountAndOffset(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_pg_1", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})
	api.GtfsManager.MockAddVehicleWithOptions("v_pg_2", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})
	api.GtfsManager.MockAddVehicleWithOptions("v_pg_3", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	params := url.Values{"maxCount": {"1"}, "offset": {"1"}}
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.False(t, model.Data.LimitExceeded, "limitExceeded must remain false")
	assert.Len(t, model.Data.List, 3, "maxCount/offset must not truncate the result")
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

// fetchVehiclesForAgencyRawList returns the data.list entries as raw JSON maps so
// tests can assert field presence, not just decoded zero values.
func fetchVehiclesForAgencyRawList(t testing.TB, api *RestAPI, endpoint string) []map[string]json.RawMessage {
	t.Helper()
	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	resp, err := http.Get(server.URL + endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		Data struct {
			List []map[string]json.RawMessage `json:"list"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&envelope))
	return envelope.Data.List
}

// rawEntryByVehicleID returns the raw entry whose vehicleId matches, or nil.
func rawEntryByVehicleID(t testing.TB, list []map[string]json.RawMessage, vehicleID string) map[string]json.RawMessage {
	t.Helper()
	for _, entry := range list {
		if string(entry["vehicleId"]) == `"`+vehicleID+`"` {
			return entry
		}
	}
	return nil
}

// findVehicleInList returns the entry with the given vehicleId, or nil.
func findVehicleInList(list []models.VehicleStatus, vehicleID string) *models.VehicleStatus {
	for i := range list {
		if list[i].VehicleID == vehicleID {
			return &list[i]
		}
	}
	return nil
}

// findTripWithBlock returns the first trip with a non-empty BlockID satisfying
// pred, searching up to 200 trips. Returns the zero value if none match.
func findTripWithBlock(t testing.TB, api *RestAPI, ctx context.Context, pred func(gtfsdb.Trip) bool) gtfsdb.Trip {
	t.Helper()
	trips, err := api.GtfsManager.GetTrips(ctx, 200)
	require.NoError(t, err)

	for _, tr := range trips {
		if !tr.BlockID.Valid || tr.BlockID.String == "" {
			continue
		}
		if pred(tr) {
			return tr
		}
	}
	return gtfsdb.Trip{}
}

// TestVehiclesForAgencyHandler_BlockTripSequenceResolved verifies that a vehicle on
// a trip with a block active on the reference date gets a resolved (>= 0) sequence.
func TestVehiclesForAgencyHandler_BlockTripSequenceResolved(t *testing.T) {
	// Monday within the RABA dataset's active service period.
	serviceDate := time.Date(2024, 11, 4, 12, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(serviceDate))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	ctx := context.Background()
	blockTrip := findTripWithBlock(t, api, ctx, func(row gtfsdb.Trip) bool {
		_, ok := api.blockTripSequence(ctx, row.ID, serviceDate)
		return ok
	})
	require.NotEmpty(t, blockTrip.ID, "need a trip with a resolvable block sequence in test data")

	const vehicleID = "v_block_seq"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, blockTrip.ID, blockTrip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	entry := findVehicleStatusByID(model.Data.List, vehicleID)
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, entry.TripStatus)
	assert.GreaterOrEqual(t, entry.TripStatus.BlockTripSequence, 0,
		"blockTripSequence must be a resolved zero-based index")
}

// TestVehiclesForAgencyHandler_BlockTripSequenceUnavailable verifies that a vehicle
// whose trip has no resolvable block sequence gets blockTripSequence = -1.
func TestVehiclesForAgencyHandler_BlockTripSequenceUnavailable(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	// A synthetic trip ID that does not exist in the DB has no block.
	const vehicleID = "v_block_seq_none"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, "nonexistent-trip", trip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	entry := findVehicleStatusByID(model.Data.List, vehicleID)
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, entry.TripStatus)
	assert.Equal(t, -1, entry.TripStatus.BlockTripSequence,
		"blockTripSequence must be -1 when the sequence is unavailable")
}

// TestVehiclesForAgencyHandler_BlockTripSequenceUsesRequestedTime verifies that
// blockTripSequence is resolved against the request's `time` parameter rather
// than the server's wall-clock "now".
func TestVehiclesForAgencyHandler_BlockTripSequenceUsesRequestedTime(t *testing.T) {
	// "Now" is set far outside the RABA feed's service calendar (2024-2025), so no
	// trip's block sequence can resolve against api.Clock.Now().
	farFutureNow := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	api := createTestApiWithClock(t, clock.NewMockClock(farFutureNow))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	// The requested reference time, which does fall within the active service
	// window and has a resolvable block sequence.
	requestedTime := time.Date(2024, 11, 4, 12, 0, 0, 0, time.UTC)

	ctx := context.Background()
	blockTrip := findTripWithBlock(t, api, ctx, func(row gtfsdb.Trip) bool {
		_, ok := api.blockTripSequence(ctx, row.ID, requestedTime)
		return ok
	})
	require.NotEmpty(t, blockTrip.ID, "need a trip with a block sequence resolvable on requestedTime in test data")

	const vehicleID = "v_block_seq_reftime"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, blockTrip.ID, blockTrip.RouteID, gtfs.MockVehicleOptions{})

	params := url.Values{"time": {strconv.FormatInt(requestedTime.UnixMilli(), 10)}}
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))
	entry := findVehicleStatusByID(model.Data.List, vehicleID)
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, entry.TripStatus)
	assert.GreaterOrEqual(t, entry.TripStatus.BlockTripSequence, 0,
		"blockTripSequence must resolve using the requested `time` parameter, not api.Clock.Now()")
}

// TestVehiclesForAgencyHandler_BlockTripSequenceUsesAgencyLocalDate verifies that,
// when no `time` parameter is supplied, blockTripSequence resolves against the
// agency's local calendar date rather than the server clock's own timezone.
func TestVehiclesForAgencyHandler_BlockTripSequenceUsesAgencyLocalDate(t *testing.T) {
	// 2024-11-09 04:00 UTC is Saturday in UTC, but still Friday 20:00 in RABA's
	// agency timezone (America/Los_Angeles, UTC-8 in November).
	mockNow := time.Date(2024, 11, 9, 4, 0, 0, 0, time.UTC)
	agencyLocalFriday := time.Date(2024, 11, 8, 12, 0, 0, 0, time.UTC)

	api := createTestApiWithClock(t, clock.NewMockClock(mockNow))
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	ctx := context.Background()
	blockTrip := findTripWithBlock(t, api, ctx, func(row gtfsdb.Trip) bool {
		_, resolvesFriday := api.blockTripSequence(ctx, row.ID, agencyLocalFriday)
		_, resolvesSaturday := api.blockTripSequence(ctx, row.ID, mockNow)
		return resolvesFriday && !resolvesSaturday
	})
	require.NotEmpty(t, blockTrip.ID,
		"need a trip whose block resolves Friday but not Saturday in test data")

	const vehicleID = "v_block_seq_tz"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, blockTrip.ID, blockTrip.RouteID, gtfs.MockVehicleOptions{})

	// No `time` param: the handler must localize "now" to the agency's timezone
	// (still Friday locally), not use the un-localized UTC instant (already
	// Saturday).
	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	entry := findVehicleStatusByID(model.Data.List, vehicleID)
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, entry.TripStatus)
	assert.GreaterOrEqual(t, entry.TripStatus.BlockTripSequence, 0,
		"blockTripSequence must resolve against the agency's local calendar date, not the server clock's UTC date")
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

// TestVehiclesForAgencyHandler_UpdateTimesZeroWhenNoUpdate verifies that
// lastUpdateTime / lastLocationUpdateTime are emitted as 0 when the vehicle has
// no update time, on both the outer entry and tripStatus.
func TestVehiclesForAgencyHandler_UpdateTimesZeroWhenNoUpdate(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_no_ts", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{
		NoTimestamp: true,
	})

	list := fetchVehiclesForAgencyRawList(t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	entry := rawEntryByVehicleID(t, list, "v_no_ts")
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")

	assert.Equal(t, "0", string(entry["lastUpdateTime"]), "outer lastUpdateTime must be 0 when no update")
	assert.Equal(t, "0", string(entry["lastLocationUpdateTime"]), "outer lastLocationUpdateTime must be 0 when no update")

	var tripStatus map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(entry["tripStatus"], &tripStatus))
	assert.Equal(t, "0", string(tripStatus["lastUpdateTime"]), "tripStatus.lastUpdateTime must be 0 when no update")
	assert.Equal(t, "0", string(tripStatus["lastLocationUpdateTime"]), "tripStatus.lastLocationUpdateTime must be 0 when no update")
}

// TestVehiclesForAgencyHandler_UpdateTimesPresentWhenSet verifies that
// lastUpdateTime / lastLocationUpdateTime are present (Unix ms) when the vehicle
// has an update time, on both the outer entry and tripStatus.
func TestVehiclesForAgencyHandler_UpdateTimesPresentWhenSet(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	api.GtfsManager.MockAddVehicleWithOptions("v_with_ts", trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	list := fetchVehiclesForAgencyRawList(t, api, vehiclesForAgencyURL(testdata.Raba.ID))
	entry := rawEntryByVehicleID(t, list, "v_with_ts")
	require.NotNil(t, entry, "mock vehicle not returned by VehiclesForAgencyID")

	updateRaw, hasUpdate := entry["lastUpdateTime"]
	locRaw, hasLocUpdate := entry["lastLocationUpdateTime"]
	require.True(t, hasUpdate, "outer lastUpdateTime must be present when set")
	require.True(t, hasLocUpdate, "outer lastLocationUpdateTime must be present when set")
	assert.NotEqual(t, "0", string(updateRaw), "lastUpdateTime must be a real timestamp, not 0")
	assert.NotEqual(t, "0", string(locRaw), "lastLocationUpdateTime must be a real timestamp, not 0")

	var tripStatus map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(entry["tripStatus"], &tripStatus))
	tsUpdateRaw, hasTSUpdate := tripStatus["lastUpdateTime"]
	tsLocRaw, hasTSLocUpdate := tripStatus["lastLocationUpdateTime"]
	require.True(t, hasTSUpdate, "tripStatus.lastUpdateTime must be present when set")
	require.True(t, hasTSLocUpdate, "tripStatus.lastLocationUpdateTime must be present when set")
	assert.NotEqual(t, "0", string(tsUpdateRaw), "tripStatus.lastUpdateTime must be a real timestamp, not 0")
	assert.NotEqual(t, "0", string(tsLocRaw), "tripStatus.lastLocationUpdateTime must be a real timestamp, not 0")
}

// TestVehiclesForAgencyHandler_TimeParameterEpochMs verifies the `time` parameter sets the reference time,
// asserting against tripStatus.serviceDate as it deterministically reflects this time.
func TestVehiclesForAgencyHandler_TimeParameterEpochMs(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	const vehicleID = "v_time_epoch_test"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	refTime := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	params := url.Values{"time": {strconv.FormatInt(refTime.UnixMilli(), 10)}}

	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	vehicle := findVehicleInList(model.Data.List, vehicleID)
	require.NotNil(t, vehicle, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, vehicle.TripStatus, "tripStatus must be present when vehicle has a trip")

	loc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	ref := refTime.In(loc)
	expectedMidnight := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, loc)
	assert.Equal(t, expectedMidnight.UnixMilli(), vehicle.TripStatus.ServiceDate.UnixMilli(),
		"tripStatus.serviceDate must be midnight of the supplied time in the agency timezone")
}

// TestVehiclesForAgencyHandler_TimeParameterAbsentUsesClock verifies that when no
// `time` parameter is supplied, the server's clock is used as the reference time.
func TestVehiclesForAgencyHandler_TimeParameterAbsentUsesClock(t *testing.T) {
	mockTime := time.Date(2025, 6, 8, 21, 10, 0, 0, time.UTC)
	api, cleanup := createTestApiWithRealTimeData(t, clock.NewMockClock(mockTime))
	defer cleanup()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	const vehicleID = "v_time_absent_test"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID))

	vehicle := findVehicleInList(model.Data.List, vehicleID)
	require.NotNil(t, vehicle, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, vehicle.TripStatus, "tripStatus must be present when vehicle has a trip")

	loc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	ref := mockTime.In(loc)
	expectedMidnight := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, loc)
	assert.Equal(t, expectedMidnight.UnixMilli(), vehicle.TripStatus.ServiceDate.UnixMilli(),
		"tripStatus.serviceDate must be midnight of the server clock in the agency timezone when time is absent")
}

// TestVehiclesForAgencyHandler_TimeParameterInvalid verifies that an unparseable
// `time` parameter yields an HTTP 400 validation error.
func TestVehiclesForAgencyHandler_TimeParameterInvalid(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	params := url.Values{"time": {"notatime"}}
	resp, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

// TestVehiclesForAgencyHandler_ServiceDateIsMidnight verifies tripStatus.serviceDate
// is midnight of the reference date in the agency timezone, not the reference time.
func TestVehiclesForAgencyHandler_ServiceDateIsMidnight(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()
	t.Cleanup(api.GtfsManager.MockResetRealTimeData)

	trip := mustGetTrip(t, api)
	const vehicleID = "v_service_date_midnight"
	api.GtfsManager.MockAddVehicleWithOptions(vehicleID, trip.ID, trip.RouteID, gtfs.MockVehicleOptions{})

	refTime := time.Date(2024, 3, 15, 14, 37, 42, 0, time.UTC)
	params := url.Values{"time": {strconv.FormatInt(refTime.UnixMilli(), 10)}}

	_, model := callAPIHandler[VehiclesForAgencyResponse](t, api, vehiclesForAgencyURL(testdata.Raba.ID, params))
	vehicle := findVehicleInList(model.Data.List, vehicleID)
	require.NotNil(t, vehicle, "mock vehicle not returned by VehiclesForAgencyID")
	require.NotNil(t, vehicle.TripStatus, "tripStatus must be present when vehicle has a trip")

	loc, err := time.LoadLocation(testdata.Raba.Timezone)
	require.NoError(t, err)
	ref := refTime.In(loc)
	expectedMidnight := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, loc)

	assert.Equal(t, expectedMidnight.UnixMilli(), vehicle.TripStatus.ServiceDate.UnixMilli(),
		"serviceDate must be midnight in the agency timezone")
	assert.NotEqual(t, refTime.UnixMilli(), vehicle.TripStatus.ServiceDate.UnixMilli(),
		"serviceDate must not be the raw reference timestamp")
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
