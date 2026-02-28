package restapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// createTestApiWithRealTimeDataB is createTestApiWithRealTimeData adapted for benchmarks (t → b).
func createTestApiWithRealTimeDataB(b *testing.B) (*RestAPI, func()) {
	mux := http.NewServeMux()
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		if err != nil {
			b.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
		if err != nil {
			b.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
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

	gtfsManager, err := gtfs.InitGTFSManager(gtfsConfig)
	if err != nil {
		server.Close()
		b.Fatal(err)
	}

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 100,
		},
		GtfsConfig:  gtfsConfig,
		GtfsManager: gtfsManager,
		Clock:       clock.RealClock{},
	}

	api := NewRestAPI(application)
	cleanup := func() {
		api.Shutdown()
		server.Close()
		gtfsManager.Shutdown()
	}
	return api, cleanup
}

// Benchmark arrivals endpoint (hot path).
func BenchmarkArrivalsAndDeparturesForStop(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeDataB(b)
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Benchmark stops-for-location (high-traffic lookup).
func BenchmarkStopsForLocation(b *testing.B) {
	api := createTestApi(b)

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/stops-for-location.json?key=TEST&lat=40.5865&lon=-122.3917&latSpan=0.05&lonSpan=0.05", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Benchmark vehicles-for-agency with real-time data.
func BenchmarkVehiclesForAgency(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeDataB(b)
	defer cleanup()

	agencies := api.GtfsManager.GetAgencies()
	if len(agencies) == 0 {
		b.Fatal("no agencies")
	}
	agencyID := agencies[0].Id

	mux := http.NewServeMux()
	api.SetRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Benchmark trip-details with real-time data.
func BenchmarkTripDetails(b *testing.B) {
	api, cleanup := createTestApiWithRealTimeDataB(b)
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}
