package restapi

import (
	"cmp"
	"context"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

// blockHandler returns the block configuration for a given block ID, including
// the ordered sequence of trips and their stop times within the block.
func (api *RestAPI) blockHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencyID, blockID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	//  Return JSON 400 response for invalid block IDs
	// We use an explicit struct here to ensure the text is exactly "invalid block id"
	if blockID == "" {
		api.sendError(w, r, http.StatusBadRequest, "invalid block id")
		return
	}

	block, err := api.GtfsManager.GtfsDB.Queries.GetBlockDetails(ctx, nulls.String(blockID))
	if err != nil {
		if ctx.Err() != nil {
			api.clientCanceledResponse(w, r, ctx.Err())
			return
		}
		api.sendNotFound(w, r)
		return
	}

	//  Return JSON 404 response if no block data is found
	if len(block) == 0 {
		api.sendNotFound(w, r)
		return
	}

	references, err := api.getReferences(ctx, agencyID, block)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// Extract TimeZone from the generated references to avoid a duplicate DB call
	var timeZone string
	if len(references.Agencies) > 0 {
		timeZone = references.Agencies[0].Timezone
	}

	blockEntry := transformBlockToEntry(block, utils.FormCombinedID(agencyID, blockID), agencyID, timeZone)

	response := models.NewEntryResponse(blockEntry, references, api.Clock)
	api.sendResponse(w, r, response)
}

func transformBlockToEntry(block []gtfsdb.GetBlockDetailsRow, blockID, agencyID, timezone string) models.BlockEntry {
	serviceGroups := make(map[string][]gtfsdb.GetBlockDetailsRow)

	for _, row := range block {
		serviceGroups[row.ServiceID] = append(serviceGroups[row.ServiceID], row)
	}

	serviceIDs := make([]string, 0, len(serviceGroups))
	for serviceID := range serviceGroups {
		serviceIDs = append(serviceIDs, serviceID)
	}
	slices.Sort(serviceIDs)

	// configurations will hold our grouped and distinct itineraries
	var configurations []models.BlockConfiguration

	for _, serviceID := range serviceIDs {
		serviceStops := serviceGroups[serviceID]

		tripStops := make(map[string][]gtfsdb.GetBlockDetailsRow)
		for _, stop := range serviceStops {
			tripStops[stop.TripID] = append(tripStops[stop.TripID], stop)
		}

		tripIDs := make([]string, 0, len(tripStops))
		for tripID := range tripStops {
			tripIDs = append(tripIDs, tripID)
		}
		slices.Sort(tripIDs)

		var currentTrips []models.TripBlock
		var blockDistance float64 // Reset block distance for each service configuration

		// Build the trips for the current serviceID
		for _, tripID := range tripIDs {
			trip := buildTripBlock(tripStops[tripID], tripID, agencyID, &blockDistance)
			currentTrips = append(currentTrips, trip)
		}

		// Check if this identical itinerary already exists in our configurations
		found := false
		combinedServiceID := utils.FormCombinedID(agencyID, serviceID)

		for i := range configurations {
			// If the sequence of trips and stop times is identical, group them together
			if reflect.DeepEqual(configurations[i].Trips, currentTrips) {
				configurations[i].ActiveServiceIds = append(configurations[i].ActiveServiceIds, combinedServiceID)
				found = true
				break
			}
		}

		// If no matching itinerary was found, create a new configuration variant
		if !found {
			configurations = append(configurations, models.BlockConfiguration{
				ActiveServiceIds:   []string{combinedServiceID},
				InactiveServiceIds: []string{},
				TimeZone:           timezone,
				Trips:              currentTrips,
			})
		}
	}

	// Sort configurations based on the wiki spec requirements
	slices.SortFunc(configurations, func(a, b models.BlockConfiguration) int {
		// Primary Sort: Descending by the number of active service IDs
		if len(a.ActiveServiceIds) != len(b.ActiveServiceIds) {
			return cmp.Compare(len(b.ActiveServiceIds), len(a.ActiveServiceIds))
		}
		// Secondary Sort: Alphabetically by the first service ID to break ties predictably
		return cmp.Compare(a.ActiveServiceIds[0], b.ActiveServiceIds[0])
	})

	return models.BlockEntry{
		Configurations: configurations,
		ID:             blockID,
	}
}

func buildTripBlock(stops []gtfsdb.GetBlockDetailsRow, tripID, agencyID string, blockDistance *float64) models.TripBlock {
	slices.SortFunc(stops, func(a, b gtfsdb.GetBlockDetailsRow) int {
		return cmp.Compare(a.StopSequence, b.StopSequence)
	})

	var blockStopTimes []models.BlockStopTime
	tripStartDistance := *blockDistance

	for i, stop := range stops {
		var distanceFromPrevious float64
		if i > 0 {
			prevStop := stops[i-1]
			distanceFromPrevious = utils.Distance(
				prevStop.Lat, prevStop.Lon,
				stop.Lat, stop.Lon,
			)
			*blockDistance += distanceFromPrevious
		}

		blockStopTime := models.BlockStopTime{
			BlockSequence:      int(stop.StopSequence - 1),
			DistanceAlongBlock: *blockDistance,
			StopTime: models.StopTime{
				ArrivalTime:   models.NewModelDuration(time.Duration(stop.ArrivalTime)),
				DepartureTime: models.NewModelDuration(time.Duration(stop.DepartureTime)),
				DropOffType:   int(stop.DropOffType.Int64),
				PickupType:    int(stop.PickupType.Int64),
				StopID:        utils.FormCombinedID(agencyID, stop.StopID),
			},
		}
		blockStopTimes = append(blockStopTimes, blockStopTime)
	}

	blockStopTimes = calculateBlockSlackTimes(blockStopTimes)

	var tripAccumulatedSlack time.Duration
	if len(blockStopTimes) > 0 {
		tripAccumulatedSlack = blockStopTimes[len(blockStopTimes)-1].AccumulatedSlackTime.Duration
	}

	tripDistance := *blockDistance - tripStartDistance

	return models.TripBlock{
		AccumulatedSlackTime: models.NewModelDuration(tripAccumulatedSlack),
		BlockStopTimes:       blockStopTimes,
		DistanceAlongBlock:   tripDistance,
		TripId:               utils.FormCombinedID(agencyID, tripID),
	}
}

func (api *RestAPI) getReferences(ctx context.Context, agencyID string, block []gtfsdb.GetBlockDetailsRow) (models.ReferencesModel, error) {
	routeIDs := make(map[string]struct{})
	stopIDs := make(map[string]struct{})
	tripIDs := make(map[string]struct{})

	for _, row := range block {
		routeIDs[row.RouteID] = struct{}{}
		stopIDs[row.StopID] = struct{}{}
		tripIDs[row.TripID] = struct{}{}
	}

	stopIDsArr := make([]string, 0, len(stopIDs))
	for stopID := range stopIDs {
		stopIDsArr = append(stopIDsArr, stopID)
	}
	agency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		return models.ReferencesModel{}, err
	}

	routesArr, err := api.GtfsManager.GtfsDB.Queries.GetRoutesForStops(ctx, stopIDsArr)

	if err != nil {
		return models.ReferencesModel{}, err
	}
	routeSet := make(map[string]struct{})
	routes := make([]models.Route, 0)
	for _, route := range routesArr {
		routeID := utils.FormCombinedID(agencyID, route.ID)
		if _, exists := routeSet[routeID]; exists {
			continue
		}
		routeSet[routeID] = struct{}{}
		routes = append(routes, models.Route{
			ID:          routeID,
			AgencyID:    agencyID,
			ShortName:   route.ShortName.String,
			LongName:    route.LongName.String,
			Description: route.Desc.String,
			Type:        models.RouteType(route.Type),
			Color:       route.Color.String,
			TextColor:   route.TextColor.String,
		})
	}

	// batch fetch
	batchedStops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDsArr)
	if err != nil {
		return models.ReferencesModel{}, err
	}

	stops := make([]models.Stop, 0)
	for _, stop := range batchedStops {
		stops = append(stops, models.Stop{
			ID:             utils.FormCombinedID(agencyID, stop.ID),
			Name:           stop.Name.String,
			Code:           stop.Code.String,
			Lat:            stop.Lat,
			Lon:            stop.Lon,
			Direction:      api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction),
			RouteIDs:       []string{},
			StaticRouteIDs: []string{},
		})
	}

	// batch fetch
	tripIDsArr := make([]string, 0, len(tripIDs))
	for tid := range tripIDs {
		tripIDsArr = append(tripIDsArr, tid)
	}

	batchedTrips, err := api.GtfsManager.GtfsDB.Queries.GetTripsByIDs(ctx, tripIDsArr)
	if err != nil {
		return models.ReferencesModel{}, err
	}

	trips := make([]models.Trip, 0)
	for _, trip := range batchedTrips {
		trips = append(trips, models.Trip{
			ID:           utils.FormCombinedID(agencyID, trip.ID),
			RouteID:      utils.FormCombinedID(agencyID, trip.RouteID),
			ServiceID:    utils.FormCombinedID(agencyID, trip.ServiceID),
			DirectionID:  strconv.FormatInt(trip.DirectionID.Int64, 10),
			BlockID:      utils.FormCombinedID(agencyID, trip.BlockID.String),
			ShapeID:      utils.FormCombinedID(agencyID, trip.ShapeID.String),
			TripHeadsign: trip.TripHeadsign.String,
		})
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{{ID: agency.ID, Name: agency.Name, URL: agency.Url, Timezone: agency.Timezone}}
	references.Routes = routes
	references.Stops = stops
	references.Trips = trips
	return *references, nil
}

func calculateBlockSlackTimes(blockStopTimes []models.BlockStopTime) []models.BlockStopTime {
	var accumulatedBlockSlackTime time.Duration

	for i := range blockStopTimes {
		blockStopTimes[i].AccumulatedSlackTime = models.NewModelDuration(accumulatedBlockSlackTime)
		dwellTime := blockStopTimes[i].StopTime.DepartureTime.Duration - blockStopTimes[i].StopTime.ArrivalTime.Duration
		accumulatedBlockSlackTime += dwellTime
	}

	return blockStopTimes
}
