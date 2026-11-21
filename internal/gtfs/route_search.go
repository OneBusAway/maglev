package gtfs

import (
	"context"
	"log/slog"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/logging"
)

// SearchRoutes performs a full text search against routes.
// Terms are ORed together to match legacy Java's default Lucene operator
// (QueryParserBase defaults to OR_OPERATOR, and RouteCollectionSearchServiceImpl
// never overrides it).
func (manager *Manager) SearchRoutes(ctx context.Context, input string, maxCount int) ([]gtfsdb.Route, error) {
	limit := maxCount
	if limit <= 0 {
		limit = 20
	}

	reqLogger := logging.ForComponent(ctx, "route_search")
	reqLogger.Debug("route search", slog.String("input", input), slog.Int("limit", limit))

	return manager.GtfsDB.Queries.SearchRoutes(ctx, gtfsdb.SearchRequest{
		Query:       input,
		Operator:    gtfsdb.SearchOperatorOr,
		PrefixMatch: true,
		Limit:       int64(limit),
	})
}
