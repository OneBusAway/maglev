package models

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/nulls"
)

func TestNewProblemReportTrip(t *testing.T) {
	dbReport := gtfsdb.ProblemReportsTrip{
		ID:            1,
		TripID:        "trip-1",
		ServiceDate:   nulls.String("20230101"),
		VehicleID:     nulls.String("veh-1"),
		StopID:        nulls.String("stop-1"),
		Code:          nulls.String("code-1"),
		UserComment:   nulls.String("late"),
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
		UserComment: nulls.String("dirty"),
	}
	apiReport := NewProblemReportStop(dbReport)

	assert.Equal(t, int64(2), apiReport.ID)
	assert.Equal(t, "stop-2", apiReport.StopID)
	assert.Equal(t, "dirty", apiReport.UserComment)
}
