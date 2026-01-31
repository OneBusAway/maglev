package utils

import (
	"context"
	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
)

// FilterAgencies filters a list of GTFS agencies based on their presence in the provided map.
// It returns a slice of AgencyReference objects for the agencies that are present.
func FilterAgencies(all []gtfs.Agency, present map[string]bool) []models.AgencyReference {
	var refs []models.AgencyReference
	for _, a := range all {
		if present[a.Id] {
			refs = append(refs, models.NewAgencyReference(
				a.Id, a.Name, a.Url, a.Timezone, a.Language, a.Phone, a.Email, a.FareUrl, "", false,
			))
		}
	}
	return refs
}

// FilterRoutes filters a list of GTFS routes based on their presence in the provided map.
// The present map must be keyed by the combined Agency+Route ID.
func FilterRoutes(q *gtfsdb.Queries, ctx context.Context, present map[string]bool) []interface{} {
	routes, err := q.ListRoutes(ctx)
	if err != nil {
		return nil
	}
	var refs []interface{}
	for _, r := range routes {
		routeIDStr := FormCombinedID(r.AgencyID, r.ID)
		if present[routeIDStr] {
			refs = append(refs, models.NewRoute(
				routeIDStr, r.AgencyID, r.ShortName.String, r.LongName.String,
				r.Desc.String, models.RouteType(r.Type), r.Url.String,
				r.Color.String, r.TextColor.String, r.ShortName.String,
			))
		}
	}
	return refs
}

func GetAllRoutesRefs(q *gtfsdb.Queries, ctx context.Context) []interface{} {
	routes, err := q.ListRoutes(ctx)
	if err != nil {
		return nil
	}
	var refs []interface{}
	for _, r := range routes {
		refs = append(refs, models.NewRoute(
			FormCombinedID(r.AgencyID, r.ID), r.AgencyID, r.ShortName.String, r.LongName.String,
			r.Desc.String, models.RouteType(r.Type), r.Url.String,
			r.Color.String, r.TextColor.String, r.ShortName.String,
		))
	}
	return refs
}
