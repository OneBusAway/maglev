package gtfsdb

import (
	"context"
)

// Implemented manually because sqlc doesn't support the virtual tables from the RTree module.

const getActiveStopsWithinBounds = `
SELECT DISTINCT
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
INNER JOIN stop_times st ON s.id = st.stop_id
INNER JOIN stops_rtree r ON r.id = s.rowid
WHERE r.min_lat >= ? AND r.max_lat <= ?
  AND r.min_lon >= ? AND r.max_lon <= ?
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
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const getActiveRoutesWithinBounds = `
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
    -- Haversine distance in km betweeen stop and the query's center.
    -- This column is not read from the response
    MIN(6371 * acos(
        cos(radians(?1)) *
        cos(radians(stops.lat)) *
        cos(radians(stops.lon) - radians(?2)) +
        sin(radians(?1)) *
        sin(radians(stops.lat))
    )) AS min_distance
FROM
    stops
    JOIN stop_times ON stops.id = stop_times.stop_id
    JOIN stops_rtree r ON r.id = stops.rowid
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    r.min_lat >= ?3 AND r.max_lat <= ?4
    AND r.min_lon >= ?5 AND r.max_lon <= ?6
    -- use LIKE for case-insensitive compare.
    AND (?7 == "" OR routes.short_name LIKE ?7)
GROUP BY
    routes.id
ORDER BY
    min_distance ASC
LIMIT
    ?8
`

type GetActiveRoutesWithinBoundsParams struct {
	Lat       float64
	Lon       float64
	MinLat    float64
	MaxLat    float64
	MinLon    float64
	MaxLon    float64
	ShortName string
	MaxCount  int
}

func (q *Queries) GetActiveRoutesWithinBounds(ctx context.Context, arg GetActiveRoutesWithinBoundsParams) ([]Route, error) {
	rows, err := q.db.QueryContext(ctx, getActiveRoutesWithinBounds,
		arg.Lat, arg.Lon, arg.MinLat, arg.MaxLat, arg.MinLon, arg.MaxLon, arg.ShortName, arg.MaxCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // closing is also checked explicitly below
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
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
