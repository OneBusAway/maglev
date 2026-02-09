package gtfsdb

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"strings"

	"github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/internal/logging"
)

func PrintSimpleSchema(db *sql.DB) error { // nolint:unused
	// Get all database objects
	rows, err := db.Query(`
		SELECT type, name, sql
		FROM sqlite_master
		WHERE type IN ('table', 'index', 'view', 'trigger')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return err
	}
	defer logging.SafeCloseWithLogging(rows,
		slog.Default().With(slog.String("component", "debugging")),
		"database_rows")

	log.Println("DATABASE SCHEMA:")
	log.Println("----------------")

	for rows.Next() {
		var objType, objName, objSQL string
		if err := rows.Scan(&objType, &objName, &objSQL); err != nil {
			return err
		}
		log.Printf("%s: %s\n", strings.ToUpper(objType), objName)
		log.Printf("%s\n\n", objSQL)
	}

	return nil
}

func (c *Client) staticDataCounts(staticData *gtfs.Static) map[string]int {
	return map[string]int{
		"routes":    len(staticData.Routes),
		"services":  len(staticData.Services),
		"stops":     len(staticData.Stops),
		"agencies":  len(staticData.Agencies),
		"transfers": len(staticData.Transfers),
		"trips":     len(staticData.Trips),
		"calendar":  len(staticData.Services),
		"shapes":    len(staticData.Shapes),
	}
}

func (c *Client) TableCounts() (map[string]int, error) {
	rows, err := c.DB.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("failed to query table names: %w", err)
	}
	defer logging.SafeCloseWithLogging(rows,
		slog.Default().With(slog.String("component", "debugging")),
		"database_rows")
	var tables []string

	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err != nil {
			return nil, fmt.Errorf("failed to scan table name: %w", err)
		}
		tables = append(tables, tableName)
	}

	counts := make(map[string]int)

	for _, table := range tables {
		var query string

		// This prevents SQL injection by ensuring the query string is always a constant.
		switch table {
		case "agencies":
			query = "SELECT COUNT(*) FROM agencies"
		case "routes":
			query = "SELECT COUNT(*) FROM routes"
		case "stops":
			query = "SELECT COUNT(*) FROM stops"
		case "trips":
			query = "SELECT COUNT(*) FROM trips"
		case "stop_times":
			query = "SELECT COUNT(*) FROM stop_times"
		case "calendar":
			query = "SELECT COUNT(*) FROM calendar"
		case "calendar_dates":
			query = "SELECT COUNT(*) FROM calendar_dates"
		case "shapes":
			query = "SELECT COUNT(*) FROM shapes"
		case "transfers":
			query = "SELECT COUNT(*) FROM transfers"
		case "feed_info":
			query = "SELECT COUNT(*) FROM feed_info"
		case "block_trip_index":
			query = "SELECT COUNT(*) FROM block_trip_index"
		case "block_trip_entry":
			query = "SELECT COUNT(*) FROM block_trip_entry"
		case "import_metadata":
			query = "SELECT COUNT(*) FROM import_metadata"
		default:
			continue
		}

		var count int
		err := c.DB.QueryRow(query).Scan(&count)
		if err != nil {
			return nil, err
		}
		counts[table] = count
	}

	return counts, nil
}
