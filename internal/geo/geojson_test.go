package geo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGeoJSONPolygons(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantType     string
		wantPolygons int
		wantRings    int
		wantErr      bool
	}{
		{
			name:         "polygon with hole",
			raw:          `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1],[0,0]],[[0.2,0.2],[0.2,0.4],[0.4,0.4],[0.2,0.2]]]}`,
			wantType:     GeoJSONPolygon,
			wantPolygons: 1,
			wantRings:    2,
		},
		{
			name:         "multipolygon",
			raw:          `{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]],[[[2,2],[3,2],[3,3],[2,2]]]]}`,
			wantType:     GeoJSONMultiPolygon,
			wantPolygons: 2,
			wantRings:    1,
		},
		{
			name:         "altitude is dropped",
			raw:          `{"type":"Polygon","coordinates":[[[0,0,5],[1,0,5],[1,1,5],[0,0,5]]]}`,
			wantType:     GeoJSONPolygon,
			wantPolygons: 1,
			wantRings:    1,
		},
		{name: "point is rejected", raw: `{"type":"Point","coordinates":[0,0]}`, wantErr: true},
		{name: "malformed json", raw: `{"type":"Polygon"`, wantErr: true},
		{name: "wrong coordinate depth", raw: `{"type":"Polygon","coordinates":[[0,0],[1,1]]}`, wantErr: true},
		{name: "polygon without rings", raw: `{"type":"Polygon","coordinates":[]}`, wantErr: true},
		{name: "polygon with null coordinates", raw: `{"type":"Polygon","coordinates":null}`, wantErr: true},
		{name: "multipolygon without polygons", raw: `{"type":"MultiPolygon","coordinates":[]}`, wantErr: true},
		{name: "multipolygon with a ringless polygon", raw: `{"type":"MultiPolygon","coordinates":[[]]}`, wantErr: true},
		{name: "exterior ring below four positions", raw: `{"type":"Polygon","coordinates":[[[0,0],[1,0],[0,0]]]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geometryType, polygons, err := ParseGeoJSONPolygons([]byte(tt.raw))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, geometryType)
			require.Len(t, polygons, tt.wantPolygons)
			assert.Len(t, polygons[0], tt.wantRings)
			assert.Equal(t, [2]float64{0, 0}, polygons[0][0][0])
		})
	}
}

func TestEncodeGeoJSONGeometry_RoundTrips(t *testing.T) {
	raw := `{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]],[[[2,2],[3,2],[3,3],[2,2]]]]}`
	geometryType, polygons, err := ParseGeoJSONPolygons([]byte(raw))
	require.NoError(t, err)

	encoded, err := EncodeGeoJSONGeometry(geometryType, polygons)
	require.NoError(t, err)
	assert.JSONEq(t, raw, string(encoded))

	_, err = EncodeGeoJSONGeometry(GeoJSONPolygon, polygons)
	assert.Error(t, err, "a Polygon must hold exactly one polygon")
	_, err = EncodeGeoJSONGeometry("Point", polygons)
	assert.Error(t, err)
}

func TestPolygonsBounds(t *testing.T) {
	polygons := [][][][2]float64{
		{{{-77.5, 38.6}, {-76.9, 38.6}, {-76.9, 39.05}, {-77.5, 39.05}, {-77.5, 38.6}}},
		{{{-77.6, 38.9}, {-77.55, 38.9}, {-77.55, 38.95}, {-77.6, 38.9}}},
	}
	bounds := PolygonsBounds(polygons)
	assert.Equal(t, CoordinateBounds{MinLat: 38.6, MaxLat: 39.05, MinLon: -77.6, MaxLon: -76.9}, bounds)
}
