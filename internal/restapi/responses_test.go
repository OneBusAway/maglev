package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/models"
)

func TestSendResponse(t *testing.T) {
	api := createTestApi(t)

	t.Run("sends valid JSON response", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		response := models.ResponseModel{
			Code:        http.StatusOK,
			CurrentTime: 1234567890,
			Text:        "OK",
			Version:     2,
			Data:        map[string]string{"test": "data"},
		}

		api.sendResponse(w, r, response)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var decoded models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&decoded)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, decoded.Code)
		assert.Equal(t, "OK", decoded.Text)
		assert.Equal(t, 2, decoded.Version)
	})

	t.Run("sends response with nil data", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		response := models.ResponseModel{
			Code:        http.StatusNoContent,
			CurrentTime: 1234567890,
			Text:        "No Content",
			Version:     2,
			Data:        nil,
		}

		api.sendResponse(w, r, response)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var decoded models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&decoded)
		require.NoError(t, err)

		assert.Equal(t, http.StatusNoContent, decoded.Code)
		assert.Equal(t, "No Content", decoded.Text)
	})
}

func TestSendNull(t *testing.T) {
	api := createTestApi(t)

	t.Run("sends null response", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		api.sendNull(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.Equal(t, "null", w.Body.String())
	})
}

func TestSendNotFound(t *testing.T) {
	api := createTestApi(t)

	t.Run("sends 404 not found response", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		api.sendNotFound(w, r)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.Equal(t, "resource not found", response.Text)
		assert.Equal(t, 2, response.Version)
	})

	t.Run("verifies response structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		api.sendNotFound(w, r)

		var response models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		assert.Greater(t, response.CurrentTime, int64(0), "CurrentTime should be set")
		assert.Nil(t, response.Data, "Data should be nil for not found")
	})
}

func TestSendUnauthorized(t *testing.T) {
	api := createTestApi(t)

	t.Run("sends 401 unauthorized response", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		api.sendUnauthorized(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Equal(t, "permission denied", response.Text)
		assert.Equal(t, 1, response.Version)
	})

	t.Run("verifies response structure", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)

		api.sendUnauthorized(w, r)

		var response models.ResponseModel
		err := json.NewDecoder(w.Body).Decode(&response)
		require.NoError(t, err)

		assert.Greater(t, response.CurrentTime, int64(0), "CurrentTime should be set")
		assert.Nil(t, response.Data, "Data should be nil for unauthorized")
	})
}

func TestSetJSONResponseType(t *testing.T) {
	t.Run("sets JSON content type header", func(t *testing.T) {
		w := httptest.NewRecorder()
		var wInterface http.ResponseWriter = w

		setJSONResponseType(&wInterface)

		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})

	t.Run("overwrites existing content type", func(t *testing.T) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "text/html")
		var wInterface http.ResponseWriter = w

		setJSONResponseType(&wInterface)

		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	})
}
