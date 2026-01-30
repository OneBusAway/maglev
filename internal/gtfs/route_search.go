package gtfs

import (
	"context"
	"fmt"
	"log/slog"
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
// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) SearchRoutes(ctx context.Context, input string, maxCount int) ([]gtfsdb.Route, error) {
	limit := maxCount
	if limit <= 0 {
		limit = 20
	}

	query := buildRouteSearchQuery(input)
	if query == "" {
		return []gtfsdb.Route{}, nil
	}

	logger := slog.Default().With(slog.String("component", "route_search"))
	logger.Debug("route search", slog.String("input", input), slog.String("query", query), slog.Int("limit", limit))

	routes, err := manager.GtfsDB.Queries.SearchRoutesByFullText(ctx, gtfsdb.SearchRoutesByFullTextParams{
		Query: query,
		Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("route search failed for query %q: %w", query, err)
	}
	return routes, nil
}
