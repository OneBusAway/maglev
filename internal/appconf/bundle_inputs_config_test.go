package appconf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const manifestURL = "https://example.com/bundle-inputs.json"

func TestLoadFromFile_BundleInputsURL(t *testing.T) {
	tests := []struct {
		name      string
		configKey string
		wantErr   string
	}{
		{
			name:      "manifest URL alone gets no default static feed",
			configKey: `"gtfs-bundle-inputs-url": "` + manifestURL + `",`,
		},
		{
			name:      "manifest local path is accepted",
			configKey: `"gtfs-bundle-inputs-url": "./bundle-inputs.json",`,
		},
		{
			name:      "manifest URL with the singular key",
			configKey: `"gtfs-bundle-inputs-url": "` + manifestURL + `", "gtfs-static-feed": {"url": "https://example.com/a.zip"},`,
			wantErr:   "gtfs-bundle-inputs-url cannot be set together with gtfs-static-feed or gtfs-static-feeds",
		},
		{
			name:      "manifest URL with the list key",
			configKey: `"gtfs-bundle-inputs-url": "` + manifestURL + `", "gtfs-static-feeds": [{"url": "https://example.com/a.zip"}],`,
			wantErr:   "gtfs-bundle-inputs-url cannot be set together with gtfs-static-feed or gtfs-static-feeds",
		},
		{
			name:      "file URL",
			configKey: `"gtfs-bundle-inputs-url": "file:///etc/bundle-inputs.json",`,
			wantErr:   "file:// URLs are not allowed in gtfs-bundle-inputs-url",
		},
		{
			name:      "path traversal",
			configKey: `"gtfs-bundle-inputs-url": "../bundle-inputs.json",`,
			wantErr:   "gtfs-bundle-inputs-url cannot start with '..'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GTFS_PROTECTED_API_KEYS", "")
			t.Setenv("GTFS_STATIC_AUTH_NAME", "")
			t.Setenv("GTFS_STATIC_AUTH_VALUE", "")
			path := writeConfigFile(t, `{`+tt.configKey+` "port": 4000}`)

			config, err := LoadFromFile(path)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Nil(t, config.GtfsStaticFeeds)
		})
	}
}

func TestLoadFromFile_BundleInputsURLRejectsStaticAuthEnv(t *testing.T) {
	t.Setenv("GTFS_PROTECTED_API_KEYS", "")
	t.Setenv("GTFS_STATIC_AUTH_NAME", "X-Key")
	t.Setenv("GTFS_STATIC_AUTH_VALUE", "secret")
	path := writeConfigFile(t, `{"gtfs-bundle-inputs-url": "`+manifestURL+`"}`)

	_, err := LoadFromFile(path)

	require.ErrorContains(t, err, "cannot be used with gtfs-bundle-inputs-url")
}

func TestToGtfsConfigData_BundleInputsURL(t *testing.T) {
	config := validConfigWithStaticFeeds()
	config.BundleInputsURL = manifestURL

	gtfsConfig, err := config.ToGtfsConfigData()

	require.NoError(t, err)
	assert.Equal(t, manifestURL, gtfsConfig.BundleInputsURL)
	assert.Empty(t, gtfsConfig.StaticFeeds)
}
