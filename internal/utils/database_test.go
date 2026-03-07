package utils

import (
	"database/sql"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestNullStringOrEmpty(t *testing.T) {
	assert.Equal(t, "test", NullStringOrEmpty(sql.NullString{String: "test", Valid: true}))
	assert.Equal(t, "", NullStringOrEmpty(sql.NullString{String: "test", Valid: false}))
}

func TestNullInt64OrDefault(t *testing.T) {
	assert.Equal(t, int64(42), NullInt64OrDefault(sql.NullInt64{Int64: 42, Valid: true}, 10))
	assert.Equal(t, int64(10), NullInt64OrDefault(sql.NullInt64{Int64: 42, Valid: false}, 10))
}

func TestNullWheelchairBoardingOrUnknown(t *testing.T) {
	assert.Equal(t, gtfs.WheelchairBoarding_Possible, NullWheelchairBoardingOrUnknown(sql.NullInt64{Int64: int64(gtfs.WheelchairBoarding_Possible), Valid: true}))
	assert.Equal(t, gtfs.WheelchairBoarding_NotSpecified, NullWheelchairBoardingOrUnknown(sql.NullInt64{Int64: 0, Valid: false}))
}
