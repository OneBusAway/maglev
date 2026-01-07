package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
)

// Declare a handler which writes a JSON response with information about the
// current time.
func (api *RestAPI) currentTimeHandler(w http.ResponseWriter, r *http.Request) {
	timeData := models.NewCurrentTimeData(api.Clock.Now())
	response := models.NewOKResponse(timeData, api.Clock)

	api.sendResponse(w, r, response)
}
