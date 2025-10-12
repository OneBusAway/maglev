package merge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContext_EntitySource(t *testing.T) {
	ctx := NewContext()

	// Mark entity source
	ctx.MarkEntitySource("stop1", 0)
	ctx.MarkEntitySource("stop2", 1)

	// Get entity source
	idx, ok := ctx.GetEntitySource("stop1")
	assert.True(t, ok)
	assert.Equal(t, 0, idx)

	idx, ok = ctx.GetEntitySource("stop2")
	assert.True(t, ok)
	assert.Equal(t, 1, idx)

	// Unknown entity
	_, ok = ctx.GetEntitySource("stop3")
	assert.False(t, ok)
}

func TestContext_Cache(t *testing.T) {
	ctx := NewContext()

	// Initially empty
	_, ok := ctx.GetCached(0, "stop", "stop1")
	assert.False(t, ok)

	// Set cached value
	ctx.SetCached(0, "stop", "stop1", "cached_data")

	// Get cached value
	val, ok := ctx.GetCached(0, "stop", "stop1")
	assert.True(t, ok)
	assert.Equal(t, "cached_data", val)

	// Clear cache
	ctx.ClearCache()

	// Should be empty again
	_, ok = ctx.GetCached(0, "stop", "stop1")
	assert.False(t, ok)
}

func TestContext_ShapePointSequence(t *testing.T) {
	ctx := NewContext()

	seq1 := ctx.NextShapePointSequence()
	seq2 := ctx.NextShapePointSequence()
	seq3 := ctx.NextShapePointSequence()

	assert.Equal(t, 1, seq1)
	assert.Equal(t, 2, seq2)
	assert.Equal(t, 3, seq3)
}

func TestContext_Statistics(t *testing.T) {
	ctx := NewContext()

	// Initially zero
	duplicates, renamings := ctx.GetStatistics()
	assert.Equal(t, 0, duplicates)
	assert.Equal(t, 0, renamings)

	// Record some operations
	ctx.RecordDuplicate()
	ctx.RecordDuplicate()
	ctx.RecordRenaming()

	// Check statistics
	duplicates, renamings = ctx.GetStatistics()
	assert.Equal(t, 2, duplicates)
	assert.Equal(t, 1, renamings)
}
