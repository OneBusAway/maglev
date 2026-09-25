package geo

import (
	"encoding/json"
	"fmt"
	"math"
)

// GeoJSON geometry types accepted for GTFS-Flex locations.
const (
	GeoJSONPolygon      = "Polygon"
	GeoJSONMultiPolygon = "MultiPolygon"
)

// geoJSONGeometry is the subset of a GeoJSON geometry object read and written here.
type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// ParseGeoJSONPolygons decodes a Polygon or MultiPolygon geometry object into
// polygons[p][ring][vertex] = [lon, lat]. Ring 0 of each polygon is the
// exterior; later rings are holes. A third (altitude) coordinate is dropped.
func ParseGeoJSONPolygons(raw []byte) (string, [][][][2]float64, error) {
	var geometry geoJSONGeometry
	if err := json.Unmarshal(raw, &geometry); err != nil {
		return "", nil, fmt.Errorf("invalid GeoJSON geometry: %w", err)
	}

	switch geometry.Type {
	case GeoJSONPolygon:
		var rings [][][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &rings); err != nil {
			return "", nil, fmt.Errorf("invalid Polygon coordinates: %w", err)
		}
		polygons := [][][][2]float64{rings}
		if err := validatePolygons(polygons); err != nil {
			return "", nil, err
		}
		return GeoJSONPolygon, polygons, nil
	case GeoJSONMultiPolygon:
		var polygons [][][][2]float64
		if err := json.Unmarshal(geometry.Coordinates, &polygons); err != nil {
			return "", nil, fmt.Errorf("invalid MultiPolygon coordinates: %w", err)
		}
		if err := validatePolygons(polygons); err != nil {
			return "", nil, err
		}
		return GeoJSONMultiPolygon, polygons, nil
	default:
		return "", nil, fmt.Errorf("unsupported GeoJSON geometry type %q", geometry.Type)
	}
}

// validatePolygons rejects geometry with no area to contain: no polygons, a
// polygon without rings, or an exterior ring too short to close.
func validatePolygons(polygons [][][][2]float64) error {
	if len(polygons) == 0 {
		return fmt.Errorf("GeoJSON geometry has no polygons")
	}
	for polygonIndex, polygon := range polygons {
		if len(polygon) == 0 {
			return fmt.Errorf("polygon %d has no rings", polygonIndex)
		}
		if len(polygon[0]) < minClosedRingPoints {
			return fmt.Errorf("polygon %d exterior ring has %d positions, need at least %d",
				polygonIndex, len(polygon[0]), minClosedRingPoints)
		}
	}
	return nil
}

// EncodeGeoJSONGeometry is the inverse of ParseGeoJSONPolygons.
func EncodeGeoJSONGeometry(geometryType string, polygons [][][][2]float64) ([]byte, error) {
	var coordinates any
	switch geometryType {
	case GeoJSONPolygon:
		if len(polygons) != 1 {
			return nil, fmt.Errorf("a Polygon holds exactly one polygon, got %d", len(polygons))
		}
		coordinates = polygons[0]
	case GeoJSONMultiPolygon:
		coordinates = polygons
	default:
		return nil, fmt.Errorf("unsupported GeoJSON geometry type %q", geometryType)
	}

	return json.Marshal(struct {
		Type        string `json:"type"`
		Coordinates any    `json:"coordinates"`
	}{Type: geometryType, Coordinates: coordinates})
}

// PolygonsBounds returns the bounding box over every vertex of every ring.
// Rings straddling the antimeridian are not handled.
func PolygonsBounds(polygons [][][][2]float64) CoordinateBounds {
	bounds := CoordinateBounds{MinLat: math.Inf(1), MaxLat: math.Inf(-1), MinLon: math.Inf(1), MaxLon: math.Inf(-1)}
	for _, polygon := range polygons {
		for _, ring := range polygon {
			for _, vertex := range ring {
				bounds.MinLon = math.Min(bounds.MinLon, vertex[0])
				bounds.MaxLon = math.Max(bounds.MaxLon, vertex[0])
				bounds.MinLat = math.Min(bounds.MinLat, vertex[1])
				bounds.MaxLat = math.Max(bounds.MaxLat, vertex[1])
			}
		}
	}
	return bounds
}
