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
	mu          sync.Mutex
	maxPoints   int
	totalPoints int
	order       *list.List // front is most recently used
	entries     map[string]*list.Element
	stats       shapeCacheStats
}

// shapeCacheStats counts how a cache's lookups were served. A lookup that is
// neither a hit nor a read shared another lookup's read, or its read failed.
type shapeCacheStats struct {
	lookups int // calls to get
	hits    int // lookups that found their shape cached
	reads   int // shapes stored by add, one for each successful read
}

type recentShape struct {
	shapeID string
	points  []gtfsdb.GetShapePointsWithDistanceRow
}

func newRecentShapes(maxPoints int) *recentShapes {
	return &recentShapes{maxPoints: maxPoints, order: list.New(), entries: map[string]*list.Element{}}
}

// get returns a shape's points if they are cached, and counts the lookup.
func (c *recentShapes) get(shapeID string) ([]gtfsdb.GetShapePointsWithDistanceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.lookups++
	points, ok := c.findLocked(shapeID)
	if ok {
		c.stats.hits++
	}
	return points, ok
}

// peek is get without counting, for checking again inside a lookup that get
// has already counted.
func (c *recentShapes) peek(shapeID string) ([]gtfsdb.GetShapePointsWithDistanceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.findLocked(shapeID)
}

func (c *recentShapes) findLocked(shapeID string) ([]gtfsdb.GetShapePointsWithDistanceRow, bool) {
	element, ok := c.entries[shapeID]
	if !ok {
		return nil, false
	}
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
	c.stats.reads++
	for c.totalPoints > c.maxPoints && c.order.Len() > 1 {
		oldest := c.order.Remove(c.order.Back()).(recentShape)
		delete(c.entries, oldest.shapeID)
		c.totalPoints -= len(oldest.points)
	}
}

// lookupStats returns the lookup counts so far.
func (c *recentShapes) lookupStats() shapeCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}
