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

	_ "github.com/mattn/go-sqlite3" // CGo-based SQLite driver
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

	queries := New(db)

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

func (c *Client) GetDBPath() string {
	return c.config.DBPath
}

// DownloadAndStore downloads GTFS data from the given URL and stores it in the database
func (c *Client) DownloadAndStore(ctx context.Context, url, authHeaderKey, authHeaderValue string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
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
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	const maxBodySize = 200 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if int64(len(body)) > maxBodySize {
		return fmt.Errorf("static GTFS response exceeds size limit of %d bytes", maxBodySize)
	}

	err = c.processAndStoreGTFSDataWithSource(body, url)

	return err
}

// ImportFromFile imports GTFS data from a local zip file into the database
func (c *Client) ImportFromFile(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	err = c.processAndStoreGTFSDataWithSource(data, path)

	return err
}
