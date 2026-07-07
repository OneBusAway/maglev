package utils

import (
	"cmp"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
)

// compareRoutesByName compares two routes naturally by their sort name (ShortName,
// falling back to LongName), then by AgencyID, then by ID. It is the single source
// of truth for the ordering rules shared by the SortRoutesByName* helpers.
func compareRoutesByName(shortNameA, longNameA, agencyIDA, idA, shortNameB, longNameB, agencyIDB, idB string) int {
	nameA := shortNameA
	if nameA == "" {
		nameA = longNameA
	}

	nameB := shortNameB
	if nameB == "" {
		nameB = longNameB
	}

	if res := NaturalCompare(nameA, nameB); res != 0 {
		return res
	}
	if agencyIDA != agencyIDB {
		return cmp.Compare(agencyIDA, agencyIDB)
	}
	return cmp.Compare(idA, idB)
}

// SortRoutesByName sorts routes naturally by ShortName, falling back to LongName, then by AgencyID and ID.
func SortRoutesByName(routes []gtfsdb.GetRoutesForStopRow) {
	slices.SortFunc(routes, func(a, b gtfsdb.GetRoutesForStopRow) int {
		return compareRoutesByName(
			a.ShortName.String, a.LongName.String, a.AgencyID, a.ID,
			b.ShortName.String, b.LongName.String, b.AgencyID, b.ID,
		)
	})
}

// SortDBRoutesByName sorts gtfsdb.Route values using the same ordering rules as SortRoutesByName.
func SortDBRoutesByName(routes []gtfsdb.Route) {
	slices.SortFunc(routes, func(a, b gtfsdb.Route) int {
		return compareRoutesByName(
			a.ShortName.String, a.LongName.String, a.AgencyID, a.ID,
			b.ShortName.String, b.LongName.String, b.AgencyID, b.ID,
		)
	})
}
