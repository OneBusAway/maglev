package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"maglev.onebusaway.org/internal/utils"
)

// Benchmark arrivals endpoint (hot path).
func BenchmarkArrivalsAndDeparturesForStop(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeData(b)
	defer cleanup()

	agencies := api.GtfsManager.GetAgencies()
	stops := api.GtfsManager.GetStops()
	if len(agencies) == 0 || len(stops) == 0 {
		b.Fatal("no agencies or stops")
	}
	stopID := utils.FormCombinedID(agencies[0].Id, stops[0].Id)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/arrivals-and-departures-for-stop/"+stopID+".json?key=TEST", nil)

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
	api, cleanup := createTestApiWithRealTimeData(b)
	defer cleanup()

	agencies := api.GtfsManager.GetAgencies()
	if len(agencies) == 0 {
		b.Fatal("no agencies")
	}
	agencyID := agencies[0].Id

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
	api, cleanup := createTestApiWithRealTimeData(b)
	defer cleanup()

	agencies := api.GtfsManager.GetAgencies()
	trips := api.GtfsManager.GetTrips()
	if len(agencies) == 0 || len(trips) == 0 {
		b.Fatal("no agencies or trips")
	}
	tripID := utils.FormCombinedID(agencies[0].Id, trips[0].ID)

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
