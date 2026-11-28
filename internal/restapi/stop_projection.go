// Stop projection minimizes the total stop-to-shape distance while keeping
// projected positions non-decreasing in trip order. Consecutive visits to the
// same stop prefer distinct positions at equal cost when the order allows it.

package restapi

import (
	"math"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

const (
	metresPerDegree         = utils.RadiusOfEarthInMeters * math.Pi / 180
	stopProjectionTolerance = 1e-6 // metres
)

type stopProjectionMatcher struct {
	shape      []gtfs.ShapePoint
	cumulative []float64
	boxes      []utils.CoordinateBounds
	stops      []orderedStop
	positions  []float64
}

// projectStopsInSequence returns each stop's distance-along-trip in metres,
// projecting the stop's lat/lon onto the shape polyline. The publisher's
// stop_times.shape_dist_traveled is deliberately ignored — Java's
// StopTimeEntriesFactory.ensureStopTimesHaveShapeDistanceTraveledSet does
// the same, overwriting the publisher value at load time with the projected
// value from DistanceAlongShapeLibrary. Publisher-supplied values are often
// wrong (bad units, corrupt data on some feeds), so both codebases treat
// projection as authoritative.
func projectStopsInSequence(
	stopIDs []string,
	stopByID map[string]gtfsdb.Stop,
	shapePoints []gtfs.ShapePoint,
	cumulativeDistances []float64,
) []float64 {
	positions := make([]float64, len(stopIDs))
	if len(shapePoints) < 2 || len(cumulativeDistances) != len(shapePoints) {
		return positions
	}
	matcher := stopProjectionMatcher{
		shape:      shapePoints,
		cumulative: cumulativeDistances,
		boxes:      projectionBounds(shapePoints, len(stopIDs)),
		positions:  positions,
	}
	var scratch [128]orderedStop
	stops, inOrder := matcher.projectNearestStops(scratch[:0], stopIDs, stopByID)
	matcher.stops = stops
	// Independently closest positions attain the lower bound if they follow
	// trip order. Try other equally close positions before the full solver.
	if !inOrder && !matcher.matchNearestStops() {
		matcher.matchOrderedStops()
	}
	matcher.separateRepeatedStopVisits()
	return positions
}

func (matcher *stopProjectionMatcher) projectNearestStops(stops []orderedStop, stopIDs []string, stopByID map[string]gtfsdb.Stop) ([]orderedStop, bool) {
	inOrder := true
	for i, stopID := range stopIDs {
		stop, ok := stopByID[stopID]
		if !ok {
			continue
		}
		geometry := stopProjectionGeometry{
			lat: stop.Lat, lon: stop.Lon,
			lonMetres: metresPerDegree * math.Cos(stop.Lat*math.Pi/180),
		}
		best := matcher.closestStopProjection(geometry)
		if len(stops) > 0 && best.position < stops[len(stops)-1].nearest.position {
			inOrder = false
		}
		stops = append(stops, orderedStop{
			index:           i,
			geometry:        geometry,
			nearest:         best,
			repeatsPrevious: i > 0 && stopID == stopIDs[i-1],
		})
		matcher.positions[i] = best.position
	}
	return stops, inOrder
}

type stopProjection struct {
	position float64
	cost     float64
}

// A stop-centred local plane keeps the segment projection, distance score and
// search bounds in the same metric. Positions still use cumulative shape metres.
type stopProjectionGeometry struct {
	lat, lon  float64
	lonMetres float64
}

type orderedStop struct {
	index           int
	geometry        stopProjectionGeometry
	nearest         stopProjection
	repeatsPrevious bool
}

// Along a segment the distance to a stop is the hypotenuse of the cross-track
// error and the displacement from the unclamped closest point.
type segmentStopDistance struct {
	length, centre, crossTrack float64
}

func (stop stopProjectionGeometry) segment(from, to gtfs.ShapePoint) segmentStopDistance {
	deltaLon := to.Longitude - from.Longitude
	stopLon := stop.lon - from.Longitude
	if math.Abs(deltaLon) > 180 || math.Abs(stopLon) > 90 {
		// Use the copy of the whole segment whose midpoint is nearest the stop.
		// Wrapping the two endpoints independently can split a segment in two.
		deltaLon = wrapLongitude(deltaLon)
		stopLon = wrapLongitude(stopLon-deltaLon/2) + deltaLon/2
	}
	dx := deltaLon * stop.lonMetres
	dy := (to.Latitude - from.Latitude) * metresPerDegree
	x := stopLon * stop.lonMetres
	y := (stop.lat - from.Latitude) * metresPerDegree
	lengthSquared := dx*dx + dy*dy
	if lengthSquared == 0 {
		return segmentStopDistance{crossTrack: math.Sqrt(x*x + y*y)}
	}
	length := math.Sqrt(lengthSquared)
	return segmentStopDistance{
		length: length, centre: (x*dx + y*dy) / lengthSquared,
		crossTrack: (x*dy - y*dx) / length,
	}
}

// wrapLongitude normalizes a longitude or longitude offset to [-180, 180].
func wrapLongitude(degrees float64) float64 {
	if degrees < -180 || degrees > 180 {
		return math.Remainder(degrees, 360)
	}
	return degrees
}

func (d segmentStopDistance) cost(ratio float64) float64 {
	along := (ratio - d.centre) * d.length
	return math.Sqrt(along*along + d.crossTrack*d.crossTrack)
}

func (d segmentStopDistance) hasLinearCost() bool {
	return d.length == 0 || (d.crossTrack == 0 && (d.centre <= 0 || d.centre >= 1))
}

func (d segmentStopDistance) closestRatio() float64 {
	return min(1, max(0, d.centre))
}

func (d segmentStopDistance) excessAtRatio(ratio, nearestCost float64) float64 {
	return max(0, d.cost(ratio)-nearestCost)
}

func (stop stopProjectionGeometry) bounds(radius float64) utils.CoordinateBounds {
	window := utils.CalculateBoundsFromSpan(stop.lat, stop.lon, radius/metresPerDegree, radius/math.Abs(stop.lonMetres))
	if window.MinLon <= -180 || window.MaxLon >= 180 {
		// A window straddling the date line can only prune by latitude.
		window.MinLon, window.MaxLon = -180, 180
	}
	return window
}

const segmentsPerProjectionBox = 32

// Each box includes both ends of every segment it covers. Adjacent boxes
// share one vertex. Small inputs are cheaper to scan directly.
func projectionBounds(shape []gtfs.ShapePoint, stops int) []utils.CoordinateBounds {
	if len(shape) < 128 || stops < 4 {
		return nil
	}
	boxes := make([]utils.CoordinateBounds, 0, (len(shape)-2)/segmentsPerProjectionBox+1)
	for start := 0; start < len(shape)-1; start += segmentsPerProjectionBox {
		p := shape[start]
		box := utils.CoordinateBounds{MinLat: p.Latitude, MaxLat: p.Latitude, MinLon: p.Longitude, MaxLon: p.Longitude}
		for _, q := range shape[start+1 : min(start+segmentsPerProjectionBox+1, len(shape))] {
			box.MinLat = min(box.MinLat, q.Latitude)
			box.MaxLat = max(box.MaxLat, q.Latitude)
			box.MinLon = min(box.MinLon, q.Longitude)
			box.MaxLon = max(box.MaxLon, q.Longitude)
		}
		if box.MaxLon-box.MinLon > 180 || box.MinLon < -180 || box.MaxLon > 180 {
			// A segment crossing the date line can leave the raw longitude bounds.
			box.MinLon, box.MaxLon = -180, 180
		}
		boxes = append(boxes, box)
	}
	return boxes
}

// Search every run of segments that could contain a match within the radius.
// The window uses the same longitude scale as the projection. At high
// latitudes, use the full scan. Ties retain the first occurrence in shape order.
func (matcher *stopProjectionMatcher) closestInBounds(stop stopProjectionGeometry, radius float64) (stopProjection, bool) {
	boxes := matcher.boxes
	best := stopProjection{cost: math.Inf(1)}
	if len(boxes) == 0 || math.Abs(stop.lat) > 85 {
		return best, false
	}
	window := stop.bounds(radius)
	for i := 0; i < len(boxes); {
		if utils.IsOutOfBounds(boxes[i], window) {
			i++
			continue
		}
		start := i * segmentsPerProjectionBox
		i++
		for i < len(boxes) && !utils.IsOutOfBounds(boxes[i], window) {
			i++
		}
		end := min(i*segmentsPerProjectionBox+1, len(matcher.shape))
		candidate := matcher.scanStopProjection(stop, start, end)
		if candidate.cost < best.cost {
			best = candidate
		}
	}
	return best, best.cost <= radius
}

func (matcher *stopProjectionMatcher) closestStopProjection(stop stopProjectionGeometry) stopProjection {
	if best, ok := matcher.closestInBounds(stop, 50); ok {
		return best
	}
	if len(matcher.boxes) > 0 {
		if best, ok := matcher.closestInBounds(stop, 500); ok {
			return best
		}
	}
	return matcher.scanStopProjection(stop, 0, len(matcher.shape))
}

func (matcher *stopProjectionMatcher) scanStopProjection(stop stopProjectionGeometry, start, end int) stopProjection {
	shape := matcher.shape[start:end]
	cumulative := matcher.cumulative[start:end]
	best := stopProjection{cost: math.Inf(1)}
	for i, from := range shape[:len(shape)-1] {
		segment := stop.segment(from, shape[i+1])
		ratio := segment.closestRatio()
		if cost := segment.cost(ratio); cost < best.cost {
			best = stopProjection{position: cumulative[i] + ratio*(cumulative[i+1]-cumulative[i]), cost: cost}
		}
	}
	return best
}

// Taking the earliest feasible minimum leaves the most room for the next stop.
// A complete sequence of independent minima attains the error lower bound.
func (matcher *stopProjectionMatcher) matchNearestStops() bool {
	stops := matcher.stops
	previous, end := matcher.cumulative[0], matcher.cumulative[len(matcher.cumulative)-1]
	tolerance := stopProjectionTolerance / float64(len(stops))
	for i, stop := range stops {
		position := stop.nearest.position
		if position < previous {
			var ok bool
			position, ok = matcher.firstMatchingProjection(stop.geometry, previous, end, stop.nearest.cost+tolerance)
			if !ok {
				// Finish the feasible prefix at its last position to bound the solver.
				for _, remaining := range stops[i:] {
					matcher.positions[remaining.index] = previous
				}
				return false
			}
		}
		matcher.positions[stop.index] = position
		previous = position
	}
	return true
}

// Search each segment's closest point in shape order. Repeated visits start
// beyond the preceding position so a shared vertex counts only once.
func (matcher *stopProjectionMatcher) firstMatchingProjection(stop stopProjectionGeometry, minPosition, maxPosition, maxCost float64) (float64, bool) {
	shape, cumulative := matcher.shape, matcher.cumulative
	start, _ := slices.BinarySearch(cumulative, minPosition)
	for i := max(0, start-1); i < len(shape)-1 && cumulative[i] <= maxPosition; i++ {
		segment := stop.segment(shape[i], shape[i+1])
		ratio := segment.closestRatio()
		position := cumulative[i] + ratio*(cumulative[i+1]-cumulative[i])
		if position >= minPosition && position <= maxPosition && segment.cost(ratio) <= maxCost {
			return position, true
		}
	}
	return 0, false
}

func (matcher *stopProjectionMatcher) separateRepeatedStopVisits() {
	stops, positions := matcher.stops, matcher.positions
	for i := 1; i < len(stops); {
		position := positions[stops[i].index]
		if !stops[i].repeatsPrevious || position != positions[stops[i-1].index] {
			i++
			continue
		}
		end := i + 1
		for end < len(stops) && stops[end].repeatsPrevious && positions[stops[end].index] == position {
			end++
		}
		limit := matcher.cumulative[len(matcher.cumulative)-1]
		if end < len(stops) {
			limit = positions[stops[end].index]
			if stops[end].repeatsPrevious {
				limit -= stopProjectionTolerance
			}
		}
		start := matcher.cumulative[0]
		if i >= 2 {
			start = positions[stops[i-2].index]
			if stops[i-1].repeatsPrevious {
				start += stopProjectionTolerance
			}
		}
		maxCost := matcher.projectionCostAtPosition(stops[i], position) + stopProjectionTolerance
		if earlier, ok := matcher.firstMatchingProjection(stops[i].geometry, start, position-stopProjectionTolerance, maxCost); ok {
			position = earlier
			positions[stops[i-1].index] = earlier
		}
		matcher.spreadRepeatedStopVisits(stops[i:end], position, limit, maxCost)
		i = end
	}
}

func (matcher *stopProjectionMatcher) spreadRepeatedStopVisits(repeats []orderedStop, position, limit, maxCost float64) {
	stop := repeats[0]
	for i, visit := range repeats {
		next, ok := matcher.firstMatchingProjection(stop.geometry, position+stopProjectionTolerance, limit, maxCost)
		if !ok {
			for _, remaining := range repeats[i:] {
				matcher.positions[remaining.index] = position
			}
			return
		}
		matcher.positions[visit.index] = next
		position = next
	}
}

type stopAssignment struct {
	previous, first, end int
	position, cost       float64
}

// At an optimal interior position, either one stop is at its own minimum or
// a contiguous run of stops shares a minimum of their total distance. At a
// vertex the stops can share the position through successive single-stop
// assignments. Process these possibilities in shape order, retaining the
// cheapest assignment of each prefix of stops.
func (matcher *stopProjectionMatcher) matchOrderedStops() {
	shape, cumulative := matcher.shape, matcher.cumulative
	stops := matcher.stops
	forwardError, backwardError := matcher.orderedProjectionErrorBounds()
	var prefixError float64
	for _, stop := range stops {
		prefixError += max(0, matcher.projectionCostAtPosition(stop, matcher.positions[stop.index])-stop.nearest.cost)
	}
	maxExcess := min(prefixError, forwardError, backwardError) + stopProjectionTolerance
	search := newStopGroupSearch(stops, maxExcess)
	assignments := []stopAssignment{{}}
	best := make([]int, len(stops)+1)
	for i := 1; i < len(best); i++ {
		best[i] = -1
	}
	for i, from := range shape[:len(shape)-1] {
		if i%segmentsPerProjectionBox == 0 {
			search.selectStops(matcher.boxes, i/segmentsPerProjectionBox)
		}
		length := cumulative[i+1] - cumulative[i]
		if length == 0 {
			continue
		}
		groups := search.candidates(stops, from, shape[i+1])
		assignments = updateStopPrefixes(best, assignments, groups, cumulative[i], length, maxExcess)
	}
	if best[len(stops)] < 0 {
		// Keep the feasible upper-bound assignment if roundoff prevents a
		// complete path from passing the pruning bounds.
		if min(forwardError, backwardError) < prefixError {
			matcher.clampNearestStopPositions(forwardError <= backwardError)
		}
		return
	}
	for index := best[len(stops)]; index > 0; {
		assignment := assignments[index]
		for _, stop := range stops[assignment.first:assignment.end] {
			matcher.positions[stop.index] = assignment.position
		}
		index = assignment.previous
	}
}

func updateStopPrefixes(best []int, assignments []stopAssignment, groups []stopGroup, start, length, maxExcess float64) []stopAssignment {
	for _, group := range groups {
		previous := best[group.first]
		if previous < 0 {
			continue
		}
		cost := assignments[previous].cost + group.cost
		if cost > maxExcess {
			continue
		}
		current := best[group.end]
		if current < 0 || cost+stopProjectionTolerance < assignments[current].cost {
			best[group.end] = len(assignments)
			assignments = append(assignments, stopAssignment{
				previous: previous, first: group.first, end: group.end,
				position: start + group.ratio*length, cost: cost,
			})
		}
	}
	return assignments
}

// Upper bounds from clamping independent minima forward and backward limit
// how far any stop can deviate from its independent minimum in the solver.
func (matcher *stopProjectionMatcher) orderedProjectionErrorBounds() (float64, float64) {
	stops := matcher.stops
	var forwardError, backwardError float64
	forward, backward := matcher.cumulative[0], matcher.cumulative[len(matcher.cumulative)-1]
	for i, stop := range stops {
		last := stops[len(stops)-1-i]
		forward = max(forward, stop.nearest.position)
		backward = min(backward, last.nearest.position)
		forwardError += max(0, matcher.projectionCostAtPosition(stop, forward)-stop.nearest.cost)
		backwardError += max(0, matcher.projectionCostAtPosition(last, backward)-last.nearest.cost)
	}
	return forwardError, backwardError
}

func (matcher *stopProjectionMatcher) clampNearestStopPositions(forward bool) {
	stops, positions := matcher.stops, matcher.positions
	if forward {
		position := stops[0].nearest.position
		for _, stop := range stops {
			position = max(position, stop.nearest.position)
			positions[stop.index] = position
		}
		return
	}
	position := stops[len(stops)-1].nearest.position
	for i := len(stops) - 1; i >= 0; i-- {
		stop := stops[i]
		position = min(position, stop.nearest.position)
		positions[stop.index] = position
	}
}

func (matcher *stopProjectionMatcher) projectionCostAtPosition(stop orderedStop, position float64) float64 {
	if position == stop.nearest.position {
		return stop.nearest.cost
	}
	cumulative := matcher.cumulative
	end, _ := slices.BinarySearch(cumulative, position)
	end = max(1, end)
	length := cumulative[end] - cumulative[end-1]
	ratio := 0.0
	if length > 0 {
		ratio = (position - cumulative[end-1]) / length
	}
	return stop.geometry.segment(matcher.shape[end-1], matcher.shape[end]).cost(ratio)
}
