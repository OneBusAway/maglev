package restapi

import (
	"encoding/json"
	"net/http"

	"maglev.onebusaway.org/internal/models"
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
		CurrentTime: models.ResponseCurrentTimeWithClock(api.Clock),
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
		CurrentTime: models.ResponseCurrentTimeWithClock(api.Clock),
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

func (api *RestAPI) sendError(w http.ResponseWriter, r *http.Request, code int, message string) {
	setJSONResponseType(&w)
	w.WriteHeader(code)

	response := models.ResponseModel{
		Code:        code,
		CurrentTime: models.ResponseCurrentTime(),
		Text:        message,
		Version:     2,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		api.serverErrorResponse(w, r, err)
	}
}