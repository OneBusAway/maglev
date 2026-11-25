package gtfsdb

import "context"

type queriesContextKey struct{}

// WithQueries returns a context that carries a request-scoped query set.
// It is useful when related database reads must share one transaction.
func WithQueries(ctx context.Context, queries *Queries) context.Context {
	return context.WithValue(ctx, queriesContextKey{}, queries)
}

// QueriesFromContext returns the query set installed by WithQueries, if any.
func QueriesFromContext(ctx context.Context) *Queries {
	queries, _ := ctx.Value(queriesContextKey{}).(*Queries)
	return queries
}
