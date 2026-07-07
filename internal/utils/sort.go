package utils

import (
	"cmp"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
)

// routeSortKey holds the fields used to order routes, decoupling the comparison
// logic from any particular gtfsdb row type.
type routeSortKey struct {
	shortName string
	longName  string
	agencyID  string
	id        string
}

// compareRouteSortKeys compares two routes naturally by their sort name (ShortName,
// falling back to LongName), then by AgencyID, then by ID. It is the single source
// of truth for the ordering rules shared by the SortRoutesByName* helpers.
func compareRouteSortKeys(a, b routeSortKey) int {
	nameA := a.shortName
	if nameA == "" {
		nameA = a.longName
	}

	nameB := b.shortName
	if nameB == "" {
		nameB = b.longName
	}

	if res := NaturalCompare(nameA, nameB); res != 0 {
		return res
	}
	if a.agencyID != b.agencyID {
		return cmp.Compare(a.agencyID, b.agencyID)
	}
	return cmp.Compare(a.id, b.id)
}

// SortRoutesByName sorts routes naturally by ShortName, falling back to LongName, then by AgencyID and ID.
func SortRoutesByName(routes []gtfsdb.GetRoutesForStopRow) {
	slices.SortFunc(routes, func(a, b gtfsdb.GetRoutesForStopRow) int {
		return compareRouteSortKeys(
			routeSortKey{a.ShortName.String, a.LongName.String, a.AgencyID, a.ID},
			routeSortKey{b.ShortName.String, b.LongName.String, b.AgencyID, b.ID},
		)
	})
}

// SortDBRoutesByName sorts gtfsdb.Route values using the same ordering rules as SortRoutesByName.
func SortDBRoutesByName(routes []gtfsdb.Route) {
	slices.SortFunc(routes, func(a, b gtfsdb.Route) int {
		return compareRouteSortKeys(
			routeSortKey{a.ShortName.String, a.LongName.String, a.AgencyID, a.ID},
			routeSortKey{b.ShortName.String, b.LongName.String, b.AgencyID, b.ID},
		)
	})
}
