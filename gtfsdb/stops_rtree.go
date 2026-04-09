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
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
