package gtfs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

// buildRouteSearchQuery normalizes user input into an FTS5-safe prefix search query.
// Terms are ORed together to match legacy Java's default Lucene operator
// (QueryParserBase defaults to OR_OPERATOR, and RouteCollectionSearchServiceImpl
// never overrides it), so a query where only some terms match still returns those
// partial matches instead of nothing.
func buildRouteSearchQuery(input string) string {
	terms := strings.Fields(strings.ToLower(input))
	safeTerms := make([]string, 0, len(terms))

	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		// FTS5 tokenizes punctuation-only terms (e.g. "%") to nothing, which
		// raises a syntax error at query time rather than just matching nothing.
		if trimmed == "" || !utils.ContainsLetterOrDigit(trimmed) {
			continue
		}
		escaped := strings.ReplaceAll(trimmed, `"`, `""`)
		safeTerms = append(safeTerms, `"`+escaped+`"*`)
	}

	if len(safeTerms) == 0 {
		return ""
	}

	return strings.Join(safeTerms, " OR ")
}

// SearchRoutes performs a full text search against routes using SQLite FTS5.
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
