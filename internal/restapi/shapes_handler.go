package restapi

import (
	"net/http"

	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/internal/models"
)

// shapesHandler returns the encoded polyline shape for a route's geographic path.
func (api *RestAPI) shapesHandler(w http.ResponseWriter, r *http.Request) {
	agencyID, shapeCode, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()

	_, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)

	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	shapes, err := api.GtfsManager.GtfsDB.Queries.GetShapeByID(ctx, shapeCode)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	if len(shapes) == 0 {
		api.sendNotFound(w, r)
		return
	}

	lineCoords := make([][]float64, 0, len(shapes))

	for i, point := range shapes {
		// Filter consecutive duplicate points to avoid zero-length segments
		if i > 0 && point.Lat == shapes[i-1].Lat && point.Lon == shapes[i-1].Lon {
			continue
		}
		lineCoords = append(lineCoords, []float64{point.Lat, point.Lon})
	}

	// Encode as a single continuous polyline to ensure valid delta offsets
	encodedPoints := string(polyline.EncodeCoords(lineCoords))

	shapeEntry := models.ShapeEntry{
		Length: len(lineCoords),
		Levels: "",
		Points: encodedPoints,
	}

	api.sendResponse(w, r, models.NewEntryResponse(shapeEntry, *models.NewEmptyReferences(), api.Clock))
}
