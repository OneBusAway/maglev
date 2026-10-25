package bundleinputs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"maglev.onebusaway.org/internal/appconf"
	"maglev.onebusaway.org/internal/logging"
)

const (
	// maxManifestBytes caps the manifest body. A real manifest lists a few
	// dozen feeds and is a few kilobytes.
	maxManifestBytes = 1 << 20
	fetchTimeout     = 30 * time.Second
)

// Fetch reads the manifest at source, an http(s) URL or a local path, and
// parses it. Errors name the source.
func Fetch(ctx context.Context, source string) (*Manifest, error) {
	manifest, err := fetchAndParse(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("bundle inputs %s: %w", source, err)
	}
	return manifest, nil
}

func fetchAndParse(ctx context.Context, source string) (*Manifest, error) {
	data, err := readSource(ctx, source)
	if err != nil {
		return nil, err
	}
	return Parse(bytes.NewReader(data))
}

// readSource returns the body at source, up to maxManifestBytes.
func readSource(ctx context.Context, source string) ([]byte, error) {
	body, err := openSource(ctx, source)
	if err != nil {
		return nil, err
	}
	defer logging.SafeCloseWithLogging(body,
		slog.Default().With(slog.String("component", "bundle_inputs")),
		"bundle_inputs_body")

	data, err := io.ReadAll(io.LimitReader(body, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds size limit of %d bytes", maxManifestBytes)
	}
	return data, nil
}

func openSource(ctx context.Context, source string) (io.ReadCloser, error) {
	if !appconf.IsHTTPURL(source) {
		return os.Open(source)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("received HTTP status %s", resp.Status)
	}
	return resp.Body, nil
}
