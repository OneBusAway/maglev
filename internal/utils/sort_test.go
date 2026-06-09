package utils_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/utils"
)

func TestSortRoutesByName(t *testing.T) {
	routes := []gtfsdb.GetRoutesForStopRow{
		{
			ID:        "3",
			AgencyID:  "agency2",
			ShortName: sql.NullString{String: "A", Valid: true},
		},
		{
			ID:        "1",
			AgencyID:  "agency1",
			ShortName: sql.NullString{String: "A", Valid: true},
		},
		{
			ID:        "2",
			AgencyID:  "agency1",
			LongName:  sql.NullString{String: "B", Valid: true},
		},
		{
			ID:        "4",
			AgencyID:  "agency2",
			ShortName: sql.NullString{String: "B", Valid: true},
		},
		{
			ID:        "6",
			AgencyID:  "agency1",
			ShortName: sql.NullString{String: "C", Valid: true},
		},
		{
			ID:        "5",
			AgencyID:  "agency1",
			ShortName: sql.NullString{String: "C", Valid: true},
		},
	}

	utils.SortRoutesByName(routes)

	assert.Equal(t, "1", routes[0].ID, "Expected ID 1: A, agency1")
	assert.Equal(t, "3", routes[1].ID, "Expected ID 3: A, agency2")
	assert.Equal(t, "2", routes[2].ID, "Expected ID 2: B (LongName fallback), agency1")
	assert.Equal(t, "4", routes[3].ID, "Expected ID 4: B, agency2")
	assert.Equal(t, "5", routes[4].ID, "Expected ID 5: C, agency1, ID 5")
	assert.Equal(t, "6", routes[5].ID, "Expected ID 6: C, agency1, ID 6")
}
