package gtfs

import (
	"context"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
)

// buildRouteSearchQuery normalizes user input into an FTS5-safe prefix search query.
func buildRouteSearchQuery(input string) string {
	terms := strings.Fields(strings.ToLower(input))
	safeTerms := make([]string, 0, len(terms))

	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			continue
		}
		escaped := strings.ReplaceAll(trimmed, `"`, `""`)
		safeTerms = append(safeTerms, `"`+escaped+`"*`)
	}

	if len(safeTerms) == 0 {
		return ""
	}

	return strings.Join(safeTerms, " AND ")
}

// SearchRoutes performs a full text search against routes using SQLite FTS5.
func (manager *Manager) SearchRoutes(ctx context.Context, input string, maxCount int) ([]gtfsdb.Route, error) {
	limit := maxCount
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	query := buildRouteSearchQuery(input)
	return manager.GtfsDB.Queries.SearchRoutesByFullText(ctx, gtfsdb.SearchRoutesByFullTextParams{
		Query: query,
		Limit: int64(limit),
	})
}
