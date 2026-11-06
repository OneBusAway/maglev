package gtfs

import (
	"container/list"
	"slices"
	"sync"

	"maglev.onebusaway.org/gtfsdb"
)

// recentShapes keeps the points of the most recently used shapes, up to a
// total number of points. Precomputing directions reads the same shape for
// every stop on it, so a small cache saves most of the reads without holding
// a whole feed's shapes in memory. The budget counts points rather than
// shapes, because shape sizes vary by orders of magnitude between feeds.
type recentShapes struct {
	mu           sync.Mutex
	maxPoints    int
	totalPoints  int
	order        *list.List // front is most recently used
	entries      map[string]*list.Element
	hits, misses int
}

type recentShape struct {
	shapeID string
	points  []gtfsdb.GetShapePointsWithDistanceRow
}

func newRecentShapes(maxPoints int) *recentShapes {
	return &recentShapes{maxPoints: maxPoints, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *recentShapes) get(shapeID string) ([]gtfsdb.GetShapePointsWithDistanceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[shapeID]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.order.MoveToFront(element)
	return element.Value.(recentShape).points, true
}

// add stores a shape that isn't cached yet and evicts the least recently used
// ones until the total fits the budget. A shape larger than the whole budget
// is kept on its own.
func (c *recentShapes) add(shapeID string, points []gtfsdb.GetShapePointsWithDistanceRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	points = slices.Clip(points)
	c.entries[shapeID] = c.order.PushFront(recentShape{shapeID: shapeID, points: points})
	c.totalPoints += len(points)
	for c.totalPoints > c.maxPoints && c.order.Len() > 1 {
		oldest := c.order.Remove(c.order.Back()).(recentShape)
		delete(c.entries, oldest.shapeID)
		c.totalPoints -= len(oldest.points)
	}
}

// stats returns how many lookups found their shape cached and how many didn't.
func (c *recentShapes) stats() (hits, misses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}
