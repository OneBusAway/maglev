package restapi

import (
	"net/http"

	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
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

	// Include every point with no simplification or consecutive-duplicate
	// filtering, matching the Java reference (ShapeBeanServiceImpl.getPolylineForShapeId).
	lineCoords := make([][]float64, 0, len(shapes))
	for _, point := range shapes {
		lineCoords = append(lineCoords, []float64{point.Lat, point.Lon})
	}

	// Encode using a floor-based encoder to stay byte-for-byte identical to the
	// Java PolylineEncoder (which floors coordinates rather than rounding).
	encodedPoints := utils.EncodePolyline(lineCoords)

	shapeEntry := models.ShapeEntry{
		Length: len(lineCoords),
		Levels: "",
		Points: encodedPoints,
	}

	api.sendResponse(w, r, models.NewEntryResponse(shapeEntry, *models.NewEmptyReferences(), api.Clock))
}
