package nulls

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComparePtr(t *testing.T) {
	one, two := int64(1), int64(2)
	assert.Equal(t, 0, ComparePtr[int64](nil, nil))
	assert.Equal(t, -1, ComparePtr(nil, &one))
	assert.Equal(t, 1, ComparePtr(&one, nil))
	assert.Equal(t, -1, ComparePtr(&one, &two))
	assert.Equal(t, 1, ComparePtr(&two, &one))
	assert.Equal(t, 0, ComparePtr(&one, &one))

	early, late := "08:00:00", "17:30:00"
	assert.Equal(t, -1, ComparePtr(&early, &late))
}
