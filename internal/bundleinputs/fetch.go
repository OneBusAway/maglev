package bundleinputs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

// retryBackoffs are the waits between attempts to fetch a remote manifest.
// They match the static feed download's startup retries, so a brief
// obacloud or CDN outage at startup is ridden out the same way for both.
var retryBackoffs = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

// Fetch reads the manifest at source, an http(s) URL or a local path, and
// parses it. Errors name the source, with any signature in it redacted.
func Fetch(ctx context.Context, source string) (*Manifest, error) {
	manifest, err := fetchWithRetries(ctx, source, retryBackoffs)
	if err != nil {
		return nil, fmt.Errorf("bundle inputs %s: %w", appconf.RedactURL(source), err)
	}
	return manifest, nil
}

// fetchWithRetries fetches the manifest, retrying network errors, 429s and
// 5xx responses after each wait in backoffs. A local file, any other HTTP
// status and a manifest that fails to parse fail on the first attempt,
// because trying again would give the same result.
func fetchWithRetries(ctx context.Context, source string, backoffs []time.Duration) (*Manifest, error) {
	for attempt := 0; ; attempt++ {
		manifest, err := fetchAndParse(ctx, source)
		var retryable retryableError
		if err == nil || !errors.As(err, &retryable) || attempt == len(backoffs) {
			return manifest, err
		}
		slog.Warn("bundle inputs fetch failed, retrying",
			"component", "bundle_inputs",
			"error", err,
			"attempt", attempt+1,
			"max_attempts", len(backoffs)+1,
			"retry_delay", backoffs[attempt])
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoffs[attempt]):
		}
	}
}

// retryableError marks a failure that may not happen on the next attempt.
type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

// isRetryableStatus reports whether a response status is worth retrying:
// the server is overloaded or failing, rather than refusing the request.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= http.StatusInternalServerError
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
	if err != nil && appconf.IsHTTPURL(source) && ctx.Err() == nil {
		return nil, retryableError{err}
	}
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
		return openLocalSource(source)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, withoutURL(err)
	}
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		err = withoutURL(err)
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, retryableError{err}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		statusErr := fmt.Errorf("received HTTP status %s", resp.Status)
		if isRetryableStatus(resp.StatusCode) {
			return nil, retryableError{statusErr}
		}
		return nil, statusErr
	}
	return resp.Body, nil
}

// withoutURL drops the *url.Error wrapper that net/url and net/http put
// around request errors. It repeats the full URL, signature included;
// Fetch names the source redacted instead.
func withoutURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// openLocalSource opens a manifest on disk. Like the config file, it must
// be a regular file: a directory, device or FIFO is rejected instead of
// being read (a FIFO would block startup).
func openLocalSource(source string) (io.ReadCloser, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest must be a regular file, not a %s", info.Mode().Type())
	}
	return os.Open(source)
}
