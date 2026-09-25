package restapi

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGeometryDetail(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		fallback   GeometryDetail
		want       GeometryDetail
		wantErrors bool
	}{
		{"absent uses the fallback", "", GeometryDetailFull, GeometryDetailFull, false},
		{"none", "geometryDetail=none", GeometryDetailFull, GeometryDetailNone, false},
		{"simplified", "geometryDetail=simplified", GeometryDetailFull, GeometryDetailSimplified, false},
		{"full", "geometryDetail=full", GeometryDetailSimplified, GeometryDetailFull, false},
		{"invalid records a field error", "geometryDetail=bbox", GeometryDetailFull, GeometryDetailFull, true},
		{"case sensitive", "geometryDetail=Full", GeometryDetailFull, GeometryDetailFull, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, _ := url.ParseQuery(tt.query)
			got, fieldErrors := parseGeometryDetail(params, tt.fallback, nil)
			assert.Equal(t, tt.want, got)
			if tt.wantErrors {
				assert.Equal(t, []string{`Invalid field value for field "geometryDetail".`}, fieldErrors["geometryDetail"])
			} else {
				assert.Empty(t, fieldErrors)
			}
		})
	}
}
