package merge

import (
	"sync"
)

// Context tracks state during the merge process
type Context struct {
	// Maps entity IDs to their source feed index
	entitySources map[string]int

	// Cache for expensive computations
	cache   map[cacheKey]interface{}
	cacheMu sync.RWMutex

	// Sequence counters
	shapePointSequence int

	// Statistics
	duplicates int
	renamings  int
}

type cacheKey struct {
	feedIndex  int
	entityType string
	entityID   string
}

// NewContext creates a new merge context
func NewContext() *Context {
	return &Context{
		entitySources: make(map[string]int),
		cache:         make(map[cacheKey]interface{}),
	}
}

// MarkEntitySource records which feed an entity came from
func (c *Context) MarkEntitySource(entityID string, feedIndex int) {
	c.entitySources[entityID] = feedIndex
}

// GetEntitySource returns the feed index for an entity
func (c *Context) GetEntitySource(entityID string) (int, bool) {
	idx, ok := c.entitySources[entityID]
	return idx, ok
}

// GetCached retrieves a cached value
func (c *Context) GetCached(feedIndex int, entityType, entityID string) (interface{}, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()

	key := cacheKey{feedIndex, entityType, entityID}
	val, ok := c.cache[key]
	return val, ok
}

// SetCached stores a cached value
func (c *Context) SetCached(feedIndex int, entityType, entityID string, value interface{}) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	key := cacheKey{feedIndex, entityType, entityID}
	c.cache[key] = value
}

// ClearCache invalidates all cached data
func (c *Context) ClearCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	c.cache = make(map[cacheKey]interface{})
}

// NextShapePointSequence returns the next shape point sequence number
func (c *Context) NextShapePointSequence() int {
	c.shapePointSequence++
	return c.shapePointSequence
}

// RecordDuplicate increments the duplicate counter
func (c *Context) RecordDuplicate() {
	c.duplicates++
}

// RecordRenaming increments the renaming counter
func (c *Context) RecordRenaming() {
	c.renamings++
}

// GetStatistics returns the current merge statistics
func (c *Context) GetStatistics() (duplicates, renamings int) {
	return c.duplicates, c.renamings
}
