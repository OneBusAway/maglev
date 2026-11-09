package restapi

import (
	"net/http"
	"net/url"

	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) parseLocationParams(r *http.Request, fieldErrors map[string][]string) (*gtfs.LocationParams, map[string][]string) {
	queryParams := r.URL.Query()

	lat, fieldErrors := utils.ParseFloatParam(queryParams, "lat", fieldErrors)
	lon, fieldErrors := utils.ParseFloatParam(queryParams, "lon", fieldErrors)
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

	return &gtfs.LocationParams{
		Lat:     lat,
		Lon:     lon,
		Radius:  radius,
		LatSpan: latSpan,
		LonSpan: lonSpan,
	}, nil
}

// validateUserCoordinates validates that userLat and userLon are within valid geographic
// ranges (latitude [-90, 90] and longitude [-180, 180]) when present.
func validateUserCoordinates(query url.Values, userLat, userLon float64, fieldErrors map[string][]string) map[string][]string {
	if query.Get("userLat") != "" && len(fieldErrors["userLat"]) == 0 {
		if err := utils.ValidateLatitude(userLat); err != nil {
			if fieldErrors == nil {
				fieldErrors = make(map[string][]string)
			}
			fieldErrors["userLat"] = append(fieldErrors["userLat"], err.Error())
		}
	}
	if query.Get("userLon") != "" && len(fieldErrors["userLon"]) == 0 {
		if err := utils.ValidateLongitude(userLon); err != nil {
			if fieldErrors == nil {
				fieldErrors = make(map[string][]string)
			}
			fieldErrors["userLon"] = append(fieldErrors["userLon"], err.Error())
		}
	}
	return fieldErrors
}
