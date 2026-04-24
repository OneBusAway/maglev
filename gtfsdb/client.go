package gtfsdb

import (
	"database/sql"
	"fmt"
	"log/slog"
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
	}
	slog.Default().Debug("successfully created DB")

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
