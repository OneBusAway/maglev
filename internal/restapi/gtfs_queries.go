package restapi

import (
	"context"

	"maglev.onebusaway.org/gtfsdb"
)

// gtfsQueries returns the request-scoped query set when one is installed, or
// the shared GTFS query set for handlers that do not need a read transaction.
func (api *RestAPI) gtfsQueries(ctx context.Context) *gtfsdb.Queries {
	if queries := gtfsdb.QueriesFromContext(ctx); queries != nil {
		return queries
	}
	return api.GtfsManager.GtfsDB.Queries
}
