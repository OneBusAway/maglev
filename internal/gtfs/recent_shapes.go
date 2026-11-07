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

// add stores a shape's points and evicts the least recently used shapes until
// the total fits the budget. Adding a shape that is already cached replaces
// its points. A shape larger than the whole budget is kept on its own.
func (c *recentShapes) add(shapeID string, points []gtfsdb.GetShapePointsWithDistanceRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// The query result was built by append and can have up to twice its length
	// in spare capacity. Copying it lets that array be freed, so the memory
	// kept matches the points the budget counts.
	points = slices.Clone(points)
	if element, ok := c.entries[shapeID]; ok {
		c.totalPoints -= len(element.Value.(recentShape).points)
		element.Value = recentShape{shapeID: shapeID, points: points}
		c.order.MoveToFront(element)
	} else {
		c.entries[shapeID] = c.order.PushFront(recentShape{shapeID: shapeID, points: points})
	}
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
