package utils

import (
	"cmp"
	"slices"

	"maglev.onebusaway.org/gtfsdb"
)

// SortRoutesByName sorts routes naturally by ShortName, falling back to LongName, then by AgencyID and ID.
func SortRoutesByName(routes []gtfsdb.GetRoutesForStopRow) {
	slices.SortFunc(routes, func(a, b gtfsdb.GetRoutesForStopRow) int {
		nameA := a.ShortName.String
		if nameA == "" {
			nameA = a.LongName.String
		}

		nameB := b.ShortName.String
		if nameB == "" {
			nameB = b.LongName.String
		}

		res := NaturalCompare(nameA, nameB)
		if res != 0 {
			return res
		}
		if a.AgencyID != b.AgencyID {
			return cmp.Compare(a.AgencyID, b.AgencyID)
		}
		return cmp.Compare(a.ID, b.ID)
	})
}
