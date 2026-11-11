package bundleinputs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maglev.onebusaway.org/internal/appconf"
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

func TestFetchWithRetries(t *testing.T) {
	golden, err := os.ReadFile(goldenManifestPath)
	require.NoError(t, err)
	backoffs := []time.Duration{time.Millisecond, time.Millisecond}

	tests := []struct {
		name         string
		failures     int // requests that get failStatus before the manifest is served
		failStatus   int
		body         []byte
		wantRequests int
		wantErr      string
	}{
		{
			name:         "a 5xx is retried until the manifest loads",
			failures:     2,
			failStatus:   http.StatusServiceUnavailable,
			body:         golden,
			wantRequests: 3,
		},
		{
			name:         "a 429 is retried",
			failures:     1,
			failStatus:   http.StatusTooManyRequests,
			body:         golden,
			wantRequests: 2,
		},
		{
			name:         "a 5xx that outlasts the backoffs fails",
			failures:     3,
			failStatus:   http.StatusBadGateway,
			body:         golden,
			wantRequests: 3,
			wantErr:      "received HTTP status 502",
		},
		{
			name:         "a 4xx fails on the first attempt",
			failures:     1,
			failStatus:   http.StatusNotFound,
			body:         golden,
			wantRequests: 1,
			wantErr:      "received HTTP status 404",
		},
		{
			name:         "an invalid manifest fails on the first attempt",
			body:         []byte(`{"version": 2}`),
			wantRequests: 1,
			wantErr:      "unsupported bundle inputs version 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if int(requests.Add(1)) <= tt.failures {
					w.WriteHeader(tt.failStatus)
					return
				}
				_, _ = w.Write(tt.body)
			}))
			defer server.Close()

			manifest, err := fetchWithRetries(context.Background(), server.URL, backoffs)

			assert.Equal(t, tt.wantRequests, int(requests.Load()))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, manifest.Feeds, 2)
		})
	}
}

func TestFetchWithRetries_RetriesNetworkErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	source := server.URL
	server.Close()
	start := time.Now()

	_, err := fetchWithRetries(context.Background(), source, []time.Duration{50 * time.Millisecond})

	require.Error(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond, "connection refused should be retried after the backoff")
}

func TestFetchWithRetries_RetriesTruncatedBody(t *testing.T) {
	golden, err := os.ReadFile(goldenManifestPath)
	require.NoError(t, err)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			// Promise more bytes than are sent, so the client sees the
			// connection drop partway through the body.
			w.Header().Set("Content-Length", strconv.Itoa(len(golden)))
			_, _ = w.Write(golden[:10])
			return
		}
		_, _ = w.Write(golden)
	}))
	defer server.Close()

	manifest, err := fetchWithRetries(context.Background(), server.URL, []time.Duration{time.Millisecond})

	require.NoError(t, err)
	assert.Len(t, manifest.Feeds, 2)
	assert.Equal(t, int32(2), requests.Load())
}

func TestFetchWithRetries_StopsWhenContextEnds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := fetchWithRetries(ctx, server.URL, []time.Duration{time.Hour})

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestFetchWithRetries_LocalFileFailsOnce(t *testing.T) {
	start := time.Now()

	_, err := fetchWithRetries(context.Background(), filepath.Join(t.TempDir(), "missing.json"), []time.Duration{time.Hour})

	require.ErrorContains(t, err, "no such file")
	assert.Less(t, time.Since(start), time.Minute)
}

func TestFetch_RedactsSignedURLInErrors(t *testing.T) {
	previous := retryBackoffs
	retryBackoffs = nil
	defer func() { retryBackoffs = previous }()
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	tests := []struct {
		name   string
		server string
	}{
		{name: "HTTP error status", server: notFound.URL},
		// net/http puts the full request URL in a connection error.
		{name: "connection refused", server: closed.URL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Fetch(context.Background(), tt.server+"/b.json?sig=s3cr3t")

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "s3cr3t")
			assert.Contains(t, err.Error(), tt.server+"/b.json?"+appconf.RedactedValue)
		})
	}
}

// A URL that net/http cannot turn into a request fails before it is sent,
// and that error repeats the full URL too.
func TestFetch_RedactsMalformedSignedURLInErrors(t *testing.T) {
	_, err := Fetch(context.Background(), "https://host:abc/b.json?sig=s3cr3t")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cr3t")
}
