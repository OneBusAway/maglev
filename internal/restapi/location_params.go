package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) parseLocationParams(r *http.Request, fieldErrors map[string][]string) (*gtfs.LocationParams, map[string][]string) {
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

	if radius > models.MaxSearchRadiusInMeters {
		radius = models.MaxSearchRadiusInMeters
	}
	if latSpan > models.MaxSearchSpanInDegrees {
		latSpan = models.MaxSearchSpanInDegrees
	}
	if lonSpan > models.MaxSearchSpanInDegrees {
		lonSpan = models.MaxSearchSpanInDegrees
	}

	return &gtfs.LocationParams{
		Lat:     lat,
		Lon:     lon,
		Radius:  radius,
		LatSpan: latSpan,
		LonSpan: lonSpan,
	}, nil
}
