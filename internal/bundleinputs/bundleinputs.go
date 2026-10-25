// Package bundleinputs reads the bundle-inputs.json manifest that obacloud
// publishes to list the static GTFS feeds a server should load.
package bundleinputs

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"maglev.onebusaway.org/internal/appconf"
)

// SupportedVersion is the only manifest version Parse accepts.
const SupportedVersion = 1

// sha256HexLength is the length of a SHA-256 digest written as hex.
const sha256HexLength = 64

// Manifest is a parsed bundle-inputs.json file.
type Manifest struct {
	Version int    `json:"version"`
	Feeds   []Feed `json:"feeds"`
	// StopConsolidationURL is optional and empty when the manifest has none.
	StopConsolidationURL string `json:"stopConsolidationUrl"`
}

// Feed is one static feed in a manifest. Feed order is load order.
type Feed struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DefaultAgencyID string `json:"defaultAgencyId"`
	URL             string `json:"url"`
	// ByteSize and SHA256 describe the published zip, for checking the
	// download. Nothing checks them yet.
	ByteSize int64  `json:"byteSize"`
	SHA256   string `json:"sha256"`
}

// Parse decodes and validates a manifest. Unknown keys are ignored.
func Parse(r io.Reader) (*Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid bundle inputs JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid bundle inputs JSON: unexpected data after the manifest")
	}
	if err := manifest.validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// StaticFeeds returns the manifest feeds as static feed config, in order.
func (m *Manifest) StaticFeeds() []appconf.GtfsStaticFeed {
	feeds := make([]appconf.GtfsStaticFeed, 0, len(m.Feeds))
	for _, feed := range m.Feeds {
		feeds = append(feeds, appconf.GtfsStaticFeed{
			ID:              feed.ID,
			URL:             feed.URL,
			DefaultAgencyID: feed.DefaultAgencyID,
		})
	}
	return feeds
}

// manifestFeedsKey names the manifest feed list in shared validation errors.
const manifestFeedsKey = "bundle inputs feeds"

// validate applies the manifest format rules, then the checks every static
// feed list gets.
func (m *Manifest) validate() error {
	if m.Version != SupportedVersion {
		return fmt.Errorf("unsupported bundle inputs version %d, want %d", m.Version, SupportedVersion)
	}
	if len(m.Feeds) == 0 {
		return fmt.Errorf("bundle inputs feeds cannot be empty")
	}
	if m.StopConsolidationURL != "" && !appconf.IsHTTPURL(m.StopConsolidationURL) {
		return fmt.Errorf("stopConsolidationUrl %q must be an http:// or https:// URL", m.StopConsolidationURL)
	}
	for i, feed := range m.Feeds {
		if err := feed.validate(); err != nil {
			return fmt.Errorf("bundle inputs feeds[%d]: %w", i, err)
		}
	}
	return appconf.ValidateStaticFeeds(m.StaticFeeds(), manifestFeedsKey)
}

// validate applies the manifest format rules to one feed. The url must be
// http(s): a manifest may come from a remote server, so it must not name
// paths on this machine.
func (feed Feed) validate() error {
	if feed.ID == "" {
		return fmt.Errorf("id is required")
	}
	if feed.DefaultAgencyID == "" {
		return fmt.Errorf("defaultAgencyId is required")
	}
	if !appconf.IsHTTPURL(feed.URL) {
		return fmt.Errorf("url %q must be an http:// or https:// URL", feed.URL)
	}
	if feed.SHA256 != "" && !isSHA256Hex(feed.SHA256) {
		return fmt.Errorf("sha256 must be %d hex characters", sha256HexLength)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == sha256HexLength
}
