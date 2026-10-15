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

func TestStringOrNonEmpty(t *testing.T) {
	assert.Equal(t, "S1", StringOrNonEmpty(sql.NullString{String: "S1", Valid: true}, "raw"))
	assert.Equal(t, " S1 ", StringOrNonEmpty(sql.NullString{String: " S1 ", Valid: true}, "raw"))
	assert.Equal(t, "raw", StringOrNonEmpty(sql.NullString{String: "", Valid: true}, "raw"))
	assert.Equal(t, "raw", StringOrNonEmpty(sql.NullString{}, "raw"))
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

func TestFloat64FromPtr(t *testing.T) {
	zero := 0.0
	value := 1500.5

	// The case this exists for: 0 is a real distance, not a missing one.
	got := Float64FromPtr(&zero)
	assert.True(t, got.Valid, "a pointer to 0 is a supplied value")
	assert.Equal(t, 0.0, got.Float64)

	got = Float64FromPtr(&value)
	assert.True(t, got.Valid)
	assert.Equal(t, 1500.5, got.Float64)

	assert.False(t, Float64FromPtr(nil).Valid, "only a nil pointer is null")
}

func TestInt64FromPtr(t *testing.T) {
	zero := int32(0)
	window := 90 * time.Minute

	got := Int64FromPtr(&zero)
	assert.True(t, got.Valid, "a pointer to 0 is a supplied value")
	assert.Equal(t, int64(0), got.Int64)

	assert.Equal(t, sql.NullInt64{Int64: int64(window), Valid: true}, Int64FromPtr(&window))
	assert.False(t, Int64FromPtr[int64](nil).Valid, "only a nil pointer is null")
}

func TestStringFromPtr(t *testing.T) {
	empty := ""
	assert.Equal(t, sql.NullString{String: "", Valid: true}, StringFromPtr(&empty), "a pointer to \"\" is a supplied value")
	assert.False(t, StringFromPtr(nil).Valid, "only a nil pointer is null")
}

func TestFloat64OrNil(t *testing.T) {
	assert.Equal(t, 2.5, *Float64OrNil(sql.NullFloat64{Float64: 2.5, Valid: true}))
	assert.Nil(t, Float64OrNil(sql.NullFloat64{Float64: 2.5, Valid: false}))
}

func TestStringOrNil(t *testing.T) {
	assert.Equal(t, "call", *StringOrNil(sql.NullString{String: "call", Valid: true}))
	assert.Nil(t, StringOrNil(sql.NullString{String: "", Valid: true}), "blank is omitted")
	assert.Nil(t, StringOrNil(sql.NullString{String: "call", Valid: false}))
}

func TestIntOrNil(t *testing.T) {
	assert.Equal(t, 90, *IntOrNil(sql.NullInt64{Int64: 90, Valid: true}))
	assert.Nil(t, IntOrNil(sql.NullInt64{Int64: 90, Valid: false}))
}
