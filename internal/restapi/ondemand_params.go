package restapi

import "net/url"

// GeometryDetail selects how much zone geometry an /ondemand response embeds.
type GeometryDetail string

const (
	GeometryDetailNone       GeometryDetail = "none"
	GeometryDetailSimplified GeometryDetail = "simplified"
	GeometryDetailFull       GeometryDetail = "full"
)

const geometryDetailParam = "geometryDetail"

// parseGeometryDetail reads the tri-state geometryDetail parameter, returning
// fallback when absent and recording a field error for any other value.
func parseGeometryDetail(params url.Values, fallback GeometryDetail, fieldErrors map[string][]string) (GeometryDetail, map[string][]string) {
	if fieldErrors == nil {
		fieldErrors = make(map[string][]string)
	}
	value := params.Get(geometryDetailParam)
	switch GeometryDetail(value) {
	case "":
		return fallback, fieldErrors
	case GeometryDetailNone, GeometryDetailSimplified, GeometryDetailFull:
		return GeometryDetail(value), fieldErrors
	default:
		fieldErrors[geometryDetailParam] = append(fieldErrors[geometryDetailParam], `Invalid field value for field "geometryDetail".`)
		return fallback, fieldErrors
	}
}
