package merge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		strategy Strategy
		expected string
	}{
		{IDENTITY, "IDENTITY"},
		{FUZZY, "FUZZY"},
		{NONE, "NONE"},
		{Strategy(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.strategy.String())
		})
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	assert.Equal(t, IDENTITY, opts.Strategy)
	assert.Equal(t, CONTEXT, opts.RenameMode)
	assert.Equal(t, 0.5, opts.Threshold)
	assert.Equal(t, 100, opts.SampleSize)
}
