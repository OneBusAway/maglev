package restapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/restapi/testdata"
)

func TestAgencyHandlerReturnsAgencyWhenItExists(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[AgencyEntryResponse](t, api, "/api/where/agency/"+testdata.Raba.ID+".json?key=TEST")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, http.StatusOK, model.Code)
	assert.Equal(t, "OK", model.Text)
	assert.Equal(t, testdata.Raba, model.Data.Entry)
}

func TestAgencyHandlerReturnsNullWhenAgencyDoesNotExist(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[AgencyEntryResponse](t, api, "/api/where/agency/non-existent-id.json?key=TEST")

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, model.Code)
	assert.Equal(t, "resource not found", model.Text)
	assert.Equal(t, EntryData[models.AgencyReference]{}, model.Data)
}

func TestAgencyHandlerRequiresValidApiKey(t *testing.T) {
	api := createTestApi(t)
	defer api.Shutdown()

	resp, model := callAPIHandler[AgencyEntryResponse](t, api, "/api/where/agency/"+testdata.Raba.ID+".json?key=INVALID")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, model.Code)
	assert.Equal(t, "permission denied", model.Text)
}
