package appconf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleValidation(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "default all is valid",
			role:      "all",
			wantError: false,
		},
		{
			name:      "ingester role is valid",
			role:      "ingester",
			wantError: false,
		},
		{
			name:      "server role is valid",
			role:      "server",
			wantError: false,
		},
		{
			name:      "empty role fails validation",
			role:      "",
			wantError: true,
			errorMsg:  `role must be one of [all, ingester, server], got ""`,
		},
		{
			name:      "unknown role fails validation",
			role:      "worker",
			wantError: true,
			errorMsg:  `role must be one of [all, ingester, server], got "worker"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := JSONConfig{
				Role:             tt.role,
				Port:             4000,
				Env:              "development",
				ApiKeys:          []string{"test"},
				ProtectedApiKeys: []string{"test"},
				RateLimit:        100,
				GtfsStaticFeeds: []GtfsStaticFeed{
					{URL: "https://example.com/gtfs.zip"},
				},
				GtfsRtFeeds: []GtfsRtFeed{
					{TripUpdatesURL: "https://example.com/tu"},
				},
				DataPath:  "./gtfs.db",
				LogLevel:  "info",
				LogFormat: "text",
			}

			err := cfg.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRoleDefaults(t *testing.T) {
	cfg := JSONConfig{}
	cfg.setDefaults()
	assert.Equal(t, "all", cfg.Role)
	assert.Equal(t, "all", cfg.ToAppConfig().Role)
}

func TestRoleJSONLoading(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("omitted role defaults to all", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, "no_role.json")
		content := map[string]any{
			"port":     4000,
			"env":      "development",
			"api-keys": []string{"test"},
		}
		data, err := json.Marshal(content)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, data, 0644))

		jsonCfg, err := LoadFromFile(configPath)
		require.NoError(t, err)
		assert.Equal(t, "all", jsonCfg.Role)
		assert.Equal(t, "all", jsonCfg.ToAppConfig().Role)
	})

	t.Run("explicit role is preserved", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, "server_role.json")
		content := map[string]any{
			"role":     "server",
			"port":     4000,
			"env":      "development",
			"api-keys": []string{"test"},
		}
		data, err := json.Marshal(content)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, data, 0644))

		jsonCfg, err := LoadFromFile(configPath)
		require.NoError(t, err)
		assert.Equal(t, "server", jsonCfg.Role)
		assert.Equal(t, "server", jsonCfg.ToAppConfig().Role)
	})

	t.Run("invalid role in JSON fails validation", func(t *testing.T) {
		configPath := filepath.Join(tmpDir, "invalid_role.json")
		content := map[string]any{
			"role":     "unsupported-role",
			"port":     4000,
			"env":      "development",
			"api-keys": []string{"test"},
		}
		data, err := json.Marshal(content)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, data, 0644))

		_, err = LoadFromFile(configPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `role must be one of [all, ingester, server], got "unsupported-role"`)
	})
}
