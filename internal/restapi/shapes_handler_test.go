package restapi

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/gtfsdb"
)

// setupShapeTest creates a test agency and inserts shape points into the database.
// Returns the agency ID for use in API endpoint URLs.
func setupShapeTest(t *testing.T, api *RestAPI, shapeID string, points []struct {
	lat      float64
	lon      float64
	sequence int64
}) string {
	t.Helper()
	ctx := context.Background()
	agencyID := "TestAgency1"

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

	return agencyID
}

// decodePolylinePoints decodes a Google encoded polyline string into coordinate pairs.
// Only used in shape handler tests to verify coordinate ordering.
func decodePolylinePoints(t *testing.T, encoded string) [][]float64 {
	t.Helper()
	coords, _, err := polyline.DecodeCoords([]byte(encoded))
	require.NoError(t, err)
	return coords
}

func TestShapesHandlerReturnsShapeWhenItExists(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{38.56173, -121.76392, 0},
		{38.56205, -121.76288, 1},
		{38.56211, -121.76244, 2},
		{38.56210, -121.75955, 3},
		{38.56200, -121.75860, 4},
		{38.55997, -121.75855, 5},
		{38.55672, -121.75857, 6},
		{38.55385, -121.75864, 7},
		{38.55227, -121.75866, 8},
		{38.54638, -121.75867, 9},
		{38.54617, -121.75078, 10},
		{38.54398, -121.75017, 11},
		{38.54405, -121.74970, 12},
		{38.54363, -121.74957, 13},
	}

	agencyID := setupShapeTest(t, api, "simple_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_simple_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	// Verify shape entry has expected fields
	assert.NotEmpty(t, entry["points"])
	assert.Equal(t, float64(14), entry["length"])
	assert.Equal(t, "", entry["levels"])
}

func TestShapesHandlerReturnsNotFoundWhenShapeDoesNotExist(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/wrong_id.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestShapesHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_any_shape.json?key=INVALID")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}

func TestShapesHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/shape/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

func TestShapesHandlerWithLoopingRoute(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A -> B -> A (loop back to start)
	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{0.0, 0.0, 1},
		{1.0, 1.0, 2},
		{0.0, 0.0, 3},
	}

	agencyID := setupShapeTest(t, api, "looping_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_looping_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")
	assert.NotEmpty(t, encodedPoints)

	decoded := decodePolylinePoints(t, encodedPoints)
	require.Equal(t, 3, len(decoded), "Should have 3 decoded points")

	tolerance := 0.00001
	assert.InDelta(t, 0.0, decoded[0][0], tolerance, "First point latitude should be 0.0")
	assert.InDelta(t, 0.0, decoded[0][1], tolerance, "First point longitude should be 0.0")
	assert.InDelta(t, 1.0, decoded[1][0], tolerance, "Second point latitude should be 1.0")
	assert.InDelta(t, 1.0, decoded[1][1], tolerance, "Second point longitude should be 1.0")
	assert.InDelta(t, 0.0, decoded[2][0], tolerance, "Third point latitude should be 0.0 (loop)")
	assert.InDelta(t, 0.0, decoded[2][1], tolerance, "Third point longitude should be 0.0 (loop)")
}

func TestShapesHandlerWithOutAndBackRoute(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A -> B -> C -> B -> A
	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{0.0, 0.0, 1},
		{1.0, 1.0, 2},
		{2.0, 2.0, 3},
		{1.0, 1.0, 4},
		{0.0, 0.0, 5},
	}

	agencyID := setupShapeTest(t, api, "out_and_back_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_out_and_back_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")
	assert.NotEmpty(t, encodedPoints)

	decoded := decodePolylinePoints(t, encodedPoints)
	require.Equal(t, 5, len(decoded))

	tolerance := 0.00001
	assert.InDelta(t, 0.0, decoded[0][0], tolerance)
	assert.InDelta(t, 0.0, decoded[0][1], tolerance)
	assert.InDelta(t, 1.0, decoded[1][0], tolerance)
	assert.InDelta(t, 1.0, decoded[1][1], tolerance)
	assert.InDelta(t, 2.0, decoded[2][0], tolerance)
	assert.InDelta(t, 2.0, decoded[2][1], tolerance)
	assert.InDelta(t, 1.0, decoded[3][0], tolerance)
	assert.InDelta(t, 1.0, decoded[3][1], tolerance)
	assert.InDelta(t, 0.0, decoded[4][0], tolerance)
	assert.InDelta(t, 0.0, decoded[4][1], tolerance)
}

func TestShapesHandlerWithConsecutiveDuplicatePoints(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// A -> B -> B (duplicate) -> C
	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{0.0, 0.0, 1},
		{1.0, 1.0, 2},
		{1.0, 1.0, 3}, // Duplicate of previous point
		{2.0, 2.0, 4},
	}

	agencyID := setupShapeTest(t, api, "duplicate_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_duplicate_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")
	assert.NotEmpty(t, encodedPoints)

	decoded := decodePolylinePoints(t, encodedPoints)
	// Should have filtered out the duplicate point
	require.Equal(t, 3, len(decoded))

	tolerance := 0.00001
	assert.InDelta(t, 0.0, decoded[0][0], tolerance)
	assert.InDelta(t, 0.0, decoded[0][1], tolerance)
	assert.InDelta(t, 1.0, decoded[1][0], tolerance)
	assert.InDelta(t, 1.0, decoded[1][1], tolerance)
	assert.InDelta(t, 2.0, decoded[2][0], tolerance)
	assert.InDelta(t, 2.0, decoded[2][1], tolerance)
}

func TestShapesHandlerWithMissingApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_any_shape.json")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
}

func TestShapesHandlerWithEmptyID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/.json?key=TEST")

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, model.Code)
}

func TestShapesHandlerLengthMatchesDecodedPoints(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{38.56173, -121.76392, 0},
		{38.56205, -121.76288, 1},
		{38.56211, -121.76244, 2},
		{38.56210, -121.75955, 3},
		{38.56200, -121.75860, 4},
		{38.55997, -121.75855, 5},
		{38.55672, -121.75857, 6},
	}

	agencyID := setupShapeTest(t, api, "length_check_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_length_check_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	assert.Equal(t, float64(7), entry["length"])
	assert.NotEmpty(t, entry["points"])
	assert.Equal(t, "", entry["levels"])
}

func TestShapesHandlerOrdersBySequence(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Insert points out of sequence order
	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{38.55997, -121.75855, 5},
		{38.56173, -121.76392, 0},
		{38.56211, -121.76244, 2},
		{38.56200, -121.75860, 4},
		{38.56205, -121.76288, 1},
		{38.56210, -121.75955, 3},
	}

	agencyID := setupShapeTest(t, api, "unordered_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_unordered_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(6), entry["length"])

	points_str, ok := entry["points"].(string)
	require.True(t, ok)

	decoded := decodePolylinePoints(t, points_str)
	require.Len(t, decoded, 6)

	assert.InDelta(t, 38.56173, decoded[0][0], 0.0001)
	assert.InDelta(t, 38.56205, decoded[1][0], 0.0001)
	assert.InDelta(t, 38.56211, decoded[2][0], 0.0001)
	assert.InDelta(t, 38.56210, decoded[3][0], 0.0001)
	assert.InDelta(t, 38.56200, decoded[4][0], 0.0001)
	assert.InDelta(t, 38.55997, decoded[5][0], 0.0001)
}
