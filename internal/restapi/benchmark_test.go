package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"maglev.onebusaway.org/internal/clock"
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
}

// Benchmark arrivals-and-departures-for-location on the same RABA window as
// the for-stop benchmark, so the two can be compared after trip status moves
// to after maxCount. An empty list would measure the wrong path.
func BenchmarkArrivalsAndDeparturesForLocation(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b, clock.NewMockClock(arrivalsTestClock))
	defer cleanup()

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, arrivalsForLocationURL(arrivalsForLocationCenter), nil)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		b.Fatalf("expected 200, got %d", w.Code)
	}

	var warmup ArrivalsAndDeparturesForLocationResponse
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
