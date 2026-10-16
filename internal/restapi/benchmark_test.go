package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/servicedate"
	"maglev.onebusaway.org/internal/utils"
)

// Benchmark arrivals endpoint (hot path).
//
// The RABA fixture's calendar ended 2025-12-31, so a real clock leaves every trip
// out of service and the handler returns an empty list without ever entering its
// per-arrival loop. Pin the clock inside the service window and ask for a stop
// that has service then, the same pair the handler tests use.
func BenchmarkArrivalsAndDeparturesForStop(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/arrivals-and-departures-for-stop/"+arrivalsTestStopID+".json?key=TEST", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("expected 200, got %d", w.Code)
	}

	// Without this the benchmark still passes on an empty list, which is how it
	// came to measure the empty path unnoticed.
	var warmup ArrivalsAndDeparturesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &warmup); err != nil {
		b.Fatalf("decode warmup response: %v", err)
	}
	if len(warmup.Data.Entry.ArrivalsAndDepartures) == 0 {
		b.Fatal("no arrivals in the benchmark window")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
	b.StopTimer()
}

// Benchmark stops-for-location (high-traffic lookup).
func BenchmarkStopsForLocation(b *testing.B) {
	api := createTestApi(b)
	defer api.Shutdown()

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/stops-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=0.05&lonSpan=0.05", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("expected 200, got %d", w.Code)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Benchmark vehicles-for-agency with real-time data.
func BenchmarkVehiclesForAgency(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.RealClock{})
	defer cleanup()

	agencies := mustGetAgencies(b, api)
	if len(agencies) == 0 {
		b.Fatal("no agencies")
	}
	agencyID := agencies[0].ID

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("expected 200, got %d", w.Code)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Benchmark trip-details with real-time data.
func BenchmarkTripDetails(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.RealClock{})
	defer cleanup()

	agencies := mustGetAgencies(b, api)
	if len(agencies) == 0 {
		b.Fatal("no agencies")
	}
	trip := mustGetTrip(b, api)
	tripID := utils.FormCombinedID(agencies[0].ID, trip.ID)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/trip-details/"+tripID+".json?key=TEST", nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("expected 200, got %d", w.Code)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// BenchmarkGetPredictedTimesRABA measures one prediction using a preloaded schedule.
func BenchmarkGetPredictedTimesRABA(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()
	agencies := mustGetAgencies(b, api)
	require.Len(b, agencies, 1, "this benchmark uses the single-agency RABA fixture")
	loc, err := time.LoadLocation(agencies[0].Timezone)
	require.NoError(b, err)
	serviceStart := servicedate.FromInstant(arrivalsTestClock, loc).Start(loc)
	type input struct {
		tripID, stopID     string
		sequence           int64
		arrival, departure time.Time
		schedule           tripSchedule
	}
	var inputs []input
	trips := api.GtfsManager.GetRealTimeTrips()
	sort.Slice(trips, func(i, j int) bool { return trips[i].ID.ID < trips[j].ID.ID })
	for _, trip := range trips {
		rows, err := api.GtfsManager.GtfsDB.Queries.GetStopTimesForTrip(b.Context(), trip.ID.ID)
		require.NoError(b, err)
		schedule := newTripSchedule(rows)
		for _, row := range rows {
			inputs = append(inputs, input{
				tripID: trip.ID.ID, stopID: row.StopID, sequence: row.StopSequence,
				arrival:   serviceStart.Add(time.Duration(row.ArrivalTime)),
				departure: serviceStart.Add(time.Duration(row.DepartureTime)),
				schedule:  schedule,
			})
		}
	}
	require.Len(b, trips, 9)
	require.Len(b, inputs, 204)
	ctx := b.Context()
	// Frozen fixture expectations, independent of the prediction helpers: one
	// trip has an arrival at seq 1; its later visits inherit that offset. All
	// other realtime trips contain only SKIPPED updates and cannot predict.
	const timingTrip = "28c61524-6da8-4506-9a92-22f2f6e91872"
	expectedTimes := map[int64][2]int64{
		1: {1749418441, 1749418441},
		2: {1749422041, 1749422641},
		3: {1749422641, 1749422641},
	}
	predictions := 0
	for _, in := range inputs {
		arrival, departure, predicted := api.getPredictedTimes(ctx, in.tripID, in.stopID, in.sequence, in.arrival, in.departure, in.schedule)
		expected, hasTiming := expectedTimes[in.sequence]
		wantPrediction := in.tripID == timingTrip && hasTiming
		require.Equal(b, wantPrediction, predicted, "trip=%s seq=%d", in.tripID, in.sequence)
		if !wantPrediction {
			require.True(b, arrival.IsZero() && departure.IsZero())
			continue
		}
		if in.sequence == 1 {
			// Predictions based on absolute Time can hide an incorrect anchor
			// through offset cancellation, so check the scheduled input too.
			require.Equal(b, time.Date(2025, time.June, 13, 14, 25, 0, 0, loc), in.arrival)
		}
		require.True(b, arrival.Equal(time.Unix(expected[0], 0)))
		require.True(b, departure.Equal(time.Unix(expected[1], 0)))
		predictions++
	}
	require.Equal(b, 3, predictions)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in := inputs[i%len(inputs)]
		api.getPredictedTimes(ctx, in.tripID, in.stopID, in.sequence, in.arrival, in.departure, in.schedule)
	}
	b.StopTimer()
}
