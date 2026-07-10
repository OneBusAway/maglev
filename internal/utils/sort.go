package utils

import (
	"cmp"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

// RouteSortKey holds the fields used to order routes, decoupling the comparison
// logic from any particular gtfsdb row type.
type RouteSortKey struct {
	ShortName string
	LongName  string
	AgencyID  string
	ID        string
}

// compareRouteSortKeys compares two routes naturally by their sort name (ShortName,
// falling back to LongName), then by AgencyID, then by ID. It is the single source
// of truth for the route ordering rules.
func compareRouteSortKeys(a, b RouteSortKey) int {
	nameA := a.ShortName
	if nameA == "" {
		nameA = a.LongName
	}

	nameB := b.ShortName
	if nameB == "" {
		nameB = b.LongName
	}

	if res := NaturalCompare(nameA, nameB); res != 0 {
		return res
	}
	if a.AgencyID != b.AgencyID {
		return cmp.Compare(a.AgencyID, b.AgencyID)
	}
	return cmp.Compare(a.ID, b.ID)
}

// SortRoutesForStopRowsByName sorts gtfsdb.GetRoutesForStopRow values naturally by
// ShortName (falling back to LongName), then by AgencyID, then by ID.
func SortRoutesForStopRowsByName(routes []gtfsdb.GetRoutesForStopRow) {
	slices.SortFunc(routes, func(a, b gtfsdb.GetRoutesForStopRow) int {
		return compareRouteSortKeys(routesForStopRowSortKey(a), routesForStopRowSortKey(b))
	})
}

// SortRoutesByName sorts gtfsdb.Route values naturally by ShortName
// (falling back to LongName), then by AgencyID, then by ID.
func SortRoutesByName(routes []gtfsdb.Route) {
	slices.SortFunc(routes, func(a, b gtfsdb.Route) int {
		return compareRouteSortKeys(routeSortKey(a), routeSortKey(b))
	})
}

func routesForStopRowSortKey(r gtfsdb.GetRoutesForStopRow) RouteSortKey {
	return RouteSortKey{nulls.StringOrEmpty(r.ShortName), nulls.StringOrEmpty(r.LongName), r.AgencyID, r.ID}
}

func routeSortKey(r gtfsdb.Route) RouteSortKey {
	return RouteSortKey{nulls.StringOrEmpty(r.ShortName), nulls.StringOrEmpty(r.LongName), r.AgencyID, r.ID}
}
