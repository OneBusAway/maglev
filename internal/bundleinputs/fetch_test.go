package bundleinputs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetch(t *testing.T) {
	golden, err := os.ReadFile(goldenManifestPath)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/bundle-inputs.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(golden)
	})
	mux.HandleFunc("/invalid.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version": 2}`))
	})
	mux.HandleFunc("/too-large.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", maxManifestBytes+1)))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	tests := []struct {
		name        string
		source      string
		wantFeedIDs []string
		wantErr     string
	}{
		{
			name:        "http URL",
			source:      server.URL + "/bundle-inputs.json",
			wantFeedIDs: []string{"metro", "sound-transit"},
		},
		{
			name:        "local path",
			source:      goldenManifestPath,
			wantFeedIDs: []string{"metro", "sound-transit"},
		},
		{
			name:    "HTTP error status",
			source:  server.URL + "/missing.json",
			wantErr: "received HTTP status 404",
		},
		{
			name:    "invalid manifest",
			source:  server.URL + "/invalid.json",
			wantErr: "unsupported bundle inputs version 2",
		},
		{
			name:    "body over the size limit",
			source:  server.URL + "/too-large.json",
			wantErr: "exceeds size limit",
		},
		{
			name:    "missing local file",
			source:  filepath.Join(t.TempDir(), "missing.json"),
			wantErr: "no such file",
		},
		{
			name:    "local directory",
			source:  t.TempDir(),
			wantErr: "manifest must be a regular file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, err := Fetch(context.Background(), tt.source)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), tt.source, "the error names the manifest source")
				return
			}
			require.NoError(t, err)
			var gotFeedIDs []string
			for _, feed := range manifest.Feeds {
				gotFeedIDs = append(gotFeedIDs, feed.ID)
			}
			assert.Equal(t, tt.wantFeedIDs, gotFeedIDs)
		})
	}
}
