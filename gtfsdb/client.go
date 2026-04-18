package gtfsdb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Client is the main entry point for the library
type Client struct {
	config        Config
	DB            *sql.DB
	Queries       *Queries
	importRuntime time.Duration
}

// NewClient creates a new Client with the provided configuration
func NewClient(config Config) (*Client, error) {
	db, err := createDB(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create DB: %w", err)
	} else if config.verbose {
		log.Println("Successfully created tables")
	}

	// Wrap DB for query interception (optional metrics).
	var dbtx DBTX = db
	if config.QueryMetricsRecorder != nil {
		wrapper := newMetricsWrapper(db)
		wrapper.queryMetrics = config.QueryMetricsRecorder
		dbtx = wrapper
	}
	queries := New(dbtx)

	client := &Client{
		config:  config,
		DB:      db,
		Queries: queries,
	}
	return client, nil
}

func (c *Client) Close() error {
	return c.DB.Close()
}

// DownloadAndStore downloads GTFS data from the given URL and stores it in the database.
// Returns (changed, err) — changed is true when the import wrote new data, false if the
// existing data matched the downloaded bytes by hash.
func (c *Client) DownloadAndStore(ctx context.Context, url, authHeaderKey, authHeaderValue string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	// Add auth header if provided
	if authHeaderKey != "" && authHeaderValue != "" {
		req.Header.Set(authHeaderKey, authHeaderValue)
	}

	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		}}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	const maxBodySize = 200 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %w", err)
	}

	if int64(len(body)) > maxBodySize {
		return false, fmt.Errorf("static GTFS response exceeds size limit of %d bytes", maxBodySize)
	}

	return c.processAndStoreGTFSDataWithSource(body, url)
}

// ImportFromFile imports GTFS data from a local zip file into the database.
// Returns (changed, err) — changed is true when the import wrote new data, false if the
// existing data matched the file's bytes by hash.
func (c *Client) ImportFromFile(ctx context.Context, path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	return c.processAndStoreGTFSDataWithSource(data, path)
}
