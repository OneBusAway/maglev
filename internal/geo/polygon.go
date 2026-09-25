package geo

import "math"

// PointInPolygon reports whether (lat, lon) is inside the geometry: inside the
// exterior ring of some polygon and outside every hole of that polygon. A point
// exactly on a ring counts as inside, so zone edges are never a dead band.
func PointInPolygon(lat, lon float64, polygons [][][][2]float64) bool {
	for _, polygon := range polygons {
		if len(polygon) == 0 || !pointInRing(lat, lon, polygon[0]) {
			continue
		}
		inHole := false
		for _, hole := range polygon[1:] {
			if pointInRing(lat, lon, hole) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

// pointInRing is the even-odd ray-casting test with an explicit on-edge check.
func pointInRing(lat, lon float64, ring [][2]float64) bool {
	inside := false
	for i := range ring {
		start, end := ring[i], ring[(i+1)%len(ring)]
		if pointOnSegment(lon, lat, start, end) {
			return true
		}
		crossesLatitude := (start[1] > lat) != (end[1] > lat)
		if !crossesLatitude {
			continue
		}
		crossingLon := start[0] + (lat-start[1])*(end[0]-start[0])/(end[1]-start[1])
		if lon < crossingLon {
			inside = !inside
		}
	}
	return inside
}

// pointOnSegment reports whether (x, y) lies on the closed segment [a, b].
func pointOnSegment(x, y float64, a, b [2]float64) bool {
	cross := (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0])
	if math.Abs(cross) > 1e-12 {
		return false
	}
	return x >= math.Min(a[0], b[0]) && x <= math.Max(a[0], b[0]) &&
		y >= math.Min(a[1], b[1]) && y <= math.Max(a[1], b[1])
}

// NearestPointOnBoundary returns the haversine distance from (lat, lon) to the
// closest point on any ring of the geometry, and that point as [lon, lat]. It is
// purely geometric: callers decide what an interior point means.
func NearestPointOnBoundary(lat, lon float64, polygons [][][][2]float64) (distanceMeters, nearestLon, nearestLat float64) {
	distanceMeters = math.Inf(1)
	scaleX := metersPerDegreeLatitude * math.Cos(lat*math.Pi/180)
	scaleY := metersPerDegreeLatitude

	for _, polygon := range polygons {
		for _, ring := range polygon {
			for i := 0; i < len(ring)-1; i++ {
				candidateLon, candidateLat := closestPointOnSegment(lon, lat, ring[i], ring[i+1], scaleX, scaleY)
				if d := Distance(lat, lon, candidateLat, candidateLon); d < distanceMeters {
					distanceMeters, nearestLon, nearestLat = d, candidateLon, candidateLat
				}
			}
		}
	}
	return distanceMeters, nearestLon, nearestLat
}

// closestPointOnSegment projects (lon, lat) onto the segment [a, b] in a local
// planar frame and returns the projection in degrees.
func closestPointOnSegment(lon, lat float64, a, b [2]float64, scaleX, scaleY float64) (float64, float64) {
	px, py := (lon-a[0])*scaleX, (lat-a[1])*scaleY
	dx, dy := (b[0]-a[0])*scaleX, (b[1]-a[1])*scaleY
	lengthSquared := dx*dx + dy*dy
	t := 0.0
	if lengthSquared > 0 {
		t = math.Max(0, math.Min(1, (px*dx+py*dy)/lengthSquared))
	}
	return a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])
}

// PolygonIntersectsBounds reports whether the geometry overlaps the bounding
// box: any exterior vertex inside the box, any box corner inside the geometry,
// or any ring edge crossing a box edge.
func PolygonIntersectsBounds(polygons [][][][2]float64, bounds CoordinateBounds) bool {
	for _, polygon := range polygons {
		if len(polygon) == 0 {
			continue
		}
		for _, vertex := range polygon[0] {
			if BoundsContain(bounds, vertex[1], vertex[0]) {
				return true
			}
		}
	}

	corners := [][2]float64{
		{bounds.MinLon, bounds.MinLat}, {bounds.MaxLon, bounds.MinLat},
		{bounds.MaxLon, bounds.MaxLat}, {bounds.MinLon, bounds.MaxLat},
	}
	for _, corner := range corners {
		if PointInPolygon(corner[1], corner[0], polygons) {
			return true
		}
	}

	for _, polygon := range polygons {
		for _, ring := range polygon {
			for i := 0; i < len(ring)-1; i++ {
				for j := range corners {
					if segmentsIntersect(ring[i], ring[i+1], corners[j], corners[(j+1)%4]) {
						return true
					}
				}
			}
		}
	}
	return false
}

// segmentsIntersect reports whether segments [p1, p2] and [q1, q2] cross,
// including touching endpoints.
func segmentsIntersect(p1, p2, q1, q2 [2]float64) bool {
	o1 := orientation(p1, p2, q1)
	o2 := orientation(p1, p2, q2)
	o3 := orientation(q1, q2, p1)
	o4 := orientation(q1, q2, p2)
	if o1 != o2 && o3 != o4 {
		return true
	}
	return (o1 == 0 && pointOnSegment(q1[0], q1[1], p1, p2)) ||
		(o2 == 0 && pointOnSegment(q2[0], q2[1], p1, p2)) ||
		(o3 == 0 && pointOnSegment(p1[0], p1[1], q1, q2)) ||
		(o4 == 0 && pointOnSegment(p2[0], p2[1], q1, q2))
}

// orientation is 0 for collinear, 1 for clockwise, 2 for counter-clockwise.
func orientation(a, b, c [2]float64) int {
	cross := (b[1]-a[1])*(c[0]-b[0]) - (b[0]-a[0])*(c[1]-b[1])
	switch {
	case math.Abs(cross) < 1e-12:
		return 0
	case cross > 0:
		return 1
	default:
		return 2
	}
}
