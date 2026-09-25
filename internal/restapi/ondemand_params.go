package restapi

import (
	"net/url"

	"maglev.onebusaway.org/internal/utils"
)

// GeometryDetail selects how much zone geometry an /ondemand response embeds.
type GeometryDetail string

const (
	GeometryDetailNone       GeometryDetail = "none"
	GeometryDetailSimplified GeometryDetail = "simplified"
	GeometryDetailFull       GeometryDetail = "full"
)

const geometryDetailParam = "geometryDetail"

// geometryDetailValues are the accepted values of the geometryDetail parameter.
var geometryDetailValues = []GeometryDetail{GeometryDetailNone, GeometryDetailSimplified, GeometryDetailFull}

// parseGeometryDetail reads the tri-state geometryDetail parameter, returning
// fallback when absent and recording a field error for any other value.
func parseGeometryDetail(params url.Values, fallback GeometryDetail, fieldErrors map[string][]string) (GeometryDetail, map[string][]string) {
	return utils.ParseEnumParam(params, geometryDetailParam, geometryDetailValues, fallback, fieldErrors)
}
