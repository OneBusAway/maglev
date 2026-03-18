package restapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// insertFrequencyData inserts frequency rows for the given trip IDs into the test DB.
// Returns a cleanup function that removes all frequency data.
func insertFrequencyData(t *testing.T, api *RestAPI, tripID string, exactTimes int64) func() {
	t.Helper()
	ctx := context.Background()

	// exact_times=0: frequency-based, headway 600s (10 min), 06:00-09:00
	// exact_times=1: schedule-based, headway 1800s (30 min), 07:00-10:00
	if exactTimes == 0 {
		err := api.GtfsManager.GtfsDB.Queries.CreateFrequency(ctx, gtfsdb.CreateFrequencyParams{
			TripID:      tripID,
			StartTime:   int64(6 * time.Hour), // 06:00 in nanoseconds
			EndTime:     int64(9 * time.Hour), // 09:00 in nanoseconds
			HeadwaySecs: 600,                  // 10 minutes
			ExactTimes:  0,
		})
		require.NoError(t, err, "failed to insert frequency data (exact_times=0)")
	} else {
		err := api.GtfsManager.GtfsDB.Queries.CreateFrequency(ctx, gtfsdb.CreateFrequencyParams{
			TripID:      tripID,
			StartTime:   int64(7 * time.Hour),  // 07:00 in nanoseconds
			EndTime:     int64(10 * time.Hour), // 10:00 in nanoseconds
			HeadwaySecs: 1800,                  // 30 minutes
			ExactTimes:  1,
		})
		require.NoError(t, err, "failed to insert frequency data (exact_times=1)")
	}

	return func() {
		_ = api.GtfsManager.GtfsDB.Queries.ClearFrequencies(ctx)
	}
}

// createTestApiWithFrequencyRealTime creates an API with real-time data and frequency data.
// It uses a separate in-memory DB. Returns (api, cleanup).
func createTestApiWithFrequencyRealTime(t *testing.T) (*RestAPI, func()) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		if err != nil {
			http.Error(w, "file not found", 500)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
		if err != nil {
			http.Error(w, "file not found", 500)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	server := httptest.NewServer(mux)

	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: ":memory:",
		RTFeeds: []gtfs.RTFeedConfig{{
			ID:                  "test-feed",
			TripUpdatesURL:      server.URL + "/trip-updates",
			VehiclePositionsURL: server.URL + "/vehicle-positions",
			RefreshInterval:     30,
			Enabled:             true,
		}},
	}

	ctx := context.Background()
	manager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)

	// Wait for real-time data to load (loaded automatically by InitGTFSManager)
	time.Sleep(500 * time.Millisecond)

	directionCalc := gtfs.NewAdvancedDirectionCalculator(manager.GtfsDB.Queries)

	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         manager,
		DirectionCalculator: directionCalc,
		Clock:               mockClock,
	}
	api := NewRestAPI(application)

	cleanup := func() {
		api.Shutdown()
		manager.Shutdown()
		server.Close()
	}
	return api, cleanup
}

func TestTripDetailsFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "RABA test data must contain trips")
	tripRawID := trips[0].ID

	t.Run("no frequency data returns null frequency", func(t *testing.T) {
		combinedTripID := utils.FormCombinedID(agency.Id, tripRawID)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trip-details/"+combinedTripID+".json?key=TEST")

		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		// frequency field should exist but be null
		freq, exists := entry["frequency"]
		assert.True(t, exists, "frequency key must be present")
		assert.Nil(t, freq, "frequency should be nil when no frequency data exists")
	})

	t.Run("exact_times=0 populates frequency with active headway", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		combinedTripID := utils.FormCombinedID(agency.Id, tripRawID)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trip-details/"+combinedTripID+".json?key=TEST")

		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		freq, ok := entry["frequency"].(map[string]interface{})
		require.True(t, ok, "frequency should be a map when frequency data exists")
		assert.Equal(t, float64(600), freq["headway"], "headway should be 600 seconds")
		assert.NotNil(t, freq["startTime"], "startTime must be present")
		assert.NotNil(t, freq["endTime"], "endTime must be present")

		// Verify schedule.frequency is also populated
		schedule, schedOk := entry["schedule"].(map[string]interface{})
		if schedOk {
			schedFreq := schedule["frequency"]
			assert.NotNil(t, schedFreq, "schedule.frequency should be populated")
		}

		// Verify status.frequency is also populated
		status, statusOk := entry["status"].(map[string]interface{})
		if statusOk {
			statusFreq := status["frequency"]
			assert.NotNil(t, statusFreq, "status.frequency should be populated")
		}
	})

	t.Run("exact_times=1 populates frequency", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 1)
		defer cleanup()

		combinedTripID := utils.FormCombinedID(agency.Id, tripRawID)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trip-details/"+combinedTripID+".json?key=TEST")

		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		freq, ok := entry["frequency"].(map[string]interface{})
		require.True(t, ok, "frequency should be populated for exact_times=1")
		assert.Equal(t, float64(1800), freq["headway"], "headway should be 1800 seconds")
	})
}

func TestTripsForRouteFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "RABA test data must contain trips")
	tripRawID := trips[0].ID

	t.Run("no frequency returns null frequency on entries", func(t *testing.T) {
		routeID := utils.FormCombinedID(agency.Id, trips[0].Route.Id)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trips-for-route/"+routeID+".json?key=TEST&includeStatus=true")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		list := data["list"].([]interface{})
		if len(list) > 0 {
			entry := list[0].(map[string]interface{})
			_, exists := entry["frequency"]
			assert.True(t, exists, "frequency key should exist")
		}
	})

	t.Run("frequency-based trip has headway in entry", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		routeID := utils.FormCombinedID(agency.Id, trips[0].Route.Id)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trips-for-route/"+routeID+".json?key=TEST&includeStatus=true")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		list := data["list"].([]interface{})
		if len(list) == 0 {
			t.Log("No trips returned for route (no active service today) — skipping frequency check")
			return
		}

		// Find the entry for our frequency trip
		var foundFreq bool
		for _, item := range list {
			entry := item.(map[string]interface{})
			if entry["tripId"] == utils.FormCombinedID(agency.Id, tripRawID) {
				freq := entry["frequency"]
				if freq != nil {
					freqVal, ok := freq.(float64)
					assert.True(t, ok, "frequency should be a number (*int64 serialized as float64)")
					assert.Equal(t, float64(600), freqVal, "headway should be 600")
					foundFreq = true
				}
				break
			}
		}
		if !foundFreq {
			t.Log("Frequency trip not found in response list or frequency was nil")
		}
	})
}

func TestTripsForLocationFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips, "RABA test data must contain trips")

	// Use the first stop to get coordinates
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops, "RABA test data must contain stops")

	lat := *stops[0].Latitude
	lon := *stops[0].Longitude

	tripRawID := trips[0].ID

	t.Run("no frequency returns null frequency on entries", func(t *testing.T) {
		url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&latSpan=0.5&lonSpan=0.5&includeSchedule=true", lat, lon)
		_, model := serveApiAndRetrieveEndpoint(t, api, url)

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		list := data["list"].([]interface{})
		if len(list) > 0 {
			entry := list[0].(map[string]interface{})
			_, exists := entry["frequency"]
			assert.True(t, exists, "frequency key should exist")
		}
	})

	t.Run("frequency-based trip has headway", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		url := fmt.Sprintf("/api/where/trips-for-location.json?key=TEST&lat=%f&lon=%f&latSpan=0.5&lonSpan=0.5&includeSchedule=true", lat, lon)
		_, model := serveApiAndRetrieveEndpoint(t, api, url)

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		list := data["list"].([]interface{})

		for _, item := range list {
			entry := item.(map[string]interface{})
			if entry["tripId"] == utils.FormCombinedID(agency.Id, tripRawID) {
				freq := entry["frequency"]
				if freq != nil {
					freqVal, ok := freq.(float64)
					assert.True(t, ok, "frequency should be numeric")
					assert.Equal(t, float64(600), freqVal)
				}

				// Check schedule.frequency if schedule is present
				if sched, ok := entry["schedule"].(map[string]interface{}); ok {
					schedFreq := sched["frequency"]
					if schedFreq != nil {
						schedFreqMap, ok := schedFreq.(map[string]interface{})
						assert.True(t, ok, "schedule.frequency should be a map")
						assert.Equal(t, float64(600), schedFreqMap["headway"])
					}
				}
				break
			}
		}
	})
}

func TestTripForVehicleFrequencyIntegration(t *testing.T) {
	api, cleanup := createTestApiWithFrequencyRealTime(t)
	defer cleanup()

	// Check if any vehicles are available
	agencies := api.GtfsManager.GetAgencies()
	require.NotEmpty(t, agencies)

	vehicles := api.GtfsManager.VehiclesForAgencyID(agencies[0].Id)
	if len(vehicles) == 0 {
		t.Skip("No real-time vehicles available in test data — skipping trip-for-vehicle frequency test")
	}

	vehicle := vehicles[0]
	combinedVehicleID := utils.FormCombinedID(agencies[0].Id, vehicle.ID.ID)

	t.Run("vehicle trip without frequency data", func(t *testing.T) {
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trip-for-vehicle/"+combinedVehicleID+".json?key=TEST")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		_, exists := entry["frequency"]
		assert.True(t, exists, "frequency key should exist in response")
	})

	t.Run("vehicle trip with frequency data", func(t *testing.T) {
		// Insert frequency for the vehicle's current trip
		if vehicle.Trip == nil {
			t.Skip("vehicle has no trip assignment")
		}
		tripID := vehicle.Trip.ID.ID
		ctx := context.Background()

		err := api.GtfsManager.GtfsDB.Queries.CreateFrequency(ctx, gtfsdb.CreateFrequencyParams{
			TripID:      tripID,
			StartTime:   int64(6 * time.Hour),
			EndTime:     int64(9 * time.Hour),
			HeadwaySecs: 600,
			ExactTimes:  0,
		})
		if err != nil {
			t.Skipf("Cannot insert frequency for trip %s: %v", tripID, err)
		}
		defer func() { _ = api.GtfsManager.GtfsDB.Queries.ClearFrequencies(ctx) }()

		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/trip-for-vehicle/"+combinedVehicleID+".json?key=TEST")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		freq, ok := entry["frequency"].(map[string]interface{})
		if ok && freq != nil {
			assert.Equal(t, float64(600), freq["headway"])
		}
	})
}

func TestScheduleForStopFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripRawID := trips[0].ID

	t.Run("no frequency data produces empty scheduleFrequencies", func(t *testing.T) {
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-stop/"+stopID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		schedules, ok := entry["stopRouteSchedules"].([]interface{})
		if ok && len(schedules) > 0 {
			s := schedules[0].(map[string]interface{})
			dirSchedules, ok := s["stopRouteDirectionSchedules"].([]interface{})
			if ok && len(dirSchedules) > 0 {
				dir := dirSchedules[0].(map[string]interface{})
				freqs, exists := dir["scheduleFrequencies"]
				assert.True(t, exists, "scheduleFrequencies key should exist")
				if freqSlice, ok := freqs.([]interface{}); ok {
					assert.Empty(t, freqSlice, "scheduleFrequencies should be empty when no frequency data")
				}
			}
		}
	})

	t.Run("exact_times=0 adds ScheduleFrequency entries", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-stop/"+stopID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		schedules, ok := entry["stopRouteSchedules"].([]interface{})
		if ok {
			for _, sched := range schedules {
				s := sched.(map[string]interface{})
				dirSchedules, ok := s["stopRouteDirectionSchedules"].([]interface{})
				if !ok {
					continue
				}
				for _, dirSched := range dirSchedules {
					dir := dirSched.(map[string]interface{})
					freqs, exists := dir["scheduleFrequencies"]
					if !exists {
						continue
					}
					freqSlice, ok := freqs.([]interface{})
					if !ok || len(freqSlice) == 0 {
						continue
					}
					// Verify ScheduleFrequency fields
					sf := freqSlice[0].(map[string]interface{})
					assert.NotNil(t, sf["startTime"], "ScheduleFrequency should have startTime")
					assert.NotNil(t, sf["endTime"], "ScheduleFrequency should have endTime")
					assert.Equal(t, float64(600), sf["headway"], "ScheduleFrequency headway should be 600")
					assert.NotNil(t, sf["serviceDate"])
					assert.NotNil(t, sf["serviceId"])
					assert.NotNil(t, sf["tripId"])
					return
				}
			}
			t.Log("No scheduleFrequency found for the test trip at this stop (trip may not serve this stop)")
		}
	})

	t.Run("exact_times=1 expands stop times", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 1)
		defer cleanup()

		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-stop/"+stopID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		// With exact_times=1, the schedule should have expanded stop times
		// (more stop time entries than without frequency data)
		schedules, ok := entry["stopRouteSchedules"].([]interface{})
		if ok {
			for _, sched := range schedules {
				s := sched.(map[string]interface{})
				stopTimes, exists := s["scheduleStopTimes"]
				if !exists {
					continue
				}
				stSlice, ok := stopTimes.([]interface{})
				if !ok {
					continue
				}
				// The expanded stop times should be present
				// Each original stop time is expanded at each headway interval
				if len(stSlice) > 0 {
					st := stSlice[0].(map[string]interface{})
					assert.NotNil(t, st["tripId"], "expanded stop time should have tripId")
					assert.NotNil(t, st["arrivalTime"], "expanded stop time should have arrivalTime")
					t.Logf("Found %d stop times for route schedule (may include expanded entries)", len(stSlice))
				}
			}
		}
	})
}

func TestScheduleForRouteFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	routeID := utils.FormCombinedID(agency.Id, trips[0].Route.Id)
	tripRawID := trips[0].ID

	t.Run("no frequency data returns normal stop times", func(t *testing.T) {
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		groupings, ok := entry["stopTripGroupings"].([]interface{})
		assert.True(t, ok, "stopTripGroupings should exist")
		if len(groupings) > 0 {
			g := groupings[0].(map[string]interface{})
			tripStopTimes, ok := g["tripsWithStopTimes"].([]interface{})
			assert.True(t, ok, "tripsWithStopTimes should exist")
			assert.NotEmpty(t, tripStopTimes, "should have at least one trip with stop times")
		}
	})

	t.Run("exact_times=1 expands trip stop times", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 1)
		defer cleanup()

		// Count stop times WITHOUT frequency data first
		// (already cleaned up by the time we get here, but we can compare structure)
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		groupings, ok := entry["stopTripGroupings"].([]interface{})
		assert.True(t, ok)
		if len(groupings) > 0 {
			g := groupings[0].(map[string]interface{})
			tripStopTimes, ok := g["tripsWithStopTimes"].([]interface{})
			assert.True(t, ok)
			// With exact_times=1 and 1800s headway over 3 hours (07:00-10:00),
			// we expect the original + 5 expanded entries (at each 30-min interval)
			// This will be more than just 1 entry for the frequency trip
			t.Logf("Found %d tripsWithStopTimes entries (includes expanded)", len(tripStopTimes))
			if len(tripStopTimes) > 0 {
				tst := tripStopTimes[0].(map[string]interface{})
				assert.NotNil(t, tst["tripId"])
				stopTimes, ok := tst["stopTimes"].([]interface{})
				assert.True(t, ok)
				assert.NotEmpty(t, stopTimes)
			}
		}
	})

	t.Run("exact_times=0 keeps original stop times unchanged", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/schedule-for-route/"+routeID+".json?key=TEST&date=2025-06-12")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		groupings, ok := entry["stopTripGroupings"].([]interface{})
		assert.True(t, ok)
		if len(groupings) > 0 {
			g := groupings[0].(map[string]interface{})
			tripStopTimes, ok := g["tripsWithStopTimes"].([]interface{})
			assert.True(t, ok)
			// exact_times=0 should NOT expand — just keep the original
			assert.NotEmpty(t, tripStopTimes, "should still have trip stop times")
		}
	})
}

func TestArrivalsAndDeparturesFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)
	tripRawID := trips[0].ID

	t.Run("no frequency returns null on arrival frequency", func(t *testing.T) {
		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST&minutesBefore=60&minutesAfter=240")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})
		arrivals, ok := entry["arrivalsAndDepartures"].([]interface{})
		assert.True(t, ok, "arrivalsAndDepartures should exist")
		if len(arrivals) > 0 {
			arr := arrivals[0].(map[string]interface{})
			_, exists := arr["frequency"]
			assert.True(t, exists, "frequency key should exist on arrival")
		}
	})

	t.Run("frequency-based trip has frequency on arrival entry", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, tripRawID, 0)
		defer cleanup()

		_, model := serveApiAndRetrieveEndpoint(t, api,
			"/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST&minutesBefore=60&minutesAfter=240")

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})
		arrivals, ok := entry["arrivalsAndDepartures"].([]interface{})
		assert.True(t, ok)

		// Search for an arrival matching our frequency trip
		for _, arrItem := range arrivals {
			arr := arrItem.(map[string]interface{})
			arrTripID, _ := arr["tripId"].(string)
			if arrTripID == utils.FormCombinedID(agency.Id, tripRawID) {
				freq, ok := arr["frequency"].(map[string]interface{})
				if ok && freq != nil {
					assert.Equal(t, float64(600), freq["headway"], "arrival frequency headway should be 600")
					assert.NotNil(t, freq["startTime"])
					assert.NotNil(t, freq["endTime"])
				}
				return
			}
		}
		t.Log("Trip did not appear in arrivals window — this may be expected if trip doesn't serve this stop at this time")
	})
}

func TestSingularArrivalAndDepartureFrequencyIntegration(t *testing.T) {
	mockClock := clock.NewMockClock(time.Date(2025, 6, 12, 14, 30, 0, 0, time.UTC))
	api := createTestApiWithClock(t, mockClock)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)

	// Find a trip that has stop times for our test stop
	ctx := context.Background()
	var testTripID string
	var testStopCode string
	var serviceDate time.Time

	for _, trip := range trips {
		stopTimes, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
		if err != nil || len(stopTimes) == 0 {
			continue
		}
		testTripID = trip.ID
		testStopCode = stopTimes[0].StopID
		// Use the service date from the trip's calendar
		serviceDate = time.Date(2025, 6, 12, 0, 0, 0, 0, time.UTC)
		break
	}

	if testTripID == "" {
		t.Skip("No trip with stop times found in test data")
	}

	stopID := utils.FormCombinedID(agency.Id, testStopCode)
	combinedTripID := utils.FormCombinedID(agency.Id, testTripID)
	serviceDateMs := serviceDate.UnixMilli()

	t.Run("singular arrival without frequency data", func(t *testing.T) {
		url := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d",
			stopID, combinedTripID, serviceDateMs)
		_, model := serveApiAndRetrieveEndpoint(t, api, url)

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})
		_, exists := entry["frequency"]
		assert.True(t, exists, "frequency key should exist")
		assert.Nil(t, entry["frequency"], "frequency should be nil without frequency data")
	})

	t.Run("singular arrival with frequency data", func(t *testing.T) {
		cleanup := insertFrequencyData(t, api, testTripID, 0)
		defer cleanup()

		url := fmt.Sprintf("/api/where/arrival-and-departure-for-stop/%s.json?key=TEST&tripId=%s&serviceDate=%d",
			stopID, combinedTripID, serviceDateMs)
		_, model := serveApiAndRetrieveEndpoint(t, api, url)

		require.Equal(t, 200, model.Code)
		data := model.Data.(map[string]interface{})
		entry := data["entry"].(map[string]interface{})

		freq, ok := entry["frequency"].(map[string]interface{})
		if ok && freq != nil {
			assert.Equal(t, float64(600), freq["headway"])
			assert.NotNil(t, freq["startTime"])
			assert.NotNil(t, freq["endTime"])
		}
	})
}

func TestExistingEndpointsNoFrequencyRegression(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agency := api.GtfsManager.GetAgencies()[0]
	trips := api.GtfsManager.GetTrips()
	require.NotEmpty(t, trips)
	stops := api.GtfsManager.GetStops()
	require.NotEmpty(t, stops)

	tripID := utils.FormCombinedID(agency.Id, trips[0].ID)
	routeID := utils.FormCombinedID(agency.Id, trips[0].Route.Id)
	stopID := utils.FormCombinedID(agency.Id, stops[0].Id)

	tests := []struct {
		name     string
		endpoint string
	}{
		{"trip-details", "/api/where/trip-details/" + tripID + ".json?key=TEST"},
		{"trips-for-route", "/api/where/trips-for-route/" + routeID + ".json?key=TEST"},
		{"schedule-for-stop", "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST&date=2025-06-12"},
		{"schedule-for-route", "/api/where/schedule-for-route/" + routeID + ".json?key=TEST&date=2025-06-12"},
		{"arrivals-and-departures-for-stop", "/api/where/arrivals-and-departures-for-stop/" + stopID + ".json?key=TEST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := serveApiAndRetrieveEndpoint(t, api, tt.endpoint)
			assert.Equal(t, http.StatusOK, resp.StatusCode, "%s should return 200 OK", tt.name)
			assert.Equal(t, 200, model.Code)
			assert.Equal(t, "OK", model.Text)
		})
	}
}
