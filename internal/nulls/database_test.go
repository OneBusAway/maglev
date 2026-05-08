package nulls

import (
	"database/sql"
	"testing"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestNullStringOrEmpty(t *testing.T) {
	assert.Equal(t, "test", StringOrEmpty(sql.NullString{String: "test", Valid: true}))
	assert.Equal(t, "", StringOrEmpty(sql.NullString{String: "test", Valid: false}))
}

func TestNullInt64OrDefault(t *testing.T) {
	assert.Equal(t, int64(42), Int64OrDefault(sql.NullInt64{Int64: 42, Valid: true}, 10))
	assert.Equal(t, int64(10), Int64OrDefault(sql.NullInt64{Int64: 42, Valid: false}, 10))
}

func TestNullWheelchairBoardingOrUnknown(t *testing.T) {
	assert.Equal(t, gtfs.WheelchairBoarding_Possible, WheelchairBoardingOrUnknown(sql.NullInt64{Int64: int64(gtfs.WheelchairBoarding_Possible), Valid: true}))
	assert.Equal(t, gtfs.WheelchairBoarding_NotSpecified, WheelchairBoardingOrUnknown(sql.NullInt64{Int64: 0, Valid: false}))
}
