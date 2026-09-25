package restapi

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"maglev.onebusaway.org/internal/models"
)

// onDemandSpecPath is the locally maintained OpenAPI document for /api/ondemand;
// testdata/openapi.yml stays synced with upstream and does not describe it.
var onDemandSpecPath = filepath.Join("../../testdata", "openapi-ondemand.yml")

func TestOpenAPIConformance_OnDemandEndpoints(t *testing.T) {
	doc := loadOpenAPISpecFrom(t, onDemandSpecPath)

	fixtures := []struct {
		name      string
		fixture   string
		endpoints []struct{ name, endpoint, specPath string }
	}{
		{
			name:    "alexandria",
			fixture: "alexandria-flex.zip",
			endpoints: []struct{ name, endpoint, specPath string }{
				{"service full", "/api/ondemand/service/5088_77652.json?key=TEST", "/api/ondemand/service/{serviceID}.json"},
				{"service none", "/api/ondemand/service/5088_77652.json?key=TEST&geometryDetail=none", "/api/ondemand/service/{serviceID}.json"},
				{"services-for-agency", "/api/ondemand/services-for-agency/5088.json?key=TEST", "/api/ondemand/services-for-agency/{agencyID}.json"},
				{"services-for-location point inside", "/api/ondemand/services-for-location.json?key=TEST&lat=38.836368&lon=-77.049221", "/api/ondemand/services-for-location.json"},
				{"services-for-location near miss", "/api/ondemand/services-for-location.json?key=TEST&lat=38.60&lon=-77.20&radius=2500", "/api/ondemand/services-for-location.json"},
				{"services-for-location viewport", "/api/ondemand/services-for-location.json?key=TEST&lat=38.9&lon=-77.1&latSpan=0.05&lonSpan=0.05", "/api/ondemand/services-for-location.json"},
				{"services-for-location empty", "/api/ondemand/services-for-location.json?key=TEST&lat=39.5&lon=-77.0", "/api/ondemand/services-for-location.json"},
			},
		},
		{
			name:    "charlevoix",
			fixture: "charlevoix-flex.zip",
			endpoints: []struct{ name, endpoint, specPath string }{
				{"service with group and stops", "/api/ondemand/service/CC_CC3.json?key=TEST", "/api/ondemand/service/{serviceID}.json"},
				{"services-for-agency", "/api/ondemand/services-for-agency/CC.json?key=TEST", "/api/ondemand/services-for-agency/{agencyID}.json"},
				{"services-for-location stop match", "/api/ondemand/services-for-location.json?key=TEST&lat=45.2562297&lon=-85.1852475", "/api/ondemand/services-for-location.json"},
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			api := createTestApiWithFeed(t, models.GetFixturePath(t, fixture.fixture))
			server := httptest.NewServer(api.SetupAPIRoutes())
			defer server.Close()
			for _, tt := range fixture.endpoints {
				t.Run(tt.name, func(t *testing.T) {
					assertConformance(t, server.URL, doc, tt.endpoint, tt.specPath)
				})
			}
		})
	}
}
