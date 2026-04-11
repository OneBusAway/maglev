package restapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/app"
	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/clock"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

// specPath is the relative path to the OpenAPI spec from this test package.
var specPath = filepath.Join("../../testdata", "openapi.yml")

// loadOpenAPISpec loads and validates the OpenAPI specification.
func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(specPath)
	require.NoError(t, err, "Failed to load OpenAPI spec from %s", specPath)

	err = doc.Validate(loader.Context)
	require.NoError(t, err, "OpenAPI spec validation failed")

	return doc
}

// getResponseSchema extracts the JSON response schema for a given spec path and HTTP method.
// It resolves allOf composition and returns the merged schema ready for validation.
func getResponseSchema(t *testing.T, doc *openapi3.T, specEndpointPath string) *openapi3.Schema {
	t.Helper()

	pathItem := doc.Paths.Find(specEndpointPath)
	require.NotNil(t, pathItem, "Path %q not found in OpenAPI spec", specEndpointPath)

	operation := pathItem.Get
	require.NotNil(t, operation, "No GET operation for path %q", specEndpointPath)

	resp200 := operation.Responses.Status(200)
	require.NotNil(t, resp200, "No 200 response for GET %q", specEndpointPath)

	content := resp200.Value.Content.Get("application/json")
	require.NotNil(t, content, "No application/json content for GET %q 200", specEndpointPath)

	schema := content.Schema.Value
	require.NotNil(t, schema, "No schema value for GET %q 200 response", specEndpointPath)

	return schema
}

// validateJSONAgainstSchema validates a parsed JSON value against an OpenAPI schema.
// Returns a list of validation errors (empty if conformant).
func validateJSONAgainstSchema(schema *openapi3.Schema, jsonValue interface{}) []error {
	err := schema.VisitJSON(jsonValue, openapi3.MultiErrors())
	if err == nil {
		return nil
	}
	if multiErr, ok := err.(openapi3.MultiError); ok {
		return []error(multiErr)
	}
	return []error{err}
}

// serveAndCaptureRawJSON makes an HTTP request to the test server and returns
// the status code and parsed JSON body.
func serveAndCaptureRawJSON(t *testing.T, serverURL string, endpoint string) (int, map[string]interface{}) {
	t.Helper()

	client := &http.Client{}
	resp, err := client.Get(serverURL + endpoint)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(body, &parsed)
	require.NoError(t, err, "Failed to parse response JSON: %s", string(body))

	return resp.StatusCode, parsed
}

// assertConformance is the core validation helper. It makes a request to the given endpoint,
// validates the response against the OpenAPI spec schema for the specPath, and reports
// any schema violations as test failures.
func assertConformance(t *testing.T, serverURL string, doc *openapi3.T, endpointURL string, specEndpointPath string) {
	t.Helper()

	statusCode, jsonBody := serveAndCaptureRawJSON(t, serverURL, endpointURL)
	require.Equal(t, http.StatusOK, statusCode, "Expected 200 OK for %s, got %d", endpointURL, statusCode)

	schema := getResponseSchema(t, doc, specEndpointPath)
	errs := validateJSONAgainstSchema(schema, jsonBody)
	if len(errs) > 0 {
		for _, e := range errs {
			t.Errorf("Schema violation for %s: %v", specEndpointPath, e)
		}
	}
}

// --- Conformance Tests ---

// createConformanceTestApi creates a test API with a high rate limit suitable for conformance testing
// where many sequential requests are made.
func createConformanceTestApi(t *testing.T) *RestAPI {
	ctx := context.Background()

	// Initialize the shared GTFS manager only once (reuses the same testDbSetupOnce from http_test.go)
	testDbSetupOnce.Do(func() {
		gtfsConfig := gtfs.Config{
			GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
			GTFSDataPath: testDbPath,
		}
		var err error
		testGtfsManager, err = gtfs.InitGTFSManager(ctx, gtfsConfig)
		if err != nil {
			t.Fatalf("Failed to initialize shared test GTFS manager: %v", err)
		}
		testDirectionCalculator = gtfs.NewAdvancedDirectionCalculator(testGtfsManager.GtfsDB.Queries)
	})

	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: testDbPath,
	}

	application := &app.Application{
		Config: appconf.Config{
			Env:       appconf.EnvFlagToEnvironment("test"),
			ApiKeys:   []string{"TEST"},
			RateLimit: 1000, // High rate limit for conformance testing
		},
		GtfsConfig:          gtfsConfig,
		GtfsManager:         testGtfsManager,
		DirectionCalculator: testDirectionCalculator,
		Clock:               clock.RealClock{},
	}

	api := NewRestAPI(application)
	return api
}

// TestOpenAPIConformance_StaticEndpoints tests all endpoints that use static GTFS data
// against the OpenAPI specification schemas.
func TestOpenAPIConformance_StaticEndpoints(t *testing.T) {
	api := createConformanceTestApi(t)
	defer api.Shutdown()

	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	doc := loadOpenAPISpec(t)

	// Gather test data IDs from the RABA fixture
	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies, "Test data must contain at least one agency")
	agencyID := agencies[0].ID

	stops := mustGetStops(t, api)
	require.NotEmpty(t, stops, "Test data must contain at least one stop")
	stopID := utils.FormCombinedID(agencyID, stops[0].ID)

	routes := mustGetRoutes(t, api)
	require.NotEmpty(t, routes, "Test data must contain at least one route")
	firstRouteID := utils.FormCombinedID(agencyID, routes[0].ID)

	firstTripID := utils.FormCombinedID(agencyID, mustGetTrip(t, api).ID)
	firstBlockID := utils.FormCombinedID(agencyID, mustGetTripIDWithBlockID(t, api))
	firstShapeID := utils.FormCombinedID(agencyID, mustGetTripIDWithShapeID(t, api))

	tests := []struct {
		name     string
		endpoint string // actual request URL
		specPath string // path in the OpenAPI spec
	}{
		{
			name:     "agencies-with-coverage",
			endpoint: "/api/where/agencies-with-coverage.json?key=TEST",
			specPath: "/api/where/agencies-with-coverage.json",
		},
		{
			name:     "current-time",
			endpoint: "/api/where/current-time.json?key=TEST",
			specPath: "/api/where/current-time.json",
		},
		{
			name:     "config",
			endpoint: "/api/where/config.json?key=TEST",
			specPath: "/api/where/config.json",
		},
		{
			name:     "agency",
			endpoint: "/api/where/agency/" + agencyID + ".json?key=TEST",
			specPath: "/api/where/agency/{agencyID}.json",
		},
		{
			name:     "stop",
			endpoint: "/api/where/stop/" + stopID + ".json?key=TEST",
			specPath: "/api/where/stop/{stopID}.json",
		},
		{
			name:     "route",
			endpoint: "/api/where/route/" + firstRouteID + ".json?key=TEST",
			specPath: "/api/where/route/{routeID}.json",
		},
		{
			name:     "routes-for-agency",
			endpoint: "/api/where/routes-for-agency/" + agencyID + ".json?key=TEST",
			specPath: "/api/where/routes-for-agency/{agencyID}.json",
		},
		{
			name:     "route-ids-for-agency",
			endpoint: "/api/where/route-ids-for-agency/" + agencyID + ".json?key=TEST",
			specPath: "/api/where/route-ids-for-agency/{agencyID}.json",
		},
		{
			name:     "stops-for-agency",
			endpoint: "/api/where/stops-for-agency/" + agencyID + ".json?key=TEST",
			specPath: "/api/where/stops-for-agency/{agencyID}.json",
		},
		{
			name:     "stop-ids-for-agency",
			endpoint: "/api/where/stop-ids-for-agency/" + agencyID + ".json?key=TEST",
			specPath: "/api/where/stop-ids-for-agency/{agencyID}.json",
		},
		{
			name:     "stops-for-route",
			endpoint: "/api/where/stops-for-route/" + firstRouteID + ".json?key=TEST",
			specPath: "/api/where/stops-for-route/{routeID}.json",
		},
		{
			name:     "trip",
			endpoint: "/api/where/trip/" + firstTripID + ".json?key=TEST",
			specPath: "/api/where/trip/{tripID}.json",
		},
		{
			name:     "trip-details",
			endpoint: "/api/where/trip-details/" + firstTripID + ".json?key=TEST",
			specPath: "/api/where/trip-details/{tripID}.json",
		},
		{
			name:     "trips-for-route",
			endpoint: "/api/where/trips-for-route/" + firstRouteID + ".json?key=TEST&includeStatus=true&includeSchedule=true",
			specPath: "/api/where/trips-for-route/{routeID}.json",
		},
		{
			name:     "schedule-for-stop",
			endpoint: "/api/where/schedule-for-stop/" + stopID + ".json?key=TEST",
			specPath: "/api/where/schedule-for-stop/{stopID}.json",
		},
		{
			name:     "schedule-for-route",
			endpoint: "/api/where/schedule-for-route/" + firstRouteID + ".json?key=TEST",
			specPath: "/api/where/schedule-for-route/{routeID}.json",
		},
		{
			name:     "report-problem-with-stop",
			endpoint: "/api/where/report-problem-with-stop/" + stopID + ".json?key=TEST",
			specPath: "/api/where/report-problem-with-stop/{stopID}.json",
		},
		{
			name:     "report-problem-with-trip",
			endpoint: "/api/where/report-problem-with-trip/" + firstTripID + ".json?key=TEST",
			specPath: "/api/where/report-problem-with-trip/{tripID}.json",
		},
	}

	// Add block test only if we found a block ID in the test data
	if firstBlockID != "" {
		tests = append(tests, struct {
			name     string
			endpoint string
			specPath string
		}{
			name:     "block",
			endpoint: "/api/where/block/" + firstBlockID + ".json?key=TEST",
			specPath: "/api/where/block/{blockID}.json",
		})
	}

	// Add shape test only if we found a shape ID in the test data
	if firstShapeID != "" {
		tests = append(tests, struct {
			name     string
			endpoint string
			specPath string
		}{
			name:     "shape",
			endpoint: "/api/where/shape/" + firstShapeID + ".json?key=TEST",
			specPath: "/api/where/shape/{shapeID}.json",
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConformance(t, server.URL, doc, tt.endpoint, tt.specPath)
		})
	}
}

// TestOpenAPIConformance_LocationEndpoints tests location-based endpoints.
func TestOpenAPIConformance_LocationEndpoints(t *testing.T) {
	api := createConformanceTestApi(t)
	defer api.Shutdown()

	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	doc := loadOpenAPISpec(t)

	// Use coordinates within the RABA service area (Redding, CA area)
	tests := []struct {
		name     string
		endpoint string
		specPath string
	}{
		{
			name:     "stops-for-location",
			endpoint: "/api/where/stops-for-location.json?key=TEST&lat=40.58&lon=-122.39&radius=5000",
			specPath: "/api/where/stops-for-location.json",
		},
		{
			name:     "routes-for-location",
			endpoint: "/api/where/routes-for-location.json?key=TEST&lat=40.58&lon=-122.39&radius=5000",
			specPath: "/api/where/routes-for-location.json",
		},
		{
			name:     "trips-for-location",
			endpoint: "/api/where/trips-for-location.json?key=TEST&lat=40.58&lon=-122.39&latSpan=0.1&lonSpan=0.1",
			specPath: "/api/where/trips-for-location.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConformance(t, server.URL, doc, tt.endpoint, tt.specPath)
		})
	}
}

// TestOpenAPIConformance_SearchEndpoints tests search endpoints.
func TestOpenAPIConformance_SearchEndpoints(t *testing.T) {
	api := createConformanceTestApi(t)
	defer api.Shutdown()

	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	doc := loadOpenAPISpec(t)

	tests := []struct {
		name     string
		endpoint string
		specPath string
	}{
		{
			name:     "search-stop",
			endpoint: "/api/where/search/stop.json?key=TEST&input=transit",
			specPath: "/api/where/search/stop.json",
		},
		{
			name:     "search-route",
			endpoint: "/api/where/search/route.json?key=TEST&input=route",
			specPath: "/api/where/search/route.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConformance(t, server.URL, doc, tt.endpoint, tt.specPath)
		})
	}
}

// TestOpenAPIConformance_RealTimeEndpoints tests endpoints that require real-time GTFS-RT data.
func TestOpenAPIConformance_RealTimeEndpoints(t *testing.T) {
	ctx := context.Background()

	// Create HTTP server to serve GTFS-RT protobuf files
	mux := http.NewServeMux()
	mux.HandleFunc("/vehicle-positions", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-vehicle-positions.pb"))
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/trip-updates", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(filepath.Join("../../testdata", "raba-trip-updates.pb"))
		if err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(data)
	})
	pbServer := httptest.NewServer(mux)
	defer pbServer.Close()

	gtfsConfig := gtfs.Config{
		GtfsURL:      filepath.Join("../../testdata", "raba.zip"),
		GTFSDataPath: ":memory:",
		RTFeeds: []gtfs.RTFeedConfig{
			{
				ID:                  "test-feed",
				TripUpdatesURL:      pbServer.URL + "/trip-updates",
				VehiclePositionsURL: pbServer.URL + "/vehicle-positions",
				RefreshInterval:     30,
				Enabled:             true,
			},
		},
	}

	gtfsManager, err := gtfs.InitGTFSManager(ctx, gtfsConfig)
	require.NoError(t, err)
	defer gtfsManager.Shutdown()

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
		Clock:               clock.RealClock{},
	}

	api := NewRestAPI(application)
	defer api.Shutdown()

	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	doc := loadOpenAPISpec(t)

	agencies := mustGetAgencies(t, api)
	require.NotEmpty(t, agencies)
	agencyID := agencies[0].ID

	vehicles, err := api.GtfsManager.VehiclesForAgencyID(ctx, agencyID)
	require.Nil(t, err)
	require.NotEmpty(t, vehicles, "Real-time vehicles must be loaded for conformance testing")

	t.Run("vehicles-for-agency", func(t *testing.T) {
		assertConformance(t, server.URL, doc,
			"/api/where/vehicles-for-agency/"+agencyID+".json?key=TEST",
			"/api/where/vehicles-for-agency/{agencyID}.json",
		)
	})
}

// TestOpenAPIConformance_ErrorResponses tests that error responses also conform to the spec's ResponseWrapper.
func TestOpenAPIConformance_ErrorResponses(t *testing.T) {
	api := createConformanceTestApi(t)
	defer api.Shutdown()

	server := httptest.NewServer(api.SetupAPIRoutes())
	defer server.Close()

	doc := loadOpenAPISpec(t)

	// Extract the ResponseWrapper schema for error responses
	wrapperRef := doc.Components.Schemas["ResponseWrapper"]
	require.NotNil(t, wrapperRef, "ResponseWrapper schema must exist in spec")
	wrapperSchema := wrapperRef.Value

	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{
			name:           "invalid-api-key",
			endpoint:       "/api/where/current-time.json?key=INVALID",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "not-found-stop",
			endpoint:       "/api/where/stop/25_NONEXISTENT.json?key=TEST",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			statusCode, jsonBody := serveAndCaptureRawJSON(t, server.URL, tt.endpoint)
			assert.Equal(t, tt.expectedStatus, statusCode)

			errs := validateJSONAgainstSchema(wrapperSchema, jsonBody)
			for _, e := range errs {
				t.Errorf("Error response schema violation: %v", e)
			}
		})
	}
}
