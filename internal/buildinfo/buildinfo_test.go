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

func TestVariablesAreWritable(t *testing.T) {
	tests := []struct {
		name     string
		ptr      *string
		setValue string
	}{
		{"CommitHash", &CommitHash, "abc123def456"},
		{"Branch", &Branch, "main"},
		{"BuildTime", &BuildTime, "2024-01-15T10:30:00Z"},
		{"Version", &Version, "v1.2.3"},
		{"Dirty", &Dirty, "true"},
		{"CommitTime", &CommitTime, "2024-01-15T10:00:00Z"},
		{"UserEmail", &UserEmail, "test@example.com"},
		{"UserName", &UserName, "Test User"},
		{"RemoteURL", &RemoteURL, "https://github.com/example/repo.git"},
		{"CommitMessage", &CommitMessage, "feat: add new feature"},
		{"Host", &Host, "build-server-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := *tt.ptr
			defer func() { *tt.ptr = original }()

			*tt.ptr = tt.setValue
			assert.Equal(t, tt.setValue, *tt.ptr)
		})
	}
}
