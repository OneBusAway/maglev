package gtfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchRoutes_MatchingResults(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "1", 20)
	require.NoError(t, err)
	assert.NotEmpty(t, routes, "Should find routes matching short name '1'")

	// Verify returned routes contain expected fields
	for _, r := range routes {
		assert.NotEmpty(t, r.ID, "Route ID should not be empty")
		assert.NotEmpty(t, r.AgencyID, "AgencyID should not be empty")
	}
}

func TestSearchRoutes_NoMatch(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "zzzyyyxxx_nomatch", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Should return empty slice for non-matching query")
}

func TestSearchRoutes_EmptyInput(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Empty input should short-circuit and return empty slice")
}

func TestSearchRoutes_WhitespaceOnlyInput(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "   \t  ", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Whitespace-only input should short-circuit and return empty slice")
}

func TestSearchRoutes_PunctuationOnlyInput(t *testing.T) {
	// "%" tokenizes to nothing under FTS5's unicode61 tokenizer; without
	// filtering it out, the built query becomes `"%"*`, which raises a
	// syntax error at MATCH time rather than simply matching nothing.
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "%", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Punctuation-only input should short-circuit and return empty slice")
}

func TestSearchRoutes_DefaultLimit(t *testing.T) {
	ctx := context.Background()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	// Pass maxCount=0 to trigger default limit of 20
	routes, err := manager.SearchRoutes(ctx, "1", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, routes, "Default limit should still return results")
}
