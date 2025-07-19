package restapi

import (
	"context"
	"database/sql"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
	"net/http"
	"sort"
)

func (api *RestAPI) blockHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	id := utils.ExtractIDFromParams(r)
	agencyID, blockID, err := utils.ExtractAgencyIDAndCodeID(id)

	if err != nil || blockID == "" {
		http.Error(w, "null", http.StatusBadRequest)
		return
	}

	block, err := api.GtfsManager.GtfsDB.Queries.GetBlockDetails(ctx, sql.NullString{String: blockID, Valid: true})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	blockData := models.BlockData{
		Entry: transformBlockToEntry(block, utils.FormCombinedID(agencyID, blockID), agencyID),
	}

	blockResponse := models.BlockResponse{
		Data: blockData,
	}
	references, err := api.getReferences(ctx, agencyID, block)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	response := models.NewEntryResponse(blockResponse, references)
	api.sendResponse(w, r, response)
}

func transformBlockToEntry(block []gtfsdb.GetBlockDetailsRow, blockID, agencyID string) models.BlockEntry {
	serviceGroups := make(map[string][]gtfsdb.GetBlockDetailsRow)

	for _, row := range block {
		serviceGroups[row.ServiceID] = append(serviceGroups[row.ServiceID], row)
	}

	serviceIDs := make([]string, 0, len(serviceGroups))
	for serviceID := range serviceGroups {
		serviceIDs = append(serviceIDs, serviceID)
	}
	sort.Strings(serviceIDs)

	configurations := make([]models.BlockConfiguration, 0, len(serviceGroups))

	var blockDistance float64

	for _, serviceID := range serviceIDs {
		serviceStops := serviceGroups[serviceID]

		config := &models.BlockConfiguration{
			ActiveServiceIds:   []string{utils.FormCombinedID(agencyID, serviceID)},
			InactiveServiceIds: []string{},
			Trips:              make([]models.TripBlock, 0),
		}

		tripStops := make(map[string][]gtfsdb.GetBlockDetailsRow)
		for _, stop := range serviceStops {
			tripStops[stop.TripID] = append(tripStops[stop.TripID], stop)
		}

		tripIDs := make([]string, 0, len(tripStops))
		for tripID := range tripStops {
			tripIDs = append(tripIDs, tripID)
		}
		sort.Strings(tripIDs)

		for _, tripID := range tripIDs {
			stops := tripStops[tripID]

			sort.Slice(stops, func(i, j int) bool {
				return stops[i].StopSequence < stops[j].StopSequence
			})

			var blockStopTimes []models.BlockStopTime
			tripStartDistance := blockDistance

			for i, stop := range stops {
				var distanceFromPrevious float64
				if i > 0 {
					prevStop := stops[i-1]
					distanceFromPrevious = utils.Haversine(
						prevStop.Lat, prevStop.Lon,
						stop.Lat, stop.Lon,
					)
					blockDistance += distanceFromPrevious
				}

				blockStopTime := models.BlockStopTime{
					BlockSequence:      int(stop.StopSequence - 1),
					DistanceAlongBlock: blockDistance,
					StopTime: models.StopTime{
						ArrivalTime:   int(stop.ArrivalTime),
						DepartureTime: int(stop.DepartureTime),
						DropOffType:   int(stop.DropOffType.Int64),
						PickupType:    int(stop.PickupType.Int64),
						StopID:        utils.FormCombinedID(agencyID, stop.StopID),
					},
				}
				blockStopTimes = append(blockStopTimes, blockStopTime)
			}

			blockStopTimes = calculateBlockSlackTimes(blockStopTimes)

			var tripAccumulatedSlack float64
			if len(blockStopTimes) > 0 {
				tripAccumulatedSlack = blockStopTimes[len(blockStopTimes)-1].AccumulatedSlackTime
			}

			tripDistance := blockDistance - tripStartDistance

			trip := models.TripBlock{
				AccumulatedSlackTime: int(tripAccumulatedSlack),
				BlockStopTimes:       blockStopTimes,
				DistanceAlongBlock:   tripDistance,
				TripId:               utils.FormCombinedID(agencyID, tripID),
			}
			config.Trips = append(config.Trips, trip)
		}

		configurations = append(configurations, *config)
	}

	return models.BlockEntry{
		Configurations: configurations,
		ID:             blockID,
	}
}

func (api *RestAPI) getReferences(ctx context.Context, agencyID string, block []gtfsdb.GetBlockDetailsRow) (models.ReferencesModel, error) {
	routeIDs := make(map[string]struct{})
	stopIDs := make(map[string]struct{})
	tripIDs := make(map[string]struct{})

	stopIDsArr := make([]string, 0, len(stopIDs))
	for _, row := range block {
		routeIDs[row.RouteID] = struct{}{}
		stopIDs[row.StopID] = struct{}{}
		tripIDs[row.TripID] = struct{}{}
	}
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
	var routes []interface{}
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

	var stops []models.Stop
	for stopID := range stopIDs {
		stop, err := api.GtfsManager.GtfsDB.Queries.GetStop(ctx, stopID)
		if err != nil {
			return models.ReferencesModel{}, err
		}
		stops = append(stops, models.Stop{
			ID:        utils.FormCombinedID(agencyID, stop.ID),
			Name:      stop.Name.String,
			Code:      stop.Code.String,
			Lat:       stop.Lat,
			Lon:       stop.Lon,
			Direction: "Direction", // TODO: Calculate the direction for the stop
		})
	}

	var trips []interface{}
	for tripID := range tripIDs {
		trip, err := api.GtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
		if err != nil {
			return models.ReferencesModel{}, err
		}
		trips = append(trips, models.Trip{
			ID:           utils.FormCombinedID(agencyID, trip.ID),
			RouteID:      utils.FormCombinedID(agencyID, trip.RouteID),
			ServiceID:    utils.FormCombinedID(agencyID, trip.ServiceID),
			DirectionID:  trip.DirectionID.Int64,
			BlockID:      utils.FormCombinedID(agencyID, trip.BlockID.String),
			ShapeID:      utils.FormCombinedID(agencyID, trip.ShapeID.String),
			TripHeadsign: trip.TripHeadsign.String,
		})
	}

	if stops == nil {
		stops = []models.Stop{}
	}
	if routes == nil {
		routes = []interface{}{}
	}
	if trips == nil {
		trips = []interface{}{}
	}
	return models.ReferencesModel{
		Agencies:   []models.AgencyReference{{ID: agency.ID, Name: agency.Name, URL: agency.Url, Timezone: agency.Timezone}},
		Routes:     routes,
		Stops:      stops,
		Trips:      trips,
		StopTimes:  []interface{}{},
		Situations: []interface{}{},
	}, nil
}

func calculateBlockSlackTimes(blockStopTimes []models.BlockStopTime) []models.BlockStopTime {
	var accumulatedBlockSlackTime int

	for i := range blockStopTimes {
		blockStopTimes[i].AccumulatedSlackTime = float64(accumulatedBlockSlackTime)
		dwellTime := blockStopTimes[i].StopTime.DepartureTime - blockStopTimes[i].StopTime.ArrivalTime
		accumulatedBlockSlackTime += dwellTime
	}

	return blockStopTimes
}
