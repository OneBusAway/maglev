package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultValues(t *testing.T) {
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"CommitHash", CommitHash, "unknown"},
		{"Branch", Branch, "unknown"},
		{"BuildTime", BuildTime, "unknown"},
		{"Version", Version, "dev"},
		{"Dirty", Dirty, "false"},
		{"CommitTime", CommitTime, "unknown"},
		{"UserEmail", UserEmail, "unknown"},
		{"UserName", UserName, "unknown"},
		{"RemoteURL", RemoteURL, "unknown"},
		{"CommitMessage", CommitMessage, "unknown"},
		{"Host", Host, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.got)
		})
	}
}
