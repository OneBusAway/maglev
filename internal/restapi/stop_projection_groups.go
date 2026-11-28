package restapi

import (
	"cmp"
	"math"
	"slices"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/utils"
)

type stopGroup struct {
	first, end  int
	ratio, cost float64
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
