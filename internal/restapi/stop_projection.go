package restapi

import (
	"cmp"
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

// projectStopsInSequence returns each stop's distance-along-trip in metres,
// projecting the stop's lat/lon onto the shape polyline. The publisher's
// stop_times.shape_dist_traveled is deliberately ignored — Java's
// StopTimeEntriesFactory.ensureStopTimesHaveShapeDistanceTraveledSet does
// the same, overwriting the publisher value at load time with the projected
// value from DistanceAlongShapeLibrary. Publisher-supplied values are often
// wrong (bad units, corrupt data on some feeds), so both codebases treat
// projection as authoritative.
//
// Choose the lowest combined projection error among matches that follow the
// trip's stop order. For consecutive visits to the same stop, prefer distinct
// positions when an equally close match fits the surrounding assignments.
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
	bounds := projectionBounds(shapePoints, len(stopIDs))
	var scratch [128]orderedStop
	stops := scratch[:0]
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
		best := closestStopProjection(bounds, geometry, shapePoints, cumulativeDistances)
		if len(stops) > 0 && best.position < stops[len(stops)-1].nearest.position {
			inOrder = false
		}
		stops = append(stops, orderedStop{
			index:           i,
			geometry:        geometry,
			nearest:         best,
			repeatsPrevious: i > 0 && stopID == stopIDs[i-1],
		})
		positions[i] = best.position
	}
	// Independently closest positions already achieve the lower bound when
	// they follow trip order.
	if !inOrder && !matchNearestStops(positions, stops, shapePoints, cumulativeDistances) {
		matchOrderedStops(positions, stops, shapePoints, cumulativeDistances, bounds)
	}
	separateRepeatedStopVisits(positions, stops, shapePoints, cumulativeDistances)
	return positions
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
func closestInBounds(boxes []utils.CoordinateBounds, stop stopProjectionGeometry, shape []gtfs.ShapePoint, cumulative []float64, radius float64) (stopProjection, bool) {
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
		end := min(i*segmentsPerProjectionBox+1, len(shape))
		candidate := scanStopProjection(stop, shape[start:end], cumulative[start:end])
		if candidate.cost < best.cost {
			best = candidate
		}
	}
	return best, best.cost <= radius
}

func closestStopProjection(bounds []utils.CoordinateBounds, stop stopProjectionGeometry, shape []gtfs.ShapePoint, cumulative []float64) stopProjection {
	if best, ok := closestInBounds(bounds, stop, shape, cumulative, 50); ok {
		return best
	}
	if len(bounds) > 0 {
		if best, ok := closestInBounds(bounds, stop, shape, cumulative, 500); ok {
			return best
		}
	}
	return scanStopProjection(stop, shape, cumulative)
}

func scanStopProjection(stop stopProjectionGeometry, shape []gtfs.ShapePoint, cumulative []float64) stopProjection {
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
func matchNearestStops(positions []float64, stops []orderedStop, shape []gtfs.ShapePoint, cumulative []float64) bool {
	previous, end := cumulative[0], cumulative[len(cumulative)-1]
	tolerance := stopProjectionTolerance / float64(len(stops))
	for i, stop := range stops {
		position := stop.nearest.position
		if position < previous {
			var ok bool
			position, ok = firstMatchingProjection(stop.geometry, shape, cumulative, previous, end, stop.nearest.cost+tolerance)
			if !ok {
				// Finish the feasible prefix at its last position to bound the solver.
				for _, remaining := range stops[i:] {
					positions[remaining.index] = previous
				}
				return false
			}
		}
		positions[stop.index] = position
		previous = position
	}
	return true
}

// Search each segment's closest point in shape order. Repeated visits start
// beyond the preceding position so a shared vertex counts only once.
func firstMatchingProjection(stop stopProjectionGeometry, shape []gtfs.ShapePoint, cumulative []float64, minPosition, maxPosition, maxCost float64) (float64, bool) {
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

func separateRepeatedStopVisits(positions []float64, stops []orderedStop, shape []gtfs.ShapePoint, cumulative []float64) {
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
		limit := cumulative[len(cumulative)-1]
		if end < len(stops) {
			limit = positions[stops[end].index]
			if stops[end].repeatsPrevious {
				limit -= stopProjectionTolerance
			}
		}
		start := cumulative[0]
		if i >= 2 {
			start = positions[stops[i-2].index]
			if stops[i-1].repeatsPrevious {
				start += stopProjectionTolerance
			}
		}
		maxCost := projectionCostAtPosition(stops[i], position, shape, cumulative) + stopProjectionTolerance
		if earlier, ok := firstMatchingProjection(stops[i].geometry, shape, cumulative, start, position-stopProjectionTolerance, maxCost); ok {
			position = earlier
			positions[stops[i-1].index] = earlier
		}
		spreadRepeatedStopVisits(positions, stops[i:end], shape, cumulative, position, limit, maxCost)
		i = end
	}
}

func spreadRepeatedStopVisits(positions []float64, repeats []orderedStop, shape []gtfs.ShapePoint, cumulative []float64, position, limit, maxCost float64) {
	stop := repeats[0]
	for i, visit := range repeats {
		next, ok := firstMatchingProjection(stop.geometry, shape, cumulative, position+stopProjectionTolerance, limit, maxCost)
		if !ok {
			for _, remaining := range repeats[i:] {
				positions[remaining.index] = position
			}
			return
		}
		positions[visit.index] = next
		position = next
	}
}

type stopGroup struct {
	first, end  int
	ratio, cost float64
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
func matchOrderedStops(positions []float64, stops []orderedStop, shape []gtfs.ShapePoint, cumulative []float64, boxes []utils.CoordinateBounds) {
	forwardError, backwardError := orderedProjectionErrorBounds(stops, shape, cumulative)
	var prefixError float64
	for _, stop := range stops {
		prefixError += max(0, projectionCostAtPosition(stop, positions[stop.index], shape, cumulative)-stop.nearest.cost)
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
			search.selectStops(boxes, i/segmentsPerProjectionBox)
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
			clampNearestStopPositions(positions, stops, forwardError <= backwardError)
		}
		return
	}
	for index := best[len(stops)]; index > 0; {
		assignment := assignments[index]
		for _, stop := range stops[assignment.first:assignment.end] {
			positions[stop.index] = assignment.position
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
func orderedProjectionErrorBounds(stops []orderedStop, shape []gtfs.ShapePoint, cumulative []float64) (float64, float64) {
	var forwardError, backwardError float64
	forward, backward := cumulative[0], cumulative[len(cumulative)-1]
	for i, stop := range stops {
		last := stops[len(stops)-1-i]
		forward = max(forward, stop.nearest.position)
		backward = min(backward, last.nearest.position)
		forwardError += max(0, projectionCostAtPosition(stop, forward, shape, cumulative)-stop.nearest.cost)
		backwardError += max(0, projectionCostAtPosition(last, backward, shape, cumulative)-last.nearest.cost)
	}
	return forwardError, backwardError
}

func clampNearestStopPositions(positions []float64, stops []orderedStop, forward bool) {
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

// Stop group generation reuses scratch space as the solver advances along the shape.
type stopGroupSearch struct {
	maxExcess float64
	windows   []utils.CoordinateBounds
	eligible  []int
	segments  []segmentStopDistance
	excess    []float64
	groups    []stopGroup
}

func newStopGroupSearch(stops []orderedStop, maxExcess float64) stopGroupSearch {
	windows := make([]utils.CoordinateBounds, len(stops))
	for i, stop := range stops {
		windows[i] = stop.geometry.bounds(stop.nearest.cost + maxExcess)
	}
	return stopGroupSearch{
		maxExcess: maxExcess,
		windows:   windows,
		eligible:  make([]int, 0, len(stops)),
		segments:  make([]segmentStopDistance, len(stops)),
		excess:    make([]float64, len(stops)),
	}
}

func (search *stopGroupSearch) selectStops(boxes []utils.CoordinateBounds, boxIndex int) {
	search.eligible = search.eligible[:0]
	for i, window := range search.windows {
		if len(boxes) == 0 || !utils.IsOutOfBounds(boxes[boxIndex], window) {
			search.eligible = append(search.eligible, i)
		}
	}
}

func (search *stopGroupSearch) candidates(stops []orderedStop, from, to gtfs.ShapePoint) []stopGroup {
	search.groups = search.groups[:0]
	hasNonlinearCost := false
	for _, i := range search.eligible {
		segment := stops[i].geometry.segment(from, to)
		search.segments[i] = segment
		search.excess[i] = segment.excessAtRatio(segment.closestRatio(), stops[i].nearest.cost)
		hasNonlinearCost = hasNonlinearCost || !segment.hasLinearCost()
	}
	for index, first := range search.eligible {
		if search.excess[first] > search.maxExcess {
			continue
		}
		search.addSingleStopGroups(first, stops[first].nearest.cost)
		// If every cost is linear on this segment, an ordered minimum
		// exists at the endpoints. Single-stop groups already cover it.
		if hasNonlinearCost && index+1 < len(search.eligible) {
			search.addSharedStopGroups(stops, search.eligible[index:])
		}
	}
	slices.SortFunc(search.groups, func(a, b stopGroup) int {
		return cmp.Or(cmp.Compare(a.ratio, b.ratio), cmp.Compare(a.first, b.first))
	})
	return search.groups
}

func (search *stopGroupSearch) addSingleStopGroups(index int, nearestCost float64) {
	segment := search.segments[index]
	minimum := segment.closestRatio()
	search.groups = append(search.groups, stopGroup{index, index + 1, minimum, search.excess[index]})
	for _, ratio := range [...]float64{0, 1} {
		if ratio == minimum {
			continue
		}
		if cost := segment.excessAtRatio(ratio, nearestCost); cost <= search.maxExcess {
			search.groups = append(search.groups, stopGroup{index, index + 1, ratio, cost})
		}
	}
}

func (search *stopGroupSearch) addSharedStopGroups(stops []orderedStop, indices []int) {
	first := indices[0]
	firstCentre := search.segments[first].centre
	lowerBound := search.excess[first]
	differentCentres := false
	for offset, last := range indices[1:] {
		if last != first+offset+1 {
			break
		}
		lowerBound += search.excess[last]
		if (last-first)%2 == 1 {
			// At a shared ratio, the sum of two along-track errors is at
			// least the distance between their centres at the smaller scale.
			// Disjoint pairs can contribute independent lower bounds.
			a, b := search.segments[last-1], search.segments[last]
			pair := math.Abs(a.centre-b.centre)*min(a.length, b.length) - stops[last-1].nearest.cost - stops[last].nearest.cost
			lowerBound += max(0, pair-search.excess[last-1]-search.excess[last])
		}
		if lowerBound > search.maxExcess {
			break
		}
		lastCentre := search.segments[last].centre
		differentCentres = differentCentres || lastCentre != firstCentre
		// An interior group must lie between the last and first minima.
		// Outside that range, one end stop could move to reduce the error.
		lo, hi := max(0, lastCentre), min(1, firstCentre)
		if !differentCentres || lo > hi || lo >= 1 || hi <= 0 {
			continue
		}
		ratio := closestStopGroup(search.segments[first:last+1], lo, hi)
		if cost := search.groupCost(stops, first, last+1, ratio); cost <= search.maxExcess {
			search.groups = append(search.groups, stopGroup{first, last + 1, ratio, cost})
		}
	}
}

func (search *stopGroupSearch) groupCost(stops []orderedStop, first, end int, ratio float64) float64 {
	var cost float64
	for i := first; i < end; i++ {
		cost += search.segments[i].excessAtRatio(ratio, stops[i].nearest.cost)
	}
	return cost
}

func projectionCostAtPosition(stop orderedStop, position float64, shape []gtfs.ShapePoint, cumulative []float64) float64 {
	if position == stop.nearest.position {
		return stop.nearest.cost
	}
	end, _ := slices.BinarySearch(cumulative, position)
	end = max(1, end)
	length := cumulative[end] - cumulative[end-1]
	ratio := 0.0
	if length > 0 {
		ratio = (position - cumulative[end-1]) / length
	}
	return stop.geometry.segment(shape[end-1], shape[end]).cost(ratio)
}

// The sum of distances on a segment is convex. Bisect its derivative to
// find the shared minimizer, including the zero-cross-track median case.
func closestStopGroup(segment []segmentStopDistance, lo, hi float64) float64 {
	if lo == hi {
		return lo
	}
	const iterations = 40
	for i := 0; i < iterations; i++ {
		mid := (lo + hi) / 2
		var derivative float64
		for _, d := range segment {
			cost := d.cost(mid)
			if cost > 0 {
				derivative += (mid - d.centre) * d.length * d.length / cost
			}
		}
		if derivative < 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
