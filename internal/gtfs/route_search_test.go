package gtfs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRouteSearchQuery(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "SingleWord",
			input:    "airport",
			expected: `"airport"*`,
		},
		{
			name:     "MultipleWords",
			input:    "redding express",
			expected: `"redding"* AND "express"*`,
		},
		{
			name:     "InputIsLowered",
			input:    "Airport Express",
			expected: `"airport"* AND "express"*`,
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "WhitespaceOnly",
			input:    "   \t  ",
			expected: "",
		},
		{
			name:     "SpecialCharactersBang",
			input:    "route!",
			expected: `"route!"*`,
		},
		{
			name:     "SpecialCharactersAt",
			input:    "stop@here",
			expected: `"stop@here"*`,
		},
		{
			name:     "SpecialCharactersSlash",
			input:    "north/south",
			expected: `"north/south"*`,
		},
		{
			name:     "UnicodeAccented",
			input:    "café",
			expected: `"café"*`,
		},
		{
			name:     "UnicodeCJK",
			input:    "日本 電車",
			expected: `"日本"* AND "電車"*`,
		},
		{
			name:     "EmbeddedDoubleQuotes",
			input:    `the "quick" route`,
			expected: `"the"* AND """quick"""* AND "route"*`,
		},
		{
			name:     "ExtraWhitespace",
			input:    "  route   one  ",
			expected: `"route"* AND "one"*`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRouteSearchQuery(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestSearchRoutes_MatchingResults(t *testing.T) {
	ctx := t.Context()
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
	ctx := t.Context()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "zzzyyyxxx_nomatch", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Should return empty slice for non-matching query")
}

func TestSearchRoutes_EmptyInput(t *testing.T) {
	ctx := t.Context()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Empty input should short-circuit and return empty slice")
}

func TestSearchRoutes_WhitespaceOnlyInput(t *testing.T) {
	ctx := t.Context()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	routes, err := manager.SearchRoutes(ctx, "   \t  ", 20)
	require.NoError(t, err)
	assert.Empty(t, routes, "Whitespace-only input should short-circuit and return empty slice")
}

func TestSearchRoutes_DefaultLimit(t *testing.T) {
	ctx := t.Context()
	manager, _ := getSharedTestComponents(t)
	require.NotNil(t, manager)

	// Pass maxCount=0 to trigger default limit of 20
	routes, err := manager.SearchRoutes(ctx, "1", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, routes, "Default limit should still return results")
}
