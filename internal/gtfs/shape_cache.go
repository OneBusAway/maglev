package gtfs

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/OneBusAway/go-gtfs"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// CachedShape is a shape's points in sequence order with the distance along
// the shape to each point. CumulativeDistances and TotalDistance are set only
// for a shape with at least two points. The slices are shared by every
// request and must never be written, appended to or sorted.
type CachedShape struct {
	Points              []gtfs.ShapePoint
	CumulativeDistances []float64
	TotalDistance       float64
}

// shapeCache holds the shapes read since the database's last import. A zero
// shapeCache is ready to use.
type shapeCache struct {
	current atomic.Pointer[shapeCacheGeneration]
}

// shapeCacheGeneration holds the shapes of one import. A shape with no points
// is stored too, so a trip whose shape is missing does not query it again.
type shapeCacheGeneration struct {
	importGeneration uint64
	shapes           sync.Map // shape ID -> CachedShape
}

// generationFor returns the generation for importGeneration, replacing the
// current one when an import has made its rows stale.
func (c *shapeCache) generationFor(importGeneration uint64) *shapeCacheGeneration {
	for {
		current := c.current.Load()
		if current != nil && current.importGeneration == importGeneration {
			return current
		}
		next := &shapeCacheGeneration{importGeneration: importGeneration}
		if c.current.CompareAndSwap(current, next) {
			return next
		}
	}
}

func (c *shapeCache) reset() {
	c.current.Store(nil)
}

func (g *shapeCacheGeneration) load(shapeID string) (CachedShape, bool) {
	shape, ok := g.shapes.Load(shapeID)
	if !ok {
		return CachedShape{}, false
	}
	return shape.(CachedShape), true
}

func (g *shapeCacheGeneration) store(shapeID string, shape CachedShape) {
	g.shapes.Store(shapeID, shape)
}

// ShapesByID returns the points and distances of each shape, keyed by shape
// ID. A shape with no points is absent, and empty or repeated IDs are ignored.
// Shapes are read from the database once per import and shared after that;
// a failed read is returned and leaves nothing cached.
func (manager *Manager) ShapesByID(ctx context.Context, shapeIDs []string) (map[string]CachedShape, error) {
	// Load the generation once: a read that overlaps an import then stores
	// into the generation it began with, which the import has retired.
	generation := manager.shapes.generationFor(manager.GtfsDB.ImportGeneration())

	shapes := make(map[string]CachedShape, len(shapeIDs))
	missing := make([]string, 0, len(shapeIDs))
	seen := make(map[string]struct{}, len(shapeIDs))
	for _, shapeID := range shapeIDs {
		if _, dup := seen[shapeID]; dup || shapeID == "" {
			continue
		}
		seen[shapeID] = struct{}{}
		if shape, ok := generation.load(shapeID); ok {
			if len(shape.Points) > 0 {
				shapes[shapeID] = shape
			}
			continue
		}
		missing = append(missing, shapeID)
	}
	if len(missing) == 0 {
		return shapes, nil
	}

	pointsByShapeID, err := manager.readShapePoints(ctx, missing)
	if err != nil {
		return nil, err
	}
	for _, shapeID := range missing {
		shape := newCachedShape(pointsByShapeID[shapeID])
		generation.store(shapeID, shape)
		if len(shape.Points) > 0 {
			shapes[shapeID] = shape
		}
	}
	return shapes, nil
}

// readShapePoints reads the points of each shape in shapeIDs, each in a slice
// of exactly its length, so a caller's append can never write into memory
// another request reads. It reads GtfsDB.Queries on every call rather than
// keeping it, since tests swap it.
func (manager *Manager) readShapePoints(ctx context.Context, shapeIDs []string) (map[string][]gtfs.ShapePoint, error) {
	rows, err := utils.QueryInBatches(ctx, shapeIDs, manager.GtfsDB.Queries.GetShapePointsByIDs)
	if err != nil {
		return nil, err
	}
	pointsByShapeID := make(map[string][]gtfs.ShapePoint, len(shapeIDs))
	// Rows arrive ordered by shape ID, so each shape is one contiguous run.
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].ShapeID == rows[start].ShapeID {
			end++
		}
		pointsByShapeID[rows[start].ShapeID] = shapePointsFromRows(rows[start:end])
		start = end
	}
	return pointsByShapeID, nil
}

func shapePointsFromRows(rows []gtfsdb.GetShapePointsByIDsRow) []gtfs.ShapePoint {
	points := make([]gtfs.ShapePoint, len(rows))
	for i, row := range rows {
		points[i] = gtfs.ShapePoint{Latitude: row.Lat, Longitude: row.Lon}
	}
	return points
}

// newCachedShape measures a shape's points once.
func newCachedShape(points []gtfs.ShapePoint) CachedShape {
	shape := CachedShape{Points: points}
	if len(points) >= 2 {
		shape.CumulativeDistances = utils.CumulativeShapeDistances(points)
		shape.TotalDistance = shape.CumulativeDistances[len(shape.CumulativeDistances)-1]
	}
	return shape
}

// ResetStaticCaches drops every cached static row, so the next lookups read
// the database again. Imports invalidate the caches on their own; this is for
// tests that count or fail queries against a manager other tests share.
func (manager *Manager) ResetStaticCaches() {
	manager.shapes.reset()
}
