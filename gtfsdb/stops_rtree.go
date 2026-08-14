package gtfsdb

import (
	"context"
	"fmt"
	"strings"
)

// Implemented manually because sqlc doesn't support the virtual tables from the RTree module.

const getActiveStopsWithinBounds = `
SELECT
    s.id,
    s.code,
    s.name,
    s."desc",
    s.lat,
    s.lon,
    s.zone_id,
    s.url,
    s.location_type,
    s.timezone,
    s.wheelchair_boarding,
    s.platform_code,
    s.direction,
    s.parent_station
FROM stops s
INNER JOIN stops_rtree sr ON sr.id = s.rowid
WHERE sr.min_lat >= ? AND sr.max_lat <= ?
  AND sr.min_lon >= ? AND sr.max_lon <= ?
  AND EXISTS (SELECT 1 FROM stop_times st WHERE st.stop_id = s.id)
`

type GetActiveStopsWithinBoundsParams struct {
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
}

func (q *Queries) GetActiveStopsWithinBounds(ctx context.Context, arg GetActiveStopsWithinBoundsParams) ([]Stop, error) {
	rows, err := q.db.QueryContext(ctx, getActiveStopsWithinBounds,
		arg.MinLat, arg.MaxLat, arg.MinLon, arg.MaxLon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Stop
	for rows.Next() {
		var i Stop
		if err := rows.Scan(
			&i.ID,
			&i.Code,
			&i.Name,
			&i.Desc,
			&i.Lat,
			&i.Lon,
			&i.ZoneID,
			&i.Url,
			&i.LocationType,
			&i.Timezone,
			&i.WheelchairBoarding,
			&i.PlatformCode,
			&i.Direction,
			&i.ParentStation,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getStopIDsWithinBounds = `
SELECT s.id
FROM stops s
INNER JOIN stops_rtree sr ON sr.id = s.rowid
WHERE sr.min_lat >= ? AND sr.max_lat <= ?
  AND sr.min_lon >= ? AND sr.max_lon <= ?
  AND EXISTS (SELECT 1 FROM stop_times st WHERE st.stop_id = s.id)
`

type GetStopIDsWithinBoundsParams struct {
	MinLat float64
	MaxLat float64
	MinLon float64
	MaxLon float64
}

func (q *Queries) GetStopIDsWithinBounds(ctx context.Context, arg GetStopIDsWithinBoundsParams) ([]string, error) {
	rows, err := q.db.QueryContext(ctx, getStopIDsWithinBounds,
		arg.MinLat, arg.MaxLat, arg.MinLon, arg.MaxLon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// getActiveRoutesWithinBoundsTemplate has two substitution points filled in by
// GetActiveRoutesWithinBounds: %[1]s is the optional "WHERE routes.id IN (...)"
// route-ID filter (empty string when no candidate list is supplied), and %[2]d
// is the positional index of the MaxCount bind parameter, which shifts based on
// how many route-ID placeholders were inserted ahead of it.
const getActiveRoutesWithinBoundsTemplate = `
-- Calculate stop distance once per stop (not once per stop_time).
WITH nearby_stops AS (
    SELECT
        stops.id AS stop_id,
        -- Haversine distance in km between stop and the query's center.
        6371 * acos(
            cos(radians(?1)) *
            cos(radians(stops.lat)) *
            cos(radians(stops.lon) - radians(?2)) +
            sin(radians(?1)) *
            sin(radians(stops.lat))
        ) AS distance
    FROM stops
    JOIN stops_rtree r ON r.id = stops.rowid
    WHERE r.min_lat >= ?3 AND r.max_lat <= ?4
      AND r.min_lon >= ?5 AND r.max_lon <= ?6
),
stop_routes AS (
    SELECT DISTINCT stop_times.stop_id, trips.route_id
    FROM stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    WHERE stop_times.stop_id IN (SELECT stop_id FROM nearby_stops)
)
SELECT
    routes.id,
    routes.agency_id,
    routes.short_name,
    routes.long_name,
    routes."desc",
    routes.type,
    routes.url,
    routes.color,
    routes.text_color,
    routes.continuous_pickup,
    routes.continuous_drop_off,
    -- This column is not read from the response.
    MIN(ns.distance) AS min_distance
FROM stop_routes sr
JOIN nearby_stops ns ON ns.stop_id = sr.stop_id
JOIN routes ON routes.id = sr.route_id
%[1]s
GROUP BY routes.id
ORDER BY min_distance ASC
LIMIT ?%[2]d
`

// GetActiveRoutesWithinBoundsParams holds the bind parameters for GetActiveRoutesWithinBounds.
// RouteIDs is optional: when empty, no route-ID filter is applied.
type GetActiveRoutesWithinBoundsParams struct {
	Lat      float64
	Lon      float64
	MinLat   float64
	MaxLat   float64
	MinLon   float64
	MaxLon   float64
	RouteIDs []string
	MaxCount int
}

// GetActiveRoutesWithinBounds returns routes serving stops within the given bounding box,
// ordered by ascending distance from (Lat, Lon), optionally restricted to RouteIDs.
func (q *Queries) GetActiveRoutesWithinBounds(ctx context.Context, arg GetActiveRoutesWithinBoundsParams) ([]Route, error) {
	args := []any{arg.Lat, arg.Lon, arg.MinLat, arg.MaxLat, arg.MinLon, arg.MaxLon}

	routeFilter := ""
	if len(arg.RouteIDs) > 0 {
		// Only generated "?N" placeholder strings go into the SQL text; the
		// route IDs themselves are passed as bind values, so this is not a
		// SQL-injection vector.
		placeholders := make([]string, len(arg.RouteIDs))
		for i, routeID := range arg.RouteIDs {
			placeholders[i] = fmt.Sprintf("?%d", len(args)+1)
			args = append(args, routeID)
		}
		routeFilter = "WHERE routes.id IN (" + strings.Join(placeholders, ",") + ")"
	}
	args = append(args, arg.MaxCount)

	query := fmt.Sprintf(getActiveRoutesWithinBoundsTemplate, routeFilter, len(args))

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Route
	for rows.Next() {
		var i Route
		var distance float32
		if err := rows.Scan(
			&i.ID,
			&i.AgencyID,
			&i.ShortName,
			&i.LongName,
			&i.Desc,
			&i.Type,
			&i.Url,
			&i.Color,
			&i.TextColor,
			&i.ContinuousPickup,
			&i.ContinuousDropOff,
			&distance,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
