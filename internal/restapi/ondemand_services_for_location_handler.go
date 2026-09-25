package restapi

import (
	"context"
	"net/http"
	"net/url"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/geo"
	"maglev.onebusaway.org/internal/gtfs"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

// onDemandSearch is the resolved query: viewport mode iff no radius and both
// spans are positive (BoundsFromParams' predicate); otherwise point mode with
// the stops-for-location default radius, clamped to the maximum.
type onDemandSearch struct {
	Viewport bool
	Point    geoPoint
	Radius   float64
	Bounds   utils.CoordinateBounds
}

func newOnDemandSearch(loc *gtfs.LocationParams) onDemandSearch {
	point := geoPoint{Lat: loc.Lat, Lon: loc.Lon}
	if loc.Radius <= 0 && loc.LatSpan > 0 && loc.LonSpan > 0 {
		// Viewport bounds are deliberately not clamped: zones are few and a 50 km
		// zone must survive a zoomed-out map.
		return onDemandSearch{
			Viewport: true,
			Point:    point,
			Bounds:   utils.CalculateBoundsFromSpan(loc.Lat, loc.Lon, loc.LatSpan/2, loc.LonSpan/2),
		}
	}
	radius := loc.Radius
	if radius <= 0 {
		radius = models.DefaultSearchRadiusInMeters
	}
	radius = utils.ClampRadius(radius)
	return onDemandSearch{
		Point:  point,
		Radius: radius,
		Bounds: utils.CalculateBounds(loc.Lat, loc.Lon, radius),
	}
}

// onDemandMatch is a matched service and the strongest ground it matched on.
type onDemandMatch struct {
	ServiceID     string // combined
	BareServiceID string
	Reason        string
}

// onDemandServicesForLocationHandler finds services whose areas contain, lie
// near, or intersect the query, or whose rule-referenced stops fall within it.
func (api *RestAPI) onDemandServicesForLocationHandler(w http.ResponseWriter, r *http.Request) {
	loc, geometryDetail, fieldErrors := api.parseOnDemandLocationQuery(r)
	if len(fieldErrors) > 0 {
		api.validationErrorResponse(w, r, fieldErrors)
		return
	}

	ctx := r.Context()
	search := newOnDemandSearch(loc)
	idx := api.GtfsManager.FlexIndex()
	distances := search.areaDistances(idx)
	matches := matchOnDemandServices(idx, search, distances)

	services, err := api.loadMatchedOnDemandServices(ctx, matches)
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	list, references, err := api.buildOnDemandServices(ctx, services, onDemandBuildOptions{GeometryDetail: geometryDetail, AreaDistances: distances})
	if err != nil {
		api.serverErrorResponse(w, r, err)
		return
	}
	applyMatchReasons(list, matches)

	outOfRange := api.onDemandOutOfRange(search.Bounds, idx)
	api.sendResponse(w, r, models.NewOnDemandListResponseWithRange(list, *references, outOfRange, api.Clock))
}

// parseOnDemandLocationQuery reads the location and geometryDetail parameters.
// lat and lon are required here (wiki §3), unlike in parseLocationParams.
func (api *RestAPI) parseOnDemandLocationQuery(r *http.Request) (*gtfs.LocationParams, GeometryDetail, map[string][]string) {
	query := r.URL.Query()
	fieldErrors := requireCoordinates(query)
	loc, fieldErrors := api.parseLocationParams(r, fieldErrors)
	geometryDetail, fieldErrors := parseGeometryDetail(query, GeometryDetailSimplified, fieldErrors)
	return loc, geometryDetail, fieldErrors
}

func requireCoordinates(query url.Values) map[string][]string {
	_, fieldErrors := utils.ParseRequiredStringParam(query, "lat", nil)
	_, fieldErrors = utils.ParseRequiredStringParam(query, "lon", fieldErrors)
	return fieldErrors
}

// areaDistances measures from the query point in point mode; viewport mode has
// no single point to measure from, so it returns nil and areas carry no
// distance.
func (search onDemandSearch) areaDistances(idx *gtfs.FlexIndex) *areaDistances {
	if search.Viewport {
		return nil
	}
	return newAreaDistances(idx, search.Point)
}

// matchOnDemandServices tests every service whose bounds overlap the search
// bounds and returns the ones that match, with their strongest reason.
func matchOnDemandServices(idx *gtfs.FlexIndex, search onDemandSearch, distances *areaDistances) []onDemandMatch {
	var matches []onDemandMatch
	for _, serviceID := range idx.ServiceBoundsOverlapping(search.Bounds) {
		if reason, ok := matchOnDemandService(idx, serviceID, search, distances); ok {
			matches = append(matches, onDemandMatch{ServiceID: serviceID, BareServiceID: idx.BareServiceIDs[serviceID], Reason: reason})
		}
	}
	return matches
}

// matchOnDemandService applies the grounds of the search's mode to one service.
func matchOnDemandService(idx *gtfs.FlexIndex, serviceID string, search onDemandSearch, distances *areaDistances) (string, bool) {
	areas := serviceFlexAreas(idx, serviceID)
	stops := idx.ServiceStops[serviceID]
	if search.Viewport {
		return matchViewport(areas, stops, search.Bounds)
	}
	return matchPoint(areas, stops, distances, search.Radius)
}

// serviceFlexAreas cannot yield a nil area: ServiceAreaIDs lists only ids
// present in Areas (see loadServiceAreas).
func serviceFlexAreas(idx *gtfs.FlexIndex, serviceID string) []*gtfs.FlexArea {
	areaIDs := idx.ServiceAreaIDs[serviceID]
	areas := make([]*gtfs.FlexArea, 0, len(areaIDs))
	for _, areaID := range areaIDs {
		areas = append(areas, idx.FlexArea(areaID))
	}
	return areas
}

// matchPoint applies the point-mode grounds in strength order:
// areaContainsPoint > stopWithinRadius > areaNearby.
func matchPoint(areas []*gtfs.FlexArea, stops []gtfs.FlexStopPoint, distances *areaDistances, radius float64) (string, bool) {
	for _, area := range areas {
		if distances.of(area).Inside {
			return models.MatchReasonAreaContainsPoint, true
		}
	}
	point := distances.point
	for _, stop := range stops {
		if utils.Distance(point.Lat, point.Lon, stop.Lat, stop.Lon) <= radius {
			return models.MatchReasonStopWithinRadius, true
		}
	}
	for _, area := range areas {
		if distances.of(area).Meters <= radius {
			return models.MatchReasonAreaNearby, true
		}
	}
	return "", false
}

// matchViewport applies the viewport-mode grounds in strength order:
// areaIntersectsViewport > stopWithinViewport. Near-miss matching does not apply.
func matchViewport(areas []*gtfs.FlexArea, stops []gtfs.FlexStopPoint, bounds utils.CoordinateBounds) (string, bool) {
	for _, area := range areas {
		if geo.PolygonIntersectsBounds(area.Polygons, bounds) {
			return models.MatchReasonAreaIntersectsViewport, true
		}
	}
	for _, stop := range stops {
		if utils.BoundsContain(bounds, stop.Lat, stop.Lon) {
			return models.MatchReasonStopWithinViewport, true
		}
	}
	return "", false
}

func (api *RestAPI) loadMatchedOnDemandServices(ctx context.Context, matches []onDemandMatch) ([]gtfsdb.OndemandService, error) {
	if len(matches) == 0 {
		return nil, nil
	}
	bareIDs := make([]string, 0, len(matches))
	for _, match := range matches {
		bareIDs = append(bareIDs, match.BareServiceID)
	}
	return queryInBatches(ctx, bareIDs, api.GtfsManager.GtfsDB.Queries.GetOnDemandServicesByIDs)
}

func applyMatchReasons(list []models.OnDemandService, matches []onDemandMatch) {
	reasonByService := make(map[string]string, len(matches))
	for _, match := range matches {
		reasonByService[match.ServiceID] = match.Reason
	}
	for i := range list {
		list[i].MatchReason = reasonByService[list[i].ID]
	}
}

// onDemandOutOfRange is true when the search bounds intersect neither any
// agency's stop bounds nor any service's bounds. CheckIfOutOfBounds alone is
// wrong here: Alexandria has one stop inside a ~50 km zone. No bounds at all
// means false.
func (api *RestAPI) onDemandOutOfRange(bounds utils.CoordinateBounds, idx *gtfs.FlexIndex) bool {
	regions := api.GtfsManager.GetRegionBounds()
	if len(regions) == 0 && len(idx.ServiceBounds) == 0 {
		return false
	}
	for _, region := range regions {
		regionBounds := utils.CalculateBoundsFromSpan(region.Lat, region.Lon, region.LatSpan/2, region.LonSpan/2)
		if !utils.IsOutOfBounds(bounds, regionBounds) {
			return false
		}
	}
	return len(idx.ServiceBoundsOverlapping(bounds)) == 0
}
