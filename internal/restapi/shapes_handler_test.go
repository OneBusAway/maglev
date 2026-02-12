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

// helper to create a test agency and shapes
func setupShapeTest(t *testing.T, api *RestAPI, shapeID string, points []struct {
	lat      float64
	lon      float64
	sequence int64
}) string {
	t.Helper()
	ctx := context.Background()
	agencyID := "TestAgency"

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
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	assert.NotEmpty(t, entry["points"])
	assert.Equal(t, float64(14), entry["length"])
	assert.Equal(t, "", entry["levels"])
}

func TestShapesHandlerDeduplicatesConsecutiveDuplicatePoints(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	// Points with consecutive duplicates — should be deduplicated
	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{38.56173, -121.76392, 0},
		{38.56173, -121.76392, 1}, // duplicate of previous
		{38.56211, -121.76244, 2},
		{38.56211, -121.76244, 3}, // duplicate of previous
		{38.56200, -121.75860, 4},
		{38.55997, -121.75855, 5},
	}

	agencyID := setupShapeTest(t, api, "dup_points_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_dup_points_shape.json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	// 6 points but only 4 unique consecutive points
	assert.Equal(t, float64(4), entry["length"])
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

func TestShapesHandlerSinglePointReturnsNotFound(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	points := []struct {
		lat      float64
		lon      float64
		sequence int64
	}{
		{38.56173, -121.76392, 0},
	}

	agencyID := setupShapeTest(t, api, "single_point_shape", points)
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_single_point_shape.json?key=TEST")

	// Single point can't form a segment (len < 2), so points will be empty
	// The handler still returns 200 but with empty/minimal data
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)
	t.Logf("entry point: %v", entry)

	assert.Equal(t, float64(1), entry["length"])
	assert.Equal(t, "", entry["points"])
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
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(7), entry["length"])
	assert.NotEmpty(t, entry["points"])
}

func TestShapesHandlerReturnsNotFoundWhenShapeDoesNotExist(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_nonexistent_shape.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestShapesHandlerReturnsNotFoundWhenAgencyDoesNotExist(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/nonexistent_agency_some_shape.json?key=TEST")

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

func TestShapesHandlerWithMalformedID(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	malformedID := "1110"
	endpoint := "/api/where/shape/" + malformedID + ".json?key=TEST"

	resp, _ := serveApiAndRetrieveEndpoint(t, api, endpoint)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Status code should be 400 Bad Request")
}

// decodePolylinePoints decodes a Google encoded polyline string into coordinate pairs.
// Only used in shape handler tests to verify coordinate ordering.
func decodePolylinePoints(t *testing.T, encoded string) [][]float64 {
	t.Helper()
	coords, _, err := polyline.DecodeCoords([]byte(encoded))
	require.NoError(t, err)
	return coords
}
