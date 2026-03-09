package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/utils"
)

type LocationParams struct {
	Lat     float64
	Lon     float64
	Radius  float64
	LatSpan float64
	LonSpan float64
}

func (api *RestAPI) parseLocationParams(w http.ResponseWriter, r *http.Request) *LocationParams {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseRequiredFloatParam(queryParams, "lat", nil)
	lon, _ := utils.ParseRequiredFloatParam(queryParams, "lon", fieldErrors)
	radius, _ := utils.ParseFloatParam(queryParams, "radius", fieldErrors)
	latSpan, _ := utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, _ := utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)

	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return nil
	}

	locationErrors := utils.ValidateLocationParams(lat, lon, radius, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		api.validationErrorResponse(w, r, locationErrors)
		return nil
	}

	return &LocationParams{
		Lat:     lat,
		Lon:     lon,
		Radius:  radius,
		LatSpan: latSpan,
		LonSpan: lonSpan,
	}
}
