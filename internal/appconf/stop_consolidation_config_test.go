package appconf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const stopConsolidationURL = "https://example.com/StopConsolidation.txt"

func TestLoadFromFile_StopConsolidationURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "https URL", url: stopConsolidationURL},
		{name: "local path", url: "/data/StopConsolidation.txt"},
		{name: "file URL", url: "FILE:///etc/passwd", wantErr: "file:// URLs are not allowed in stop-consolidation-url"},
		{name: "path traversal", url: "../../secret.txt", wantErr: "stop-consolidation-url cannot start with '..'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GTFS_PROTECTED_API_KEYS", "")
			t.Setenv("GTFS_STATIC_AUTH_NAME", "")
			t.Setenv("GTFS_STATIC_AUTH_VALUE", "")
			path := writeConfigFile(t, `{"stop-consolidation-url": "`+tt.url+`", "port": 4000}`)

			config, err := LoadFromFile(path)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.url, config.StopConsolidationURL)
		})
	}
}

func TestToGtfsConfigData_StopConsolidationURL(t *testing.T) {
	config := validConfigWithStaticFeeds()
	config.StopConsolidationURL = stopConsolidationURL

	gtfsConfig, err := config.ToGtfsConfigData()

	require.NoError(t, err)
	assert.Equal(t, stopConsolidationURL, gtfsConfig.StopConsolidationURL)
}
