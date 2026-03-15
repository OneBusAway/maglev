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

func (api *RestAPI) parseLocationParams(r *http.Request, fieldErrors map[string][]string) (*LocationParams, map[string][]string) {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseRequiredFloatParam(queryParams, "lat", fieldErrors)
	lon, fieldErrors := utils.ParseRequiredFloatParam(queryParams, "lon", fieldErrors)
	radius, fieldErrors := utils.ParseFloatParam(queryParams, "radius", fieldErrors)
	latSpan, fieldErrors := utils.ParseFloatParam(queryParams, "latSpan", fieldErrors)
	lonSpan, fieldErrors := utils.ParseFloatParam(queryParams, "lonSpan", fieldErrors)

	if len(fieldErrors) > 0 {
		return nil, fieldErrors
	}

	locationErrors := utils.ValidateLocationParams(lat, lon, radius, latSpan, lonSpan)
	if len(locationErrors) > 0 {
		if fieldErrors == nil {
			fieldErrors = make(map[string][]string)
		}
		for k, v := range locationErrors {
			fieldErrors[k] = append(fieldErrors[k], v...)
		}
		return nil, fieldErrors
	}

	return &LocationParams{
		Lat:     lat,
		Lon:     lon,
		Radius:  radius,
		LatSpan: latSpan,
		LonSpan: lonSpan,
	}, nil
}
