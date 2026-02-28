//go:build perftest

package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkLargeAgencyStopsForLocation measures stops-for-location response time over a large GTFS feed.
func BenchmarkLargeAgencyStopsForLocation(b *testing.B) {
	api := createLargeAgencyApi(b)
	defer api.Shutdown()
	mux := http.NewServeMux()
	api.SetRoutes(mux)
	// Portland area; realistic lat/lon for TriMet
	url := "/api/where/stops-for-location.json?key=TEST&lat=45.52&lon=-122.68&radius=2000"
	req := httptest.NewRequest("GET", url, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
	}
}
