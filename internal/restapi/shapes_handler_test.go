package restapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.True(t, ok)

	entry, ok := data["entry"].(map[string]interface{})
	require.True(t, ok)

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
