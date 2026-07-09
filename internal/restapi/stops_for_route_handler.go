package restapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"time"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/nulls"
	"maglev.onebusaway.org/internal/utils"
)

type stopsForRouteParams struct {
	IncludePolylines bool
	Time             *time.Time
}

func (api *RestAPI) parseStopsForRouteParams(r *http.Request) stopsForRouteParams {
	now := api.Clock.Now()
	params := stopsForRouteParams{
		IncludePolylines: true,
		Time:             &now,
	}

	if r.URL.Query().Get("includePolylines") == "false" {
		params.IncludePolylines = false
	}

	if timeParam := r.URL.Query().Get("time"); timeParam != "" {
		if t, err := time.Parse(time.RFC3339, timeParam); err == nil {
			params.Time = &t
		}
	}
	return params
}

// stopsForRouteHandler returns all stops served by a route, grouped by direction
// with optional encoded polyline shapes.
func (api *RestAPI) stopsForRouteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check if context is already cancelled
	if ctx.Err() != nil {
		api.clientCanceledResponse(w, r, ctx.Err())
		return
	}

	agencyID, routeID, ok := api.extractAndValidateAgencyCodeID(w, r)
	if !ok {
		return
	}

	params := api.parseStopsForRouteParams(r)

	currentAgency, err := api.GtfsManager.GtfsDB.Queries.GetAgency(ctx, agencyID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	currentLocation, err := loadAgencyLocation(currentAgency.ID, currentAgency.Timezone)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	// The service-date filter is only applied when a time is supplied. With no
	// time, the endpoint returns stops/shapes across all service dates, matching
	// the Java reference default (display.serviceDateFiltering defaults to off).
	timeParam := r.URL.Query().Get("time")
	filterByDate := timeParam != ""

	var serviceIDs []string
	if filterByDate {
		formattedDate, _, fieldErrors, success := utils.ParseTimeParameter(timeParam, currentLocation)
		if !success {
			api.validationErrorResponse(w, r, fieldErrors)
			return
		}
		serviceIDs, err = api.GtfsManager.GtfsDB.Queries.GetActiveServiceIDsForDate(ctx, formattedDate)
		if err != nil {
			api.serverErrorResponse(w, r, err)
			return
		}
	}

	_, err = api.GtfsManager.GtfsDB.Queries.GetRoute(ctx, routeID)
	if err != nil {
		api.sendNotFound(w, r)
		return
	}

	result, stopsList, err := api.processRouteStops(ctx, agencyID, routeID, serviceIDs, filterByDate, params.IncludePolylines)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	api.buildAndSendResponse(w, r, ctx, result, stopsList, currentAgency)
}

func (api *RestAPI) processRouteStops(ctx context.Context, agencyID string, routeID string, serviceIDs []string, filterByDate bool, includePolylines bool) (models.RouteEntry, []models.Stop, error) {
	allStops := make(map[string]bool)
	var stopGroupings []models.StopGrouping

	var effectiveTrips []gtfsdb.Trip
	var err error
	if filterByDate {
		// A time was supplied: restrict to trips active on that service date.
		effectiveTrips, err = api.GtfsManager.GtfsDB.Queries.GetTripsForRouteInActiveServiceIDs(ctx, gtfsdb.GetTripsForRouteInActiveServiceIDsParams{
			RouteID:    routeID,
			ServiceIds: serviceIDs,
		})
	} else {
		// No time: use every trip for the route, across all service dates.
		effectiveTrips, err = api.GtfsManager.GtfsDB.Queries.GetAllTripsForRoute(ctx, routeID)
	}
	if err != nil {
		return models.RouteEntry{}, nil, err
	}

	if err := processTripGroups(ctx, api, agencyID, routeID, effectiveTrips, &stopGroupings, allStops, includePolylines); err != nil {
		return models.RouteEntry{}, nil, err
	}

	// Entry-level polylines are an independent merge over the shapes of every
	// qualifying trip, mirroring Java's getEncodedPolylinesForRoute — not a
	// concatenation of the per-direction group polylines.
	entryPolylines := []models.Polyline{}
	if includePolylines {
		entryPolylines, err = api.mergePolylinesForShapeIDs(ctx, distinctShapeIDs(effectiveTrips))
		if err != nil {
			return models.RouteEntry{}, nil, err
		}
	}

	allStopsIds := formatStopIDs(agencyID, allStops)
	stopsList, err := buildStopsList(ctx, api, agencyID, allStops)
	if err != nil {
		return models.RouteEntry{}, nil, err
	}

	result := models.RouteEntry{
		Polylines:     entryPolylines,
		RouteID:       utils.FormCombinedID(agencyID, routeID),
		StopGroupings: stopGroupings,
		StopIds:       allStopsIds,
	}

	return result, stopsList, nil
}

func buildStopsList(ctx context.Context, api *RestAPI, agencyID string, allStops map[string]bool) ([]models.Stop, error) {

	stopIDs := make([]string, 0, len(allStops))
	for stopID := range allStops {
		stopIDs = append(stopIDs, stopID)
	}

	stops, err := api.GtfsManager.GtfsDB.Queries.GetStopsByIDs(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	routeRows, err := api.GtfsManager.GtfsDB.Queries.GetRouteIDsForStops(ctx, stopIDs)
	if err != nil {
		return nil, err
	}

	// Organize Routes in Memory
	routesMap := make(map[string][]string)
	for _, row := range routeRows {
		routeID, ok := row.RouteID.(string)
		stopID := row.StopID

		if ok {
			routesMap[stopID] = append(routesMap[stopID], routeID)
		}
	}

	stopsList := make([]models.Stop, 0, len(stops))

	for _, stop := range stops {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		direction := api.DirectionCalculator.CalculateStopDirection(ctx, stop.ID, stop.Direction)

		routeIdsString := append([]string(nil), routesMap[stop.ID]...)

		stopsList = append(stopsList, models.Stop{
			Code:               stop.Code.String,
			Direction:          direction,
			ID:                 utils.FormCombinedID(agencyID, stop.ID),
			Lat:                stop.Lat,
			LocationType:       int(stop.LocationType.Int64),
			Lon:                stop.Lon,
			Name:               stop.Name.String,
			RouteIDs:           routeIdsString,
			StaticRouteIDs:     routeIdsString,
			WheelchairBoarding: utils.MapWheelchairBoarding(nulls.WheelchairBoardingOrUnknown(stop.WheelchairBoarding)),
		})
	}
	return stopsList, nil
}

func (api *RestAPI) buildAndSendResponse(w http.ResponseWriter, r *http.Request, ctx context.Context, result models.RouteEntry, stopsList []models.Stop, currentAgency gtfsdb.Agency) {
	agencyRef := models.NewAgencyReference(
		currentAgency.ID,
		currentAgency.Name,
		currentAgency.Url,
		currentAgency.Timezone,
		currentAgency.Lang.String,
		currentAgency.Phone.String,
		currentAgency.Email.String,
		currentAgency.FareUrl.String,
		"",
		false,
	)

	routes, err := api.BuildRouteReferences(ctx, currentAgency.ID, stopsList)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}

	references := models.NewEmptyReferences()
	references.Agencies = []models.AgencyReference{agencyRef}
	references.Routes = routes
	references.Stops = stopsList

	response := models.NewEntryResponse(result, *references, api.Clock)
	api.sendResponse(w, r, response)
}

func processTripGroups(
	ctx context.Context,
	api *RestAPI,
	agencyID string,
	routeID string,
	trips []gtfsdb.Trip,
	stopGroupings *[]models.StopGrouping,
	allStops map[string]bool,
	includePolylines bool,
) error {
	dirGroups := groupTripsByDirection(trips)

	var allStopGroups []models.StopGroup

	for _, group := range dirGroups {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stopGroup, err := buildStopGroup(ctx, api, agencyID, routeID, group, allStops, includePolylines)
		if err != nil {
			return err
		}
		allStopGroups = append(allStopGroups, stopGroup)
	}

	slices.SortFunc(allStopGroups, func(a, b models.StopGroup) int {
		return cmp.Compare(a.Name.Name, b.Name.Name)
	})

	*stopGroupings = append(*stopGroupings, models.StopGrouping{
		Ordered:    true,
		StopGroups: allStopGroups,
		Type:       "direction",
	})
	return nil
}

// buildStopGroup assembles a single direction's StopGroup: its ordered stop IDs,
// most-common headsign name, and merged polylines. It also records the group's
// stops in allStops.
func buildStopGroup(ctx context.Context, api *RestAPI, agencyID string, routeID string, group directionGroup, allStops map[string]bool, includePolylines bool) (models.StopGroup, error) {
	headsignCounts, dirServiceIDs := summarizeTrips(group.Trips)

	orderedStopIDs, err := orderedStopIDsForGroup(ctx, api, routeID, group, dirServiceIDs)
	if err != nil {
		return models.StopGroup{}, err
	}
	for _, stopID := range orderedStopIDs {
		allStops[stopID] = true
	}

	// groupPolylines stays a non-nil empty slice so it serializes as [] (not null)
	// when includePolylines is false or a group has no shapes. The polylines are
	// merged from the distinct shapes of this direction's trips, mirroring Java's
	// getShapeIdsForStopSequenceBlock + merge.
	groupPolylines := []models.Polyline{}
	if includePolylines {
		groupPolylines, err = api.mergePolylinesForShapeIDs(ctx, distinctShapeIDs(group.Trips))
		if err != nil {
			return models.StopGroup{}, err
		}
	}

	formattedStopIDs := make([]string, len(orderedStopIDs))
	for idx, id := range orderedStopIDs {
		formattedStopIDs[idx] = utils.FormCombinedID(agencyID, id)
	}

	groupHeadsign := mostCommonHeadsign(headsignCounts)
	return models.StopGroup{
		ID: group.GroupID,
		Name: models.StopGroupName{
			Name:  groupHeadsign,
			Names: []string{groupHeadsign},
			Type:  "destination",
		},
		StopIds:   formattedStopIDs,
		Polylines: groupPolylines,
	}, nil
}

// summarizeTrips counts trip headsigns and collects the distinct service IDs of a
// direction's trips (preserving first-seen order).
func summarizeTrips(trips []gtfsdb.Trip) (map[string]int, []string) {
	headsignCounts := make(map[string]int)
	var dirServiceIDs []string
	seenServiceIDs := make(map[string]bool)
	for _, trip := range trips {
		headsignCounts[trip.TripHeadsign.String]++
		if !seenServiceIDs[trip.ServiceID] {
			seenServiceIDs[trip.ServiceID] = true
			dirServiceIDs = append(dirServiceIDs, trip.ServiceID)
		}
	}
	return headsignCounts, dirServiceIDs
}

// orderedStopIDsForGroup returns the stop IDs of a direction in route sequence.
func orderedStopIDsForGroup(ctx context.Context, api *RestAPI, routeID string, group directionGroup, dirServiceIDs []string) ([]string, error) {
	if !group.DirectionID.Valid {
		/*
			direction_id is NULL in the GTFS data. SQL NULL = NULL evaluates to
			UNKNOWN, not TRUE, so GetOrderedStopIDsForRouteDirection would return
			zero rows. Fall back to single-trip ordering instead.
		*/
		return api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForTrip(ctx, group.Trips[0].ID)
	}
	return api.GtfsManager.GtfsDB.Queries.GetOrderedStopIDsForRouteDirection(ctx,
		gtfsdb.GetOrderedStopIDsForRouteDirectionParams{
			RouteID:     routeID,
			DirectionID: group.DirectionID,
			ServiceIds:  dirServiceIDs,
		})
}

// mostCommonHeadsign returns the headsign with the highest count, breaking ties by
// the lexicographically smaller headsign.
func mostCommonHeadsign(headsignCounts map[string]int) string {
	groupHeadsign := ""
	maxCount := 0
	for headsign, count := range headsignCounts {
		if count > maxCount || (count == maxCount && headsign < groupHeadsign) {
			groupHeadsign = headsign
			maxCount = count
		}
	}
	return groupHeadsign
}

// distinctShapeIDs returns the unique, non-empty shape IDs of the given trips in
// sorted order. Java iterates a HashSet of shape IDs (unspecified order); we sort
// for deterministic output.
func distinctShapeIDs(trips []gtfsdb.Trip) []string {
	seen := make(map[string]bool)
	var shapeIDs []string
	for _, trip := range trips {
		if !trip.ShapeID.Valid || trip.ShapeID.String == "" {
			continue
		}
		if seen[trip.ShapeID.String] {
			continue
		}
		seen[trip.ShapeID.String] = true
		shapeIDs = append(shapeIDs, trip.ShapeID.String)
	}
	slices.Sort(shapeIDs)
	return shapeIDs
}

// coordPoint is an exact-valued lat/lon used as a map key for edge de-duplication.
type coordPoint struct {
	lat float64
	lon float64
}

// edgeKey is an undirected edge between two coordinate points, normalized so that
// (a,b) and (b,a) compare equal — matching Java's Edge class in ShapeBeanServiceImpl.
type edgeKey struct {
	a coordPoint
	b coordPoint
}

func makeEdge(p, q coordPoint) edgeKey {
	if p.lat < q.lat || (p.lat == q.lat && p.lon <= q.lon) {
		return edgeKey{a: p, b: q}
	}
	return edgeKey{a: q, b: p}
}

// mergePolylinesForShapeIDs ports Java's ShapeBeanServiceImpl.getMergedPolylinesForShapeIds.
// It walks the ordered points of each shape while maintaining a shared set of
// undirected edges. Consecutive duplicate points are dropped; when an edge has
// already been seen, the current line is flushed as one polyline and a new line
// begins, de-overlapping shared track. Each line is floor-encoded via
// utils.EncodePolyline with length = the merged line's point count.
func (api *RestAPI) mergePolylinesForShapeIDs(ctx context.Context, shapeIDs []string) ([]models.Polyline, error) {
	merger := newPolylineMerger()
	for _, shapeID := range shapeIDs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		points, err := api.GtfsManager.GtfsDB.Queries.GetShapeByID(ctx, shapeID)
		if err != nil {
			return nil, err
		}
		if len(points) == 0 {
			api.Logger.Warn("no shape points for shape", "shape_id", shapeID)
			continue
		}
		merger.addShape(points)
	}
	return merger.polylines, nil
}

// polylineMerger accumulates shape points into de-overlapped polylines, tracking a
// shared undirected edge set across all shapes added to it.
type polylineMerger struct {
	edges       map[edgeKey]struct{}
	currentLine [][]float64
	polylines   []models.Polyline
}

func newPolylineMerger() *polylineMerger {
	return &polylineMerger{
		edges:     make(map[edgeKey]struct{}),
		polylines: []models.Polyline{},
	}
}

// flush emits the current line as a polyline (if it has at least one segment) and
// starts a new one.
func (m *polylineMerger) flush() {
	if len(m.currentLine) > 1 {
		m.polylines = append(m.polylines, models.Polyline{
			Length: len(m.currentLine),
			Levels: "",
			Points: utils.EncodePolyline(m.currentLine),
		})
	}
	m.currentLine = nil
}

// addShape walks a shape's ordered points, dropping consecutive duplicates and
// flushing whenever an already-seen edge is encountered.
func (m *polylineMerger) addShape(points []gtfsdb.Shape) {
	var prev coordPoint
	havePrev := false
	for _, p := range points {
		loc := coordPoint{lat: p.Lat, lon: p.Lon}
		if havePrev && prev != loc {
			edge := makeEdge(prev, loc)
			if _, seen := m.edges[edge]; seen {
				m.flush()
			} else {
				m.edges[edge] = struct{}{}
			}
		}
		if !havePrev || prev != loc {
			m.currentLine = append(m.currentLine, []float64{loc.lat, loc.lon})
		}
		prev = loc
		havePrev = true
	}
	m.flush()
}

func formatStopIDs(agencyID string, stops map[string]bool) []string {
	var stopIDs []string
	for key := range stops {
		stopIDs = append(stopIDs, utils.FormCombinedID(agencyID, key))
	}
	slices.Sort(stopIDs)

	return stopIDs
}
