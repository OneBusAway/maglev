package utils_test

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/models"
	"maglev.onebusaway.org/internal/utils"
)

func TestSortRoutesForStopRowsByName(t *testing.T) {
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
			ID:       "2",
			AgencyID: "agency1",
			LongName: sql.NullString{String: "B", Valid: true},
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

	utils.SortRoutesForStopRowsByName(routes)

	assert.Equal(t, "1", routes[0].ID, "Expected ID 1: A, agency1")
	assert.Equal(t, "3", routes[1].ID, "Expected ID 3: A, agency2")
	assert.Equal(t, "2", routes[2].ID, "Expected ID 2: B (LongName fallback), agency1")
	assert.Equal(t, "4", routes[3].ID, "Expected ID 4: B, agency2")
	assert.Equal(t, "5", routes[4].ID, "Expected ID 5: C, agency1, ID 5")
	assert.Equal(t, "6", routes[5].ID, "Expected ID 6: C, agency1, ID 6")
}

func TestSortRoutesByName(t *testing.T) {
	routes := []gtfsdb.Route{
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
			ID:       "2",
			AgencyID: "agency1",
			LongName: sql.NullString{String: "B", Valid: true},
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

func TestSortRoutesByNaturalOrder(t *testing.T) {
	// NaturalCompare should order numeric route names numerically, not lexically.
	routes := []gtfsdb.Route{
		{ID: "c", ShortName: sql.NullString{String: "10", Valid: true}},
		{ID: "a", ShortName: sql.NullString{String: "2", Valid: true}},
		{ID: "b", ShortName: sql.NullString{String: "9", Valid: true}},
	}

	utils.SortRoutesByName(routes)

	assert.Equal(t, "a", routes[0].ID, "Expected 2 first")
	assert.Equal(t, "b", routes[1].ID, "Expected 9 second")
	assert.Equal(t, "c", routes[2].ID, "Expected 10 last")
}

func TestSortModelRoutesByName(t *testing.T) {
	routes := []models.Route{
		{ID: "3", AgencyID: "agency2", ShortName: "A"},
		{ID: "1", AgencyID: "agency1", ShortName: "A"},
		{ID: "2", AgencyID: "agency1", LongName: "B"},
		{ID: "4", AgencyID: "agency2", ShortName: "B"},
		{ID: "c", AgencyID: "agency1", ShortName: "10"},
		{ID: "a", AgencyID: "agency1", ShortName: "2"},
		{ID: "b", AgencyID: "agency1", ShortName: "9"},
	}

	utils.SortModelRoutesByName(routes)

	assert.Equal(t, "a", routes[0].ID, "Expected ShortName 2")
	assert.Equal(t, "b", routes[1].ID, "Expected ShortName 9")
	assert.Equal(t, "c", routes[2].ID, "Expected ShortName 10")
	assert.Equal(t, "1", routes[3].ID, "Expected ShortName A, agency1")
	assert.Equal(t, "3", routes[4].ID, "Expected ShortName A, agency2")
	assert.Equal(t, "2", routes[5].ID, "Expected LongName B fallback, agency1")
	assert.Equal(t, "4", routes[6].ID, "Expected ShortName B, agency2")
}

func TestSortAgencyReferencesByID(t *testing.T) {
	agencies := []models.AgencyReference{
		{ID: "sound-transit", Name: "Sound Transit"},
		{ID: "metro", Name: "King County Metro"},
		{ID: "community-transit", Name: "Community Transit"},
	}

	utils.SortAgencyReferencesByID(agencies)

	assert.Equal(t, "community-transit", agencies[0].ID)
	assert.Equal(t, "metro", agencies[1].ID)
	assert.Equal(t, "sound-transit", agencies[2].ID)
}
