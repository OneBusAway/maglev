package appconf

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfigFile writes content to a config file in a temp directory and
// returns its path.
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// validConfigWithStaticFeeds returns a config that passes Validate apart
// from the given static feed list.
func validConfigWithStaticFeeds(feeds ...GtfsStaticFeed) *JSONConfig {
	return &JSONConfig{
		Port:             4000,
		Env:              "development",
		ApiKeys:          []string{"test"},
		ProtectedApiKeys: []string{"test"},
		RateLimit:        100,
		GtfsStaticFeeds:  feeds,
		LogLevel:         "info",
		LogFormat:        "text",
	}
}

func TestLoadFromFile_StaticFeedKeys(t *testing.T) {
	tests := []struct {
		name          string
		staticFeedKey string
		wantURLs      []string
		wantErr       string
	}{
		{
			name:     "neither key gets the default URL",
			wantURLs: []string{defaultStaticFeedURL},
		},
		{
			name:          "singular key without url gets the default URL",
			staticFeedKey: `"gtfs-static-feed": {"enable-gtfs-tidy": true},`,
			wantURLs:      []string{defaultStaticFeedURL},
		},
		{
			name:          "singular key becomes a one-element list",
			staticFeedKey: `"gtfs-static-feed": {"url": "https://example.com/a.zip"},`,
			wantURLs:      []string{"https://example.com/a.zip"},
		},
		{
			name:          "list keeps its order and gets no default URL",
			staticFeedKey: `"gtfs-static-feeds": [{"url": "https://example.com/a.zip"}, {"url": "https://example.com/b.zip"}],`,
			wantURLs:      []string{"https://example.com/a.zip", "https://example.com/b.zip"},
		},
		{
			name:          "null list counts as absent",
			staticFeedKey: `"gtfs-static-feeds": null,`,
			wantURLs:      []string{defaultStaticFeedURL},
		},
		{
			name:          "both keys are rejected",
			staticFeedKey: `"gtfs-static-feed": {"url": "https://example.com/a.zip"}, "gtfs-static-feeds": [{"url": "https://example.com/b.zip"}],`,
			wantErr:       "gtfs-static-feed and gtfs-static-feeds cannot both be set",
		},
		{
			name:          "empty list is rejected",
			staticFeedKey: `"gtfs-static-feeds": [],`,
			wantErr:       "gtfs-static-feeds cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GTFS_PROTECTED_API_KEYS", "")
			t.Setenv("GTFS_STATIC_AUTH_NAME", "")
			t.Setenv("GTFS_STATIC_AUTH_VALUE", "")
			path := writeConfigFile(t, `{`+tt.staticFeedKey+` "port": 4000}`)

			config, err := LoadFromFile(path)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			var gotURLs []string
			for _, feed := range config.GtfsStaticFeeds {
				gotURLs = append(gotURLs, feed.URL)
			}
			assert.Equal(t, tt.wantURLs, gotURLs)
		})
	}
}

func TestValidate_StaticFeedList(t *testing.T) {
	const urlA = "https://example.com/a.zip"
	const urlB = "https://example.com/b.zip"

	tests := []struct {
		name    string
		feeds   []GtfsStaticFeed
		legacy  *GtfsStaticFeed // set as gtfs-static-feed instead of feeds
		wantErr string
	}{
		{
			name: "valid feeds",
			feeds: []GtfsStaticFeed{
				{ID: "metro", URL: urlA, DefaultAgencyID: "1"},
				{ID: "st", URL: "./st.zip", DefaultAgencyID: "40", AgencyIDMappings: map[string]string{"S_T": "40"}},
			},
		},
		{
			name:  "feeds without a default agency do not collide",
			feeds: []GtfsStaticFeed{{URL: urlA}, {URL: urlB}},
		},
		{
			name:    "duplicate ids",
			feeds:   []GtfsStaticFeed{{ID: "metro", URL: urlA}, {ID: "metro", URL: urlB}},
			wantErr: `duplicate gtfs-static-feeds id: "metro"`,
		},
		{
			name:    "explicit id collides with a default id",
			feeds:   []GtfsStaticFeed{{ID: "feed-1", URL: urlA}, {URL: urlB}},
			wantErr: `duplicate gtfs-static-feeds id: "feed-1"`,
		},
		{
			name:    "duplicate default agency ids",
			feeds:   []GtfsStaticFeed{{URL: urlA, DefaultAgencyID: "1"}, {URL: urlB, DefaultAgencyID: "1"}},
			wantErr: `duplicate gtfs-static-feeds default-agency-id: "1"`,
		},
		{
			name:    "underscore in default agency id",
			feeds:   []GtfsStaticFeed{{URL: urlA, DefaultAgencyID: "king_county"}},
			wantErr: `default-agency-id "king_county" must not contain '_'`,
		},
		{
			name:    "underscore in agency id mapping target",
			feeds:   []GtfsStaticFeed{{URL: urlA, AgencyIDMappings: map[string]string{"ST": "sound_transit"}}},
			wantErr: `agency-id-mappings target "sound_transit" for "ST" must not contain '_'`,
		},
		{
			name:    "empty agency id mapping key",
			feeds:   []GtfsStaticFeed{{URL: urlA, AgencyIDMappings: map[string]string{"": "40"}}},
			wantErr: `agency-id-mappings key for "40" cannot be empty`,
		},
		{
			name:    "empty agency id mapping target",
			feeds:   []GtfsStaticFeed{{URL: urlA, AgencyIDMappings: map[string]string{"ST": ""}}},
			wantErr: `agency-id-mappings target for "ST" cannot be empty`,
		},
		{
			name:    "file URL in a later feed",
			feeds:   []GtfsStaticFeed{{URL: urlA}, {ID: "local", URL: "file:///etc/passwd"}},
			wantErr: `gtfs-static-feeds "local": file:// URLs are not allowed`,
		},
		{
			name:    "path traversal in a later feed",
			feeds:   []GtfsStaticFeed{{URL: urlA}, {URL: "../secret.zip"}},
			wantErr: `gtfs-static-feeds "feed-1": url cannot start with '..'`,
		},
		{
			name:    "list entry without url",
			feeds:   []GtfsStaticFeed{{URL: urlA}, {ID: "metro"}},
			wantErr: `gtfs-static-feeds "metro": url is required`,
		},
		{
			name:    "legacy feed error names the legacy key",
			legacy:  &GtfsStaticFeed{URL: urlA, DefaultAgencyID: "a_b"},
			wantErr: `gtfs-static-feed "feed-0": default-agency-id`,
		},
		{
			name:    "legacy feed without url",
			legacy:  &GtfsStaticFeed{},
			wantErr: `gtfs-static-feed "feed-0": url is required`,
		},
		{
			name:    "auth name without value",
			feeds:   []GtfsStaticFeed{{URL: urlA}, {URL: urlB, AuthHeaderName: "Authorization"}},
			wantErr: "both auth-header-name and auth-header-value must be provided together",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := validConfigWithStaticFeeds(tt.feeds...)
			config.GtfsStaticFeed = tt.legacy

			err := config.Validate()

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestApplyStaticAuthEnvOverrides(t *testing.T) {
	const urlA = "https://example.com/a.zip"
	const urlB = "https://example.com/b.zip"

	tests := []struct {
		name         string
		envName      string
		envValue     string
		config       JSONConfig
		wantAuthName []string
		wantErr      string
	}{
		{
			name:         "singular feed",
			envName:      "X-Env",
			envValue:     "env-secret",
			config:       JSONConfig{GtfsStaticFeed: &GtfsStaticFeed{URL: urlA}},
			wantAuthName: []string{"X-Env"},
		},
		{
			name:         "one-element list",
			envName:      "X-Env",
			envValue:     "env-secret",
			config:       JSONConfig{GtfsStaticFeeds: []GtfsStaticFeed{{URL: urlA}}},
			wantAuthName: []string{"X-Env"},
		},
		{
			name:     "longer list is an error, not a dropped secret",
			envName:  "X-Env",
			envValue: "env-secret",
			config:   JSONConfig{GtfsStaticFeeds: []GtfsStaticFeed{{URL: urlA}, {URL: urlB}}},
			wantErr:  "apply to one static feed, but 2 are configured",
		},
		{
			name:         "no env leaves the file value",
			config:       JSONConfig{GtfsStaticFeeds: []GtfsStaticFeed{{URL: urlA, AuthHeaderName: "X-File", AuthHeaderValue: "file-secret"}}},
			wantAuthName: []string{"X-File"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GTFS_STATIC_AUTH_NAME", tt.envName)
			t.Setenv("GTFS_STATIC_AUTH_VALUE", tt.envValue)
			require.NoError(t, tt.config.normalizeStaticFeeds())

			err := tt.config.applyStaticAuthEnvOverrides()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			var gotAuthNames []string
			for _, feed := range tt.config.GtfsStaticFeeds {
				gotAuthNames = append(gotAuthNames, feed.AuthHeaderName)
			}
			assert.Equal(t, tt.wantAuthName, gotAuthNames)
		})
	}
}

func TestToGtfsConfigData_FlattensFirstStaticFeed(t *testing.T) {
	config := validConfigWithStaticFeeds(
		GtfsStaticFeed{URL: "https://example.com/a.zip", AuthHeaderName: "X-Key", AuthHeaderValue: "secret", EnableGTFSTidy: true},
		GtfsStaticFeed{URL: "https://example.com/b.zip"},
	)

	gtfsConfig, err := config.ToGtfsConfigData()

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/a.zip", gtfsConfig.GtfsURL)
	assert.Equal(t, "X-Key", gtfsConfig.StaticAuthHeaderKey)
	assert.Equal(t, "secret", gtfsConfig.StaticAuthHeaderValue)
	assert.True(t, gtfsConfig.EnableGTFSTidy)
}

func TestToGtfsConfigData_NoStaticFeeds(t *testing.T) {
	config := validConfigWithStaticFeeds()

	gtfsConfig, err := config.ToGtfsConfigData()

	require.NoError(t, err)
	assert.Empty(t, gtfsConfig.GtfsURL)
	assert.Empty(t, gtfsConfig.StaticAuthHeaderKey)
}

// captureDefaultLog sends slog's default logger to a buffer for the rest
// of the test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func TestToGtfsConfigData_WarnsAboutIgnoredStaticFeeds(t *testing.T) {
	tests := []struct {
		name      string
		feeds     []GtfsStaticFeed
		wantInLog []string
	}{
		{
			name:  "one feed logs nothing",
			feeds: []GtfsStaticFeed{{ID: "metro", URL: "https://example.com/a.zip"}},
		},
		{
			name: "later feeds are named",
			feeds: []GtfsStaticFeed{
				{ID: "metro", URL: "https://example.com/a.zip"},
				{ID: "st", URL: "https://example.com/b.zip"},
				{URL: "https://example.com/c.zip"},
			},
			wantInLog: []string{
				"only the first static feed is loaded",
				"component=config_loader",
				"loaded_feed=metro",
				"ignored_feeds=\"[st feed-2]\"",
			},
		},
		{
			name: "unused fields on the loaded feed are named",
			feeds: []GtfsStaticFeed{{
				ID:               "metro",
				URL:              "https://example.com/a.zip",
				DefaultAgencyID:  "1",
				AgencyIDMappings: map[string]string{"M": "1"},
				Headers:          map[string]string{"Authorization": "Bearer x"},
			}},
			wantInLog: []string{
				"accepted but not applied",
				"feed=metro",
				"unused_fields=\"[agency-id-mappings headers]\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logOutput := captureDefaultLog(t)

			_, err := validConfigWithStaticFeeds(tt.feeds...).ToGtfsConfigData()

			require.NoError(t, err)
			if len(tt.wantInLog) == 0 {
				assert.Empty(t, logOutput.String())
				return
			}
			for _, want := range tt.wantInLog {
				assert.Contains(t, logOutput.String(), want)
			}
		})
	}
}
