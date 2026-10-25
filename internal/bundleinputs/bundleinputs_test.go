package bundleinputs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"maglev.onebusaway.org/internal/appconf"
)

const goldenManifestPath = "testdata/bundle-inputs.json"

func TestParse_GoldenManifest(t *testing.T) {
	file, err := os.Open(goldenManifestPath)
	require.NoError(t, err)
	defer file.Close()

	manifest, err := Parse(file)

	require.NoError(t, err)
	want := &Manifest{
		Version: 1,
		Feeds: []Feed{
			{
				ID:              "metro",
				Name:            "King County Metro",
				DefaultAgencyID: "1",
				URL:             "https://example.com/bundles/metro.zip",
				ByteSize:        1234567,
				SHA256:          "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			},
			{
				ID:              "sound-transit",
				Name:            "Sound Transit",
				DefaultAgencyID: "40",
				URL:             "https://example.com/bundles/sound-transit.zip",
			},
		},
		StopConsolidationURL: "https://example.com/bundles/stop-consolidation.txt",
	}
	assert.Equal(t, want, manifest)

	wantStaticFeeds := []appconf.GtfsStaticFeed{
		{ID: "metro", URL: "https://example.com/bundles/metro.zip", DefaultAgencyID: "1"},
		{ID: "sound-transit", URL: "https://example.com/bundles/sound-transit.zip", DefaultAgencyID: "40"},
	}
	assert.Equal(t, wantStaticFeeds, manifest.StaticFeeds())
}

func TestParse_Validation(t *testing.T) {
	const validSHA256 = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	feedA := `{"id": "a", "defaultAgencyId": "1", "url": "https://example.com/a.zip"}`

	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name:     "minimal manifest without consolidation url",
			manifest: `{"version": 1, "feeds": [` + feedA + `]}`,
		},
		{
			name:     "uppercase sha256 is accepted",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1", "url": "https://example.com/a.zip", "sha256": "` + strings.ToUpper(validSHA256) + `"}]}`,
		},
		{
			name:     "malformed JSON",
			manifest: `{"version": 1,`,
			wantErr:  "invalid bundle inputs JSON",
		},
		{
			name:     "missing version",
			manifest: `{"feeds": [` + feedA + `]}`,
			wantErr:  "unsupported bundle inputs version 0",
		},
		{
			name:     "future version",
			manifest: `{"version": 2, "feeds": [` + feedA + `]}`,
			wantErr:  "unsupported bundle inputs version 2",
		},
		{
			name:     "no feeds",
			manifest: `{"version": 1, "feeds": []}`,
			wantErr:  "feeds cannot be empty",
		},
		{
			name:     "missing id",
			manifest: `{"version": 1, "feeds": [{"defaultAgencyId": "1", "url": "https://example.com/a.zip"}]}`,
			wantErr:  "feeds[0]: id is required",
		},
		{
			name:     "missing url",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1"}]}`,
			wantErr:  `feeds[0]: url "" must be an http:// or https:// URL`,
		},
		{
			name:     "local path url",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1", "url": "/etc/passwd"}]}`,
			wantErr:  "must be an http:// or https:// URL",
		},
		{
			name:     "missing defaultAgencyId",
			manifest: `{"version": 1, "feeds": [{"id": "a", "url": "https://example.com/a.zip"}]}`,
			wantErr:  "feeds[0]: defaultAgencyId is required",
		},
		{
			name:     "defaultAgencyId with underscore",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1_2", "url": "https://example.com/a.zip"}]}`,
			wantErr:  `bundle inputs feeds "a": default-agency-id "1_2" must not contain '_'`,
		},
		{
			name:     "duplicate id",
			manifest: `{"version": 1, "feeds": [` + feedA + `, {"id": "a", "defaultAgencyId": "2", "url": "https://example.com/b.zip"}]}`,
			wantErr:  `duplicate bundle inputs feeds id: "a"`,
		},
		{
			name:     "duplicate defaultAgencyId fails the shared static feed validator",
			manifest: `{"version": 1, "feeds": [` + feedA + `, {"id": "b", "defaultAgencyId": "1", "url": "https://example.com/b.zip"}]}`,
			wantErr:  `duplicate bundle inputs feeds default-agency-id: "1"`,
		},
		{
			name:     "short sha256",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1", "url": "https://example.com/a.zip", "sha256": "abc123"}]}`,
			wantErr:  "sha256 must be 64 hex characters",
		},
		{
			name:     "non-hex sha256",
			manifest: `{"version": 1, "feeds": [{"id": "a", "defaultAgencyId": "1", "url": "https://example.com/a.zip", "sha256": "` + strings.Repeat("z", 64) + `"}]}`,
			wantErr:  "sha256 must be 64 hex characters",
		},
		{
			name:     "local stopConsolidationUrl",
			manifest: `{"version": 1, "feeds": [` + feedA + `], "stopConsolidationUrl": "stop-consolidation.txt"}`,
			wantErr:  "stopConsolidationUrl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, err := Parse(strings.NewReader(tt.manifest))

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, manifest)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, manifest.Feeds)
		})
	}
}
