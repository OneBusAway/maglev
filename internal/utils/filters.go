package utils

import (
	"context"
	"maglev.onebusaway.org/internal/nulls"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

// FilterAgencies filters a list of agencies based on their presence in the provided map.
// It returns a slice of AgencyReference objects for the agencies that are present.
func FilterAgencies(all []gtfsdb.Agency, present map[string]bool) []models.AgencyReference {
	var refs []models.AgencyReference
	for _, a := range all {
		if present[a.ID] {
			refs = append(refs, models.AgencyReferenceFromDatabase(&a))
		}
	}
	return refs
}

// FilterRoutes filters a list of GTFS routes based on their presence in the provided map.
// The present map must be keyed by the combined Agency+Route ID.
func FilterRoutes(q *gtfsdb.Queries, ctx context.Context, present map[string]bool) ([]models.Route, error) {
	routes, err := q.ListRoutes(ctx)
	if err != nil {
		return nil, err
	}
	var refs []models.Route
	for _, r := range routes {
		routeIDStr := FormCombinedID(r.AgencyID, r.ID)
		if present[routeIDStr] {
			refs = append(refs, models.NewRoute(
				routeIDStr, r.AgencyID, nulls.StringOrEmpty(r.ShortName), nulls.StringOrEmpty(r.LongName),
				nulls.StringOrEmpty(r.Desc), models.RouteType(r.Type), nulls.StringOrEmpty(r.Url),
				nulls.StringOrEmpty(r.Color), nulls.StringOrEmpty(r.TextColor)))
		}
	}
	return refs, nil
}
