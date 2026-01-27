package restapi

import (
	"net/http"
	"strings"

	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) shapesHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, shapeID, err := utils.ExtractAgencyIDAndCodeID(utils.ExtractIDFromParams(r))

	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()

	_, err = api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	shapes, err := api.GtfsManager.GtfsDB.Queries.GetShapeByID(ctx, shapeID)

	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	if len(shapes) == 0 {
		api.sendNotFound(w, r)
		return
	}

	var prev = gtfsdb.Shape{
		Lat: -1,
		Lon: -1,
	}
	var polylines []string
	var currentLine [][]float64
	edges := make(map[models.Edge]bool)

	for _, point := range shapes {
		loc := []float64{point.Lat, point.Lon}

		if prev.Lat != -1 && (prev.Lat != point.Lat || prev.Lon != point.Lon) {
			prevPoint := models.CoordinatePoint{Lat: prev.Lat, Lon: prev.Lon}
			currentPoint := models.CoordinatePoint{Lat: point.Lat, Lon: point.Lon}
			edge := models.NewEdge(prevPoint, currentPoint)

			if _, exists := edges[edge]; exists {
				if len(currentLine) > 1 {
					polylines = append(polylines, string(polyline.EncodeCoords(currentLine)))
				}
				currentLine = [][]float64{}
			} else {
				edges[edge] = true
			}
		}
		if prev.Lon == -1 || prev.Lat != point.Lat || prev.Lon != point.Lon {
			currentLine = append(currentLine, loc)
		}

		prev = point
	}

	if len(currentLine) > 1 {
		polylines = append(polylines, string(polyline.EncodeCoords(currentLine)))
	}

	encodedPoints := strings.Join(polylines, "")

	shapeEntry := models.ShapeEntry{
		Length: len(encodedPoints),
		Levels: "",
		Points: encodedPoints,
	}

	api.sendResponse(w, r, models.NewEntryResponse(shapeEntry, models.NewEmptyReferences(), api.Clock))
}
