package restapi

import (
	"encoding/json"
	"maglev.onebusaway.org/internal/models"
	"net/http"
)

func (api *RestAPI) sendResponse(w http.ResponseWriter, r *http.Request, response models.ResponseModel) {
	setJSONResponseType(&w)
	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
}

func (api *RestAPI) sendNull(w http.ResponseWriter, r *http.Request) { // nolint:unused
	setJSONResponseType(&w)
	_, err := w.Write([]byte("null"))
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
}

func (api *RestAPI) sendNotFound(w http.ResponseWriter, r *http.Request) {
	setJSONResponseType(&w)
	w.WriteHeader(http.StatusNotFound)

	response := models.ResponseModel{
		Code:        http.StatusNotFound,
		CurrentTime: models.ResponseCurrentTime(),
		Text:        "resource not found",
		Version:     2,
	}

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
}

func (api *RestAPI) sendUnauthorized(w http.ResponseWriter, r *http.Request) { // nolint:unused
	setJSONResponseType(&w)
	w.WriteHeader(http.StatusUnauthorized)

	response := models.ResponseModel{
		Code:        http.StatusUnauthorized,
		CurrentTime: models.ResponseCurrentTime(),
		Text:        "permission denied",
		Version:     1,
	}

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
}

func setJSONResponseType(w *http.ResponseWriter) {
	(*w).Header().Set("Content-Type", "application/json")
}

func (api *RestAPI) sendBadRequest(w http.ResponseWriter, r *http.Request, text string) {
	setJSONResponseType(&w)
	w.WriteHeader(http.StatusBadRequest)

	response := models.ResponseModel{
		Code:        http.StatusBadRequest,
		CurrentTime: models.ResponseCurrentTime(),
		Text:        text,
		Version:     2,
	}

	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
}