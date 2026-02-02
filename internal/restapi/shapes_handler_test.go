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

func TestShapesHandlerReturnsShapeWhenItExists(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	shapes, err := api.GtfsManager.GtfsDB.Queries.GetAllShapes(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, shapes)

	shapeID := shapes[0].ShapeID
	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_"+shapeID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	// Verify shape entry has expected fields
	assert.NotEmpty(t, entry["points"])
	assert.NotEmpty(t, entry["length"], 0)
	assert.Equal(t, "", entry["levels"])
	// Verify shape entry has expected values
	assert.Equal(t, entry["points"], "eifvFbvmiVsC?MBWPMNIRCNAxExGAAzFDvKJ^?vQElDYlDo@bDq@rBw@bB_CnEq@q@EDc@g@FOBSAOIUIIMCa@@QJEP")
	assert.Equal(t, entry["length"], 91.0)
}

func TestShapesHandlerReturnsNullWhenShapeDoesNotExist(t *testing.T) {
	_, resp, model := serveAndRetrieveEndpoint(t, "/api/where/shape/wrong_id.json?key=TEST")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Nil(t, model.Data)
}

func TestShapesHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	shapes, err := api.GtfsManager.GtfsDB.Queries.GetAllShapes(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, shapes)

	shapeID := shapes[0].ShapeID
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/raba_"+shapeID+".json?key=INVALID")

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

	ctx := context.Background()

	shapeID := "looping_shape"

	_, err := api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
		ShapeID:           shapeID,
		Lat:               0.0,
		Lon:               0.0,
		ShapePtSequence:   1,
		ShapeDistTraveled: sql.NullFloat64{Float64: 0.0, Valid: true},
	})
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
		ShapeID:           shapeID,
		Lat:               1.0,
		Lon:               1.0,
		ShapePtSequence:   2,
		ShapeDistTraveled: sql.NullFloat64{Float64: 100.0, Valid: true},
	})
	require.NoError(t, err)

	_, err = api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
		ShapeID:           shapeID,
		Lat:               0.0,
		Lon:               0.0,
		ShapePtSequence:   3,
		ShapeDistTraveled: sql.NullFloat64{Float64: 200.0, Valid: true},
	})
	require.NoError(t, err)

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_"+shapeID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")

	assert.NotEmpty(t, encodedPoints, "Encoded polyline should not be empty")

	decodedCoords, _, err := polyline.DecodeCoords([]byte(encodedPoints))
	require.NoError(t, err, "Encoded polyline should decode without errors")

	require.Equal(t, 3, len(decodedCoords), "Should have 3 decoded points")

	tolerance := 0.00001

	assert.InDelta(t, 0.0, decodedCoords[0][0], tolerance, "First point latitude should be 0.0")
	assert.InDelta(t, 0.0, decodedCoords[0][1], tolerance, "First point longitude should be 0.0")

	assert.InDelta(t, 1.0, decodedCoords[1][0], tolerance, "Second point latitude should be 1.0")
	assert.InDelta(t, 1.0, decodedCoords[1][1], tolerance, "Second point longitude should be 1.0")

	assert.InDelta(t, 0.0, decodedCoords[2][0], tolerance, "Third point latitude should be 0.0 (loop)")
	assert.InDelta(t, 0.0, decodedCoords[2][1], tolerance, "Third point longitude should be 0.0 (loop)")
}

func TestShapesHandlerWithOutAndBackRoute(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	shapeID := "out_and_back_shape"

	// Create points: A -> B -> C -> B -> A
	// (0,0) -> (1,1) -> (2,2) -> (1,1) -> (0,0)
	points := []struct {
		lat float64
		lon float64
		seq int64
	}{
		{0.0, 0.0, 1},
		{1.0, 1.0, 2},
		{2.0, 2.0, 3},
		{1.0, 1.0, 4},
		{0.0, 0.0, 5},
	}

	for _, p := range points {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
			ShapeID:           shapeID,
			Lat:               p.lat,
			Lon:               p.lon,
			ShapePtSequence:   p.seq,
			ShapeDistTraveled: sql.NullFloat64{Valid: true, Float64: float64(p.seq * 100)},
		})
		require.NoError(t, err)
	}

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_"+shapeID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")

	assert.NotEmpty(t, encodedPoints)

	decodedCoords, _, err := polyline.DecodeCoords([]byte(encodedPoints))
	require.NoError(t, err)
	require.Equal(t, 5, len(decodedCoords))

	tolerance := 0.00001
	// Verify A
	assert.InDelta(t, 0.0, decodedCoords[0][0], tolerance)
	assert.InDelta(t, 0.0, decodedCoords[0][1], tolerance) // Verify Longitude
	// Verify B
	assert.InDelta(t, 1.0, decodedCoords[1][0], tolerance)
	assert.InDelta(t, 1.0, decodedCoords[1][1], tolerance) // Verify Longitude
	// Verify C
	assert.InDelta(t, 2.0, decodedCoords[2][0], tolerance)
	assert.InDelta(t, 2.0, decodedCoords[2][1], tolerance) // Verify Longitude
	// Verify B (Return)
	assert.InDelta(t, 1.0, decodedCoords[3][0], tolerance)
	assert.InDelta(t, 1.0, decodedCoords[3][1], tolerance) // Verify Longitude
	// Verify A (Return)
	assert.InDelta(t, 0.0, decodedCoords[4][0], tolerance)
	assert.InDelta(t, 0.0, decodedCoords[4][1], tolerance) // Verify Longitude
}

func TestShapesHandlerWithConsecutiveDuplicatePoints(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	ctx := context.Background()
	shapeID := "duplicate_shape"

	// Create points: A -> B -> B -> C
	// (0,0) -> (1,1) -> (1,1) -> (2,2)
	points := []struct {
		lat float64
		lon float64
		seq int64
	}{
		{0.0, 0.0, 1},
		{1.0, 1.0, 2},
		{1.0, 1.0, 3}, // Duplicate of previous point
		{2.0, 2.0, 4},
	}

	for _, p := range points {
		_, err := api.GtfsManager.GtfsDB.Queries.CreateShape(ctx, gtfsdb.CreateShapeParams{
			ShapeID:           shapeID,
			Lat:               p.lat,
			Lon:               p.lon,
			ShapePtSequence:   p.seq,
			ShapeDistTraveled: sql.NullFloat64{Valid: true, Float64: float64(p.seq * 100)},
		})
		require.NoError(t, err)
	}

	agencyID := api.GtfsManager.GetAgencies()[0].Id
	resp, model := serveApiAndRetrieveEndpoint(t, api, "/api/where/shape/"+agencyID+"_"+shapeID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	data, ok := model.Data.(map[string]interface{})
	require.True(t, ok, "model.Data should be a map")

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok, "entry should be a map")

	encodedPoints, ok := entry["points"].(string)
	require.True(t, ok, "points should be a string")

	assert.NotEmpty(t, encodedPoints)

	decodedCoords, _, err := polyline.DecodeCoords([]byte(encodedPoints))
	require.NoError(t, err)
	
	// Should have filtered out the duplicate point, so length should be 3
	require.Equal(t, 3, len(decodedCoords))

	tolerance := 0.00001
	// Verify A (0,0)
	assert.InDelta(t, 0.0, decodedCoords[0][0], tolerance)
	assert.InDelta(t, 0.0, decodedCoords[0][1], tolerance)
	// Verify B (1,1)
	assert.InDelta(t, 1.0, decodedCoords[1][0], tolerance)
	assert.InDelta(t, 1.0, decodedCoords[1][1], tolerance)
	// Verify C (2,2)
	assert.InDelta(t, 2.0, decodedCoords[2][0], tolerance)
	assert.InDelta(t, 2.0, decodedCoords[2][1], tolerance)
}
