package gtfsdb

// Hand-written FTS5 query implementations.
// sqlc cannot handle queries that use FTS5-specific syntax (MATCH operator,
// bm25() function), so these are maintained manually instead of in query.sql.
//
// IMPORTANT: If the 'routes' or 'stops' table schema changes, the SQL
// and Go types in this file must be updated manually to match.
// Running 'make models' will NOT update this file.

import (
	"context"
	"database/sql"
)

const searchRoutesByFullText = `
SELECT
    r.id,
    r.agency_id,
    r.short_name,
    r.long_name,
    r."desc",
    r.type,
    r.url,
    r.color,
    r.text_color,
    r.continuous_pickup,
    r.continuous_drop_off
FROM
    routes_fts
    JOIN routes r ON r.rowid = routes_fts.rowid
WHERE
    routes_fts MATCH ?
ORDER BY
    bm25(routes_fts),
    r.agency_id,
    r.id
LIMIT
    ?
`

type SearchRoutesByFullTextParams struct {
	Query string
	Limit int64
}

func (q *Queries) SearchRoutesByFullText(ctx context.Context, arg SearchRoutesByFullTextParams) ([]Route, error) {
	// nil stmt: FTS queries are not prepared since they're not managed by sqlc.
	rows, err := q.query(ctx, nil, searchRoutesByFullText, arg.Query, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // closing is also checked explicitly below
	var items []Route
	for rows.Next() {
		var i Route
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

const searchStopsByName = `
SELECT
    s.id,
    s.code,
    s.name,
    s.lat,
    s.lon,
    s.location_type,
    s.wheelchair_boarding,
    s.direction,
    s.parent_station
FROM stops s
JOIN stops_fts fts
  ON s.rowid = fts.rowid
WHERE fts.stop_name MATCH ?
ORDER BY s.name
LIMIT ?
`

type SearchStopsByNameParams struct {
	SearchQuery string
	Limit       int64
}

type SearchStopsByNameRow struct {
	ID                 string
	Code               sql.NullString
	Name               sql.NullString
	Lat                float64
	Lon                float64
	LocationType       sql.NullInt64
	WheelchairBoarding sql.NullInt64
	Direction          sql.NullString
	ParentStation      sql.NullString
}

func (q *Queries) SearchStopsByName(ctx context.Context, arg SearchStopsByNameParams) ([]SearchStopsByNameRow, error) {
	// nil stmt: FTS queries are not prepared since they're not managed by sqlc.
	rows, err := q.query(ctx, nil, searchStopsByName, arg.SearchQuery, arg.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // closing is also checked explicitly below
	var items []SearchStopsByNameRow
	for rows.Next() {
		var i SearchStopsByNameRow
		if err := rows.Scan(
			&i.ID,
			&i.Code,
			&i.Name,
			&i.Lat,
			&i.Lon,
			&i.LocationType,
			&i.WheelchairBoarding,
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
