package restapi

import (
	"net/http"

	"github.com/twpayne/go-polyline"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func (api *RestAPI) shapesHandler(w http.ResponseWriter, r *http.Request) {
	id := utils.ExtractIDFromParams(r)
	if err := utils.ValidateID(id); err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	agencyID, shapeID, err := utils.ExtractAgencyIDAndCodeID(id)
	if err != nil {
		fieldErrors := map[string][]string{
			"id": {err.Error()},
		}
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()

	api.GtfsManager.RLock()
	defer api.GtfsManager.RUnlock()

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

	lineCoords := make([][]float64, 0, len(shapes))
	var totalPoints int

	for i, point := range shapes {
		// Filter consecutive duplicate points to avoid zero-length segments
		if i > 0 && point.Lat == shapes[i-1].Lat && point.Lon == shapes[i-1].Lon {
			continue
		}
		lineCoords = append(lineCoords, []float64{point.Lat, point.Lon})
		totalPoints++
	}

	// Encode as a single continuous polyline to ensure valid delta offsets
	encodedPoints := string(polyline.EncodeCoords(lineCoords))

	shapeEntry := models.ShapeEntry{
		Length: totalPoints,
		Levels: "",
		Points: encodedPoints,
	}

	api.sendResponse(w, r, models.NewEntryResponse(shapeEntry, models.NewEmptyReferences(), api.Clock))
}
