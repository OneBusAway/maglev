package restapi

import (
	"cmp"
	"math"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

const metresPerDegree = utils.RadiusOfEarthInMeters * math.Pi / 180

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
	index    int
	geometry stopProjectionGeometry
	nearest  stopProjection
}

// Along a segment the distance to a stop is the hypotenuse of the cross-track
// error and the displacement from the unclamped closest point.
type segmentStopDistance struct {
	length, centre, crossTrack float64
}

func (stop stopProjectionGeometry) segment(from, to gtfs.ShapePoint) segmentStopDistance {
	dx := (to.Longitude - from.Longitude) * stop.lonMetres
	dy := (to.Latitude - from.Latitude) * metresPerDegree
	x := (stop.lon - from.Longitude) * stop.lonMetres
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

func (stop stopProjectionGeometry) bounds(radius float64) utils.CoordinateBounds {
	return utils.CalculateBoundsFromSpan(stop.lat, stop.lon, radius/metresPerDegree, radius/math.Abs(stop.lonMetres))
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
// trip's stop order. A slightly closer match on a return leg can otherwise
// advance the search past the correct locations of subsequent stops.
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
		stops = append(stops, orderedStop{index: i, geometry: geometry, nearest: best})
		positions[i] = best.position
	}
	// Independently closest positions already achieve the lower bound when
	// they follow trip order.
	if !inOrder {
		matchOrderedStops(positions, stops, shapePoints, cumulativeDistances, bounds)
	}
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
		boxes = append(boxes, box)
	}
	return boxes
}

// Search every run of segments that could contain a match within the radius.
// The window uses the same longitude scale as the projection. At high
// latitudes and near the date line, use the full scan. Ties retain the
// first occurrence in shape order.
func closestInBounds(boxes []utils.CoordinateBounds, stop stopProjectionGeometry, shape []gtfs.ShapePoint, cumulative []float64, radius float64) (stopProjection, bool) {
	best := stopProjection{cost: math.Inf(1)}
	if len(boxes) == 0 || math.Abs(stop.lat) > 85 || math.Abs(stop.lon) > 179 {
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
		d := stop.segment(from, shape[i+1])
		ratio := min(1, max(0, d.centre))
		cost := d.cost(ratio)
		if cost < best.cost {
			best = stopProjection{position: cumulative[i] + ratio*(cumulative[i+1]-cumulative[i]), cost: cost}
		}
	}
	return best
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
	// A feasible upper bound limits how far any stop can deviate from its
	// independent minimum. Try adjusting in each direction.
	var forwardError, backwardError float64
	forward, backward := cumulative[0], cumulative[len(cumulative)-1]
	for i, stop := range stops {
		last := stops[len(stops)-1-i]
		forward = max(forward, stop.nearest.position)
		backward = min(backward, last.nearest.position)
		forwardError += excessProjectionCost(stop, forward, shape, cumulative)
		backwardError += excessProjectionCost(last, backward, shape, cumulative)
	}
	const tolerance = 1e-6 // metres
	maxExcess := min(forwardError, backwardError) + tolerance

	assignments := []stopAssignment{{}}
	best := make([]int, len(stops)+1)
	for i := 1; i < len(best); i++ {
		best[i] = -1
	}
	segment := make([]segmentStopDistance, len(stops))
	excess := make([]float64, len(stops))
	windows := make([]utils.CoordinateBounds, len(stops))
	for i, stop := range stops {
		windows[i] = stop.geometry.bounds(stop.nearest.cost + maxExcess)
	}
	eligible := make([]int, 0, len(stops))
	var groups []stopGroup
	for i, from := range shape[:len(shape)-1] {
		if i%segmentsPerProjectionBox == 0 {
			eligible = eligible[:0]
			for j, window := range windows {
				if len(boxes) == 0 || !utils.IsOutOfBounds(boxes[i/segmentsPerProjectionBox], window) {
					eligible = append(eligible, j)
				}
			}
		}
		length := cumulative[i+1] - cumulative[i]
		if length == 0 || len(eligible) == 0 {
			continue
		}
		to := shape[i+1]
		for _, j := range eligible {
			d := stops[j].geometry.segment(from, to)
			segment[j] = d
			excess[j] = excessAtRatio(d, min(1, max(0, d.centre)), stops[j])
		}
		groups = groups[:0]
		for index, first := range eligible {
			if excess[first] > maxExcess {
				continue
			}
			d := segment[first]
			minimum := min(1, max(0, d.centre))
			groups = append(groups, stopGroup{first, first + 1, minimum, excess[first]})
			if minimum > 0 {
				if cost := excessAtRatio(d, 0, stops[first]); cost <= maxExcess {
					groups = append(groups, stopGroup{first, first + 1, 0, cost})
				}
			}
			if minimum < 1 {
				if cost := excessAtRatio(d, 1, stops[first]); cost <= maxExcess {
					groups = append(groups, stopGroup{first, first + 1, 1, cost})
				}
			}
			lower := excess[first]
			differentCentres := false
			for next := index + 1; next < len(eligible); next++ {
				last := eligible[next]
				if last-first != next-index {
					break
				}
				lower += excess[last]
				if lower > maxExcess {
					break
				}
				differentCentres = differentCentres || segment[last].centre != d.centre
				// An interior group must lie between the last and first
				// unconstrained minima. Otherwise one end stop could move
				// independently and reduce the group's error.
				lo := max(0, segment[last].centre)
				hi := min(1, d.centre)
				if !differentCentres || lo > hi || lo >= 1 || hi <= 0 {
					continue
				}
				ratio := closestStopGroup(segment[first:last+1], lo, hi)
				var cost float64
				for j := first; j <= last; j++ {
					cost += excessAtRatio(segment[j], ratio, stops[j])
				}
				if cost <= maxExcess {
					groups = append(groups, stopGroup{first, last + 1, ratio, cost})
				}
			}
		}
		slices.SortFunc(groups, func(a, b stopGroup) int {
			if a.ratio != b.ratio {
				return cmp.Compare(a.ratio, b.ratio)
			}
			return cmp.Compare(a.first, b.first)
		})
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
			if current < 0 || cost+tolerance < assignments[current].cost {
				best[group.end] = len(assignments)
				assignments = append(assignments, stopAssignment{
					previous: previous, first: group.first, end: group.end,
					position: cumulative[i] + group.ratio*length, cost: cost,
				})
			}
		}
	}
	if best[len(stops)] < 0 {
		// Keep the feasible upper-bound assignment if roundoff prevents a
		// complete path from passing the pruning bounds.
		if forwardError <= backwardError {
			for i := 1; i < len(stops); i++ {
				positions[stops[i].index] = max(positions[stops[i-1].index], positions[stops[i].index])
			}
		} else {
			for i := len(stops) - 2; i >= 0; i-- {
				positions[stops[i].index] = min(positions[stops[i+1].index], positions[stops[i].index])
			}
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

func excessAtRatio(d segmentStopDistance, ratio float64, stop orderedStop) float64 {
	return max(0, d.cost(ratio)-stop.nearest.cost)
}

func excessProjectionCost(stop orderedStop, position float64, shape []gtfs.ShapePoint, cumulative []float64) float64 {
	if position == stop.nearest.position {
		return 0
	}
	end, _ := slices.BinarySearch(cumulative, position)
	end = max(1, end)
	length := cumulative[end] - cumulative[end-1]
	ratio := 0.0
	if length > 0 {
		ratio = (position - cumulative[end-1]) / length
	}
	return excessAtRatio(stop.geometry.segment(shape[end-1], shape[end]), ratio, stop)
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
