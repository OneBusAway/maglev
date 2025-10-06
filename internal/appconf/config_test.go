package appconf

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestEnvFlagToEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		envFlag  string
		expected Environment
	}{
		{
			name:     "Development environment",
			envFlag:  "development",
			expected: Development,
		},
		{
			name:     "Test environment",
			envFlag:  "test",
			expected: Test,
		},
		{
			name:     "Production environment",
			envFlag:  "production",
			expected: Production,
		},
		{
			name:     "Unknown environment defaults to Development",
			envFlag:  "unknown",
			expected: Development,
		},
		{
			name:     "Empty string defaults to Development",
			envFlag:  "",
			expected: Development,
		},
		{
			name:     "Mixed case defaults to Development",
			envFlag:  "Development",
			expected: Development,
		},
		{
			name:     "Uppercase defaults to Development",
			envFlag:  "PRODUCTION",
			expected: Development,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EnvFlagToEnvironment(tt.envFlag)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnvironmentConstants(t *testing.T) {
	// Verify the enum values are as expected
	assert.Equal(t, Environment(0), Development)
	assert.Equal(t, Environment(1), Test)
	assert.Equal(t, Environment(2), Production)
}
