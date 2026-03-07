package gtfsdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNullHelpersCoverage(t *testing.T) {
	assert.True(t, ToNullString("val").Valid)
	assert.False(t, ToNullString("").Valid)

	f := ParseNullFloat("1.23")
	assert.True(t, f.Valid)
	assert.Equal(t, 1.23, f.Float64)

	assert.False(t, ParseNullFloat("invalid").Valid)

	b := ParseNullBool("1")
	assert.True(t, b.Valid)
	assert.Equal(t, int64(1), b.Int64)

	assert.False(t, ParseNullBool("invalid").Valid)
}
