package nulls

import (
	"database/sql"
	"testing"
	"time"

	"github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
)

func TestNullStringOrEmpty(t *testing.T) {
	assert.Equal(t, "test", StringOrEmpty(String("test")))
	assert.Equal(t, "", StringOrEmpty(sql.NullString{String: "test", Valid: false}))
}

func TestNullStringOrDefault(t *testing.T) {
	assert.Equal(t, "test", StringOrDefault(sql.NullString{String: "test", Valid: true}, "fallback"))
	assert.Equal(t, "", StringOrDefault(sql.NullString{String: "", Valid: true}, "fallback"))
	assert.Equal(t, "fallback", StringOrDefault(sql.NullString{String: "test", Valid: false}, "fallback"))
}

func TestNullInt64OrDefault(t *testing.T) {
	assert.Equal(t, int64(42), Int64OrDefault(sql.NullInt64{Int64: 42, Valid: true}, 10))
	assert.Equal(t, int64(10), Int64OrDefault(sql.NullInt64{Int64: 42, Valid: false}, 10))
}

func TestNonEmptyString(t *testing.T) {
	assert.Equal(t, sql.NullString{String: "test", Valid: true}, NonEmptyString("test"))
	assert.Equal(t, sql.NullString{String: "", Valid: false}, NonEmptyString(""))
}

func TestNullWheelchairBoardingOrUnknown(t *testing.T) {
	assert.Equal(t, gtfs.WheelchairBoarding_Possible, WheelchairBoardingOrUnknown(sql.NullInt64{Int64: int64(gtfs.WheelchairBoarding_Possible), Valid: true}))
	assert.Equal(t, gtfs.WheelchairBoarding_NotSpecified, WheelchairBoardingOrUnknown(sql.NullInt64{Int64: 0, Valid: false}))
}

func TestFloat64OrNil(t *testing.T) {
	assert.Equal(t, 2.5, *Float64OrNil(sql.NullFloat64{Float64: 2.5, Valid: true}))
	assert.Nil(t, Float64OrNil(sql.NullFloat64{Float64: 2.5, Valid: false}))
}

func TestIntOrNil(t *testing.T) {
	assert.Equal(t, 90, *IntOrNil(sql.NullInt64{Int64: 90, Valid: true}))
	assert.Nil(t, IntOrNil(sql.NullInt64{Int64: 90, Valid: false}))
}

func TestInt64FromPtr(t *testing.T) {
	count := int32(7)
	duration := 90 * time.Minute
	assert.Equal(t, sql.NullInt64{Int64: 7, Valid: true}, Int64FromPtr(&count))
	assert.Equal(t, sql.NullInt64{Int64: int64(duration), Valid: true}, Int64FromPtr(&duration))
	assert.Equal(t, sql.NullInt64{}, Int64FromPtr[int64](nil))
}

func TestFloat64FromPtr(t *testing.T) {
	factor := 1.5
	assert.Equal(t, sql.NullFloat64{Float64: 1.5, Valid: true}, Float64FromPtr(&factor))
	assert.Equal(t, sql.NullFloat64{}, Float64FromPtr(nil))
}

func TestStringFromPtr(t *testing.T) {
	id := "rule-1"
	empty := ""
	assert.Equal(t, sql.NullString{String: "rule-1", Valid: true}, StringFromPtr(&id))
	assert.Equal(t, sql.NullString{String: "", Valid: true}, StringFromPtr(&empty))
	assert.Equal(t, sql.NullString{}, StringFromPtr(nil))
}
