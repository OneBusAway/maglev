package restapi

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/gtfsdb"
)

// shapeURL builds the /shape endpoint URL with key=TEST baked in. Tests that
// want a different key (auth checks) build their URL inline.
func shapeURL(combinedShapeID string) string {
	return "/api/where/shape/" + combinedShapeID + ".json?key=TEST"
}

type shapePoint struct {
	lat      float64
	lon      float64
	sequence int64
}

// setupShapeTest creates a test agency and inserts shape points into the database.
// Returns the combined "agencyID_shapeID" used by the handler.
func setupShapeTest(t *testing.T, api *RestAPI, shapeID string, points []shapePoint) string {
	t.Helper()
	ctx := context.Background()
	const agencyID = "TestAgency1"

	_, err := api.GtfsManager.GtfsDB.Queries.CreateAgency(ctx, gtfsdb.CreateAgencyParams{
		ID:       agencyID,
		Name:     "Test Transit Agency",
		Url:      "http://test-agency.com",
		Timezone: "America/Los_Angeles",
	})
	require.NoError(t, err)

	for _, p := range points {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
			ShapeID:           shapeID,
			Lat:               p.lat,
			Lon:               p.lon,
			ShapePtSequence:   p.sequence,
			ShapeDistTraveled: sql.NullFloat64{Float64: 0, Valid: false},
		})
		require.NoError(t, err)
	}

	return agencyID + "_" + shapeID
}

// decodePolylinePoints decodes a Google encoded polyline string into [lat, lon] pairs.
func decodePolylinePoints(t *testing.T, encoded string) [][]float64 {
	t.Helper()
	coords, _, err := polyline.DecodeCoords([]byte(encoded))
	require.NoError(t, err)
	return coords
}

func TestShapesHandlerEndToEnd(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	points := []shapePoint{
		{38.56173, -121.76392, 0},
		{38.56205, -121.76288, 1},
		{38.56211, -121.76244, 2},
		{38.56210, -121.75955, 3},
		{38.56200, -121.75860, 4},
		{38.55997, -121.75855, 5},
		{38.55672, -121.75857, 6},
	}
	combinedShapeID := setupShapeTest(t, api, "simple_shape", points)

	resp, model := callAPIHandler[ShapeEntryResponse](t, api, shapeURL(combinedShapeID))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	entry := model.Data.Entry
	assert.Equal(t, len(points), entry.Length)
	assert.Equal(t, "", entry.Levels)
	require.NotEmpty(t, entry.Points)

	decoded := decodePolylinePoints(t, entry.Points)
	require.Len(t, decoded, len(points), "decoded coords count should match input points")
	const tolerance = 0.00001
	for i, p := range points {
		assert.InDelta(t, p.lat, decoded[i][0], tolerance, "decoded[%d].lat", i)
		assert.InDelta(t, p.lon, decoded[i][1], tolerance, "decoded[%d].lon", i)
	}
}

func TestShapesHandlerReturnsNotFoundWhenShapeDoesNotExist(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ShapeEntryResponse](t, api, shapeURL("wrong_id"))

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
}

func TestShapesHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ShapeEntryResponse](t, api,
		"/api/where/shape/25_any_shape.json?key=INVALID")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestShapesHandlerWithMissingApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[ShapeEntryResponse](t, api,
		"/api/where/shape/25_any_shape.json")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
}

// TestShapesHandler_InvalidIDs covers both malformed IDs (no underscore
// separator) and empty IDs — both should return 400.
func TestShapesHandler_InvalidIDs(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	tests := []struct {
		name string
		path string
	}{
		{"Missing agency separator", shapeURL("1110")},
		{"Empty ID", shapeURL("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, model := callAPIHandler[ShapeEntryResponse](t, api, tt.path)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			assert.Equal(t, http.StatusBadRequest, model.Code)
		})
	}
}

// TestShapesHandler_PolylineDecoding consolidates the looping-route,
// out-and-back, and consecutive-duplicates cases. Each subtest inserts shape
// points, fetches the encoded polyline, decodes it, and verifies the resulting
// coordinate sequence against an expected slice. Each subtest gets its own API
// so the per-subtest agency-create doesn't collide.
func TestShapesHandler_PolylineDecoding(t *testing.T) {
	tests := []struct {
		name     string
		shapeID  string
		input    []shapePoint
		expected [][2]float64 // (lat, lon) pairs in expected order after decoding
	}{
		{
			name:    "Looping route (A -> B -> A)",
			shapeID: "looping_shape",
			input: []shapePoint{
				{0.0, 0.0, 1},
				{1.0, 1.0, 2},
				{0.0, 0.0, 3},
			},
			expected: [][2]float64{{0.0, 0.0}, {1.0, 1.0}, {0.0, 0.0}},
		},
		{
			name:    "Out-and-back (A -> B -> C -> B -> A)",
			shapeID: "out_and_back_shape",
			input: []shapePoint{
				{0.0, 0.0, 1},
				{1.0, 1.0, 2},
				{2.0, 2.0, 3},
				{1.0, 1.0, 4},
				{0.0, 0.0, 5},
			},
			expected: [][2]float64{{0.0, 0.0}, {1.0, 1.0}, {2.0, 2.0}, {1.0, 1.0}, {0.0, 0.0}},
		},
		{
			name:    "Consecutive duplicates (B repeats) are deduplicated",
			shapeID: "duplicate_shape",
			input: []shapePoint{
				{0.0, 0.0, 1},
				{1.0, 1.0, 2},
				{1.0, 1.0, 3}, // duplicate of previous point — handler filters this
				{2.0, 2.0, 4},
			},
			expected: [][2]float64{{0.0, 0.0}, {1.0, 1.0}, {2.0, 2.0}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest uses its own API so the agency-create doesn't conflict.
			subAPI := createTestApi(t)
			defer subAPI.Shutdown()
			combinedShapeID := setupShapeTest(t, subAPI, tt.shapeID, tt.input)

			resp, model := callAPIHandler[ShapeEntryResponse](t, subAPI, shapeURL(combinedShapeID))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NotEmpty(t, model.Data.Entry.Points)

			decoded := decodePolylinePoints(t, model.Data.Entry.Points)
			require.Len(t, decoded, len(tt.expected))

			const tolerance = 0.00001
			for i, want := range tt.expected {
				assert.InDelta(t, want[0], decoded[i][0], tolerance, "decoded[%d].lat", i)
				assert.InDelta(t, want[1], decoded[i][1], tolerance, "decoded[%d].lon", i)
			}
		})
	}
}

// TestShapesHandlerOrdersBySequence inserts shape points out of order and
// asserts that the encoded polyline reflects ascending shape_pt_sequence —
// regression for ordering bugs in the shape query.
func TestShapesHandlerOrdersBySequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Inserted with sequences {5, 0, 2, 4, 1, 3} — handler must return them
	// in {0, 1, 2, 3, 4, 5} order.
	points := []shapePoint{
		{38.55997, -121.75855, 5},
		{38.56173, -121.76392, 0},
		{38.56211, -121.76244, 2},
		{38.56200, -121.75860, 4},
		{38.56205, -121.76288, 1},
		{38.56210, -121.75955, 3},
	}
	combinedShapeID := setupShapeTest(t, api, "unordered_shape", points)

	resp, model := callAPIHandler[ShapeEntryResponse](t, api, shapeURL(combinedShapeID))

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, len(points), model.Data.Entry.Length)
	decoded := decodePolylinePoints(t, model.Data.Entry.Points)
	require.Len(t, decoded, len(points))

	// Latitude alone is enough to verify the sequence ordering since lats are unique.
	expectedLats := []float64{38.56173, 38.56205, 38.56211, 38.56210, 38.56200, 38.55997}
	const tolerance = 0.00001
	for i, want := range expectedLats {
		assert.InDelta(t, want, decoded[i][0], tolerance, "decoded[%d].lat (sequence %d)", i, i)
	}
}
