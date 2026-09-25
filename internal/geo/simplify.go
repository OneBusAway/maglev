package geo

import "math"

const (
	// SimplifyInitialToleranceMeters is the first Douglas–Peucker tolerance tried
	// for display geometry; it doubles until every ring fits SimplifyMaxRingPoints.
	SimplifyInitialToleranceMeters = 10.0
	// SimplifyMaxRingPoints bounds the vertices per ring of display geometry. The
	// bound wins over the tolerance target: Alexandria's zone needs ~160 m.
	SimplifyMaxRingPoints = 256
	// minClosedRingPoints is three distinct vertices plus the closing vertex.
	minClosedRingPoints = 4
	// metersPerDegreeLatitude is the local planar scale for Y (and, cosine-scaled, X).
	metersPerDegreeLatitude = 111320.0
	// simplifyMaxDoublings caps the tolerance search. 10 m × 2^40 far exceeds
	// Earth's circumference, so real data never reaches it; it only guards
	// against a future regression that stops a ring from shrinking.
	simplifyMaxDoublings = 40
)

// SimplifiedPolygons is the result of SimplifyPolygons.
type SimplifiedPolygons struct {
	Polygons        [][][][2]float64
	ToleranceMeters float64
	// Changed is false when no ring lost a vertex, so callers can store NULL.
	Changed bool
}

// SimplifyPolygons produces display geometry: Douglas–Peucker per ring, run on
// the open ring split at the vertex farthest from vertex 0 and re-closed after,
// starting at 10 m and doubling until every ring has at most
// SimplifyMaxRingPoints vertices. Holes that collapse below four points are
// dropped; the exterior ring is never dropped; vertex order (winding) is kept.
//
// Termination for rings of coincident vertices comes from simplifyRing
// collapsing them to a closed pair; simplifyMaxDoublings is only a backstop,
// and the last pass is returned if it is ever reached.
func SimplifyPolygons(polygons [][][][2]float64) SimplifiedPolygons {
	tolerance := SimplifyInitialToleranceMeters
	for doublings := 0; ; doublings++ {
		simplified, largestRing := simplifyAtTolerance(polygons, tolerance)
		fitsBound := largestRing <= SimplifyMaxRingPoints
		if fitsBound || doublings == simplifyMaxDoublings {
			return SimplifiedPolygons{
				Polygons:        simplified,
				ToleranceMeters: tolerance,
				Changed:         ringsChanged(polygons, simplified),
			}
		}
		tolerance *= 2
	}
}

// simplifyAtTolerance simplifies every ring at one tolerance and reports the
// largest resulting ring size.
func simplifyAtTolerance(polygons [][][][2]float64, toleranceMeters float64) ([][][][2]float64, int) {
	largest := 0
	result := make([][][][2]float64, 0, len(polygons))
	for _, polygon := range polygons {
		rings := make([][][2]float64, 0, len(polygon))
		for ringIndex, ring := range polygon {
			simplified := simplifyRing(ring, toleranceMeters)
			isHole := ringIndex > 0
			if isHole && len(simplified) < minClosedRingPoints {
				// A hole that degenerates to a line would invert the map fill.
				continue
			}
			rings = append(rings, simplified)
			largest = max(largest, len(simplified))
		}
		result = append(result, rings)
	}
	return result, largest
}

// ringsChanged reports whether any ring of the result has a different vertex
// count from the original. Comparing counts is sufficient because
// Douglas–Peucker only ever removes vertices, never moves them.
func ringsChanged(original, simplified [][][][2]float64) bool {
	for p := range original {
		if len(original[p]) != len(simplified[p]) {
			return true
		}
		for r := range original[p] {
			if len(original[p][r]) != len(simplified[p][r]) {
				return true
			}
		}
	}
	return false
}

// simplifyRing runs Douglas–Peucker on one closed ring. Standard DP degenerates
// when first == last (the chord has zero length), so the ring is split at the
// vertex farthest from vertex 0, each half is simplified as an open polyline,
// and the two halves are joined and re-closed.
func simplifyRing(ring [][2]float64, toleranceMeters float64) [][2]float64 {
	if len(ring) < minClosedRingPoints {
		return ring
	}
	open := ring
	if ring[0] == ring[len(ring)-1] {
		open = ring[:len(ring)-1]
	}

	scaleX, scaleY := metersPerDegree(open)
	farthest := farthestVertexFrom(open, 0, scaleX, scaleY)
	if farthest == 0 {
		// Every vertex coincides with vertex 0: collapse to a closed pair.
		return [][2]float64{ring[0], ring[0]}
	}

	firstHalf := douglasPeucker(open[:farthest+1], toleranceMeters, scaleX, scaleY)
	secondHalfInput := append(append([][2]float64{}, open[farthest:]...), open[0])
	secondHalf := douglasPeucker(secondHalfInput, toleranceMeters, scaleX, scaleY)

	// firstHalf ends at open[farthest]; secondHalf starts there and ends at open[0],
	// so the join is already closed. firstHalf may alias the caller's ring, so the
	// join is built in a fresh slice.
	joined := make([][2]float64, 0, len(firstHalf)+len(secondHalf)-1)
	joined = append(joined, firstHalf...)
	return append(joined, secondHalf[1:]...)
}

// douglasPeucker keeps the endpoints and, recursively, every vertex farther than
// toleranceMeters from the chord of its span.
func douglasPeucker(points [][2]float64, toleranceMeters, scaleX, scaleY float64) [][2]float64 {
	if len(points) < 3 {
		return points
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true

	type span struct{ start, end int }
	stack := []span{{0, len(points) - 1}}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		farthest, farthestDistance := -1, 0.0
		for i := current.start + 1; i < current.end; i++ {
			distance := pointToSegmentMeters(points[i], points[current.start], points[current.end], scaleX, scaleY)
			if distance > farthestDistance {
				farthest, farthestDistance = i, distance
			}
		}
		if farthest >= 0 && farthestDistance > toleranceMeters {
			keep[farthest] = true
			stack = append(stack, span{current.start, farthest}, span{farthest, current.end})
		}
	}

	kept := make([][2]float64, 0, len(points))
	for i, point := range points {
		if keep[i] {
			kept = append(kept, point)
		}
	}
	return kept
}

// farthestVertexFrom returns the index of the vertex farthest from ring[from].
func farthestVertexFrom(ring [][2]float64, from int, scaleX, scaleY float64) int {
	farthest, farthestDistance := from, 0.0
	for i, vertex := range ring {
		dx := (vertex[0] - ring[from][0]) * scaleX
		dy := (vertex[1] - ring[from][1]) * scaleY
		if distance := dx*dx + dy*dy; distance > farthestDistance {
			farthest, farthestDistance = i, distance
		}
	}
	return farthest
}

// metersPerDegree returns the local planar scale (x for longitude, y for
// latitude) at the ring's mean latitude. Rings straddling the antimeridian are
// not handled.
func metersPerDegree(ring [][2]float64) (scaleX, scaleY float64) {
	if len(ring) == 0 {
		return metersPerDegreeLatitude, metersPerDegreeLatitude
	}
	latitudeSum := 0.0
	for _, vertex := range ring {
		latitudeSum += vertex[1]
	}
	meanLatitude := latitudeSum / float64(len(ring))
	return metersPerDegreeLatitude * math.Cos(meanLatitude*math.Pi/180), metersPerDegreeLatitude
}

// pointToSegmentMeters is the planar distance from point to the segment
// [segmentStart, segmentEnd], with degrees scaled to metres by scaleX/scaleY.
func pointToSegmentMeters(point, segmentStart, segmentEnd [2]float64, scaleX, scaleY float64) float64 {
	px, py := (point[0]-segmentStart[0])*scaleX, (point[1]-segmentStart[1])*scaleY
	dx, dy := (segmentEnd[0]-segmentStart[0])*scaleX, (segmentEnd[1]-segmentStart[1])*scaleY
	t := clampedProjection(px, py, dx, dy)
	return math.Hypot(px-t*dx, py-t*dy)
}
