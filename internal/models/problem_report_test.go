package models

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
)

func TestNewProblemReportTrip(t *testing.T) {
	dbReport := gtfsdb.ProblemReportsTrip{
		ID:            1,
		TripID:        "trip-1",
		ServiceDate:   sql.NullString{String: "20230101", Valid: true},
		VehicleID:     sql.NullString{String: "veh-1", Valid: true},
		StopID:        sql.NullString{String: "stop-1", Valid: true},
		Code:          sql.NullString{String: "code-1", Valid: true},
		UserComment:   sql.NullString{String: "late", Valid: true},
		UserOnVehicle: sql.NullInt64{Int64: 1, Valid: true},
	}
	apiReport := NewProblemReportTrip(dbReport)

	assert.Equal(t, int64(1), apiReport.ID)
	assert.Equal(t, "trip-1", apiReport.TripID)
	assert.Equal(t, "20230101", apiReport.ServiceDate)
	assert.Equal(t, "late", apiReport.UserComment)
	assert.True(t, apiReport.UserOnVehicle)
}

func TestNewProblemReportStop(t *testing.T) {
	dbReport := gtfsdb.ProblemReportsStop{
		ID:          2,
		StopID:      "stop-2",
		UserComment: sql.NullString{String: "dirty", Valid: true},
	}
	apiReport := NewProblemReportStop(dbReport)

	assert.Equal(t, int64(2), apiReport.ID)
	assert.Equal(t, "stop-2", apiReport.StopID)
	assert.Equal(t, "dirty", apiReport.UserComment)
}
