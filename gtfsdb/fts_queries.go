package gtfsdb

// Hand-written FTS5 query implementations.
// sqlc cannot handle queries that use FTS5-specific syntax (MATCH operator,
// bm25() function), so these are maintained manually instead of in query.sql.
//
// IMPORTANT: If the 'routes', 'stops', 'routes_fts', or 'stops_fts' table schemas change,
// the SQL and Go types in this file must be updated manually to match.
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
  -- A stop qualifies only if some stop time permits unrestricted pick-up or
  -- drop-off (pickup_type/drop_off_type == 0), matching the legacy
  -- stopHasRevenueService predicate. Types 2 (phone agency) and 3 (coordinate
  -- with driver) are restricted and intentionally do not qualify.
  --
  -- The COALESCE exists because toNullInt64 (gtfsdb/helpers.go) persists a
  -- parsed 0 as NULL, so NULL and 0 both mean unrestricted here.
  --
  -- This predicate is written for the storage chain GTFS specifies, not the one
  -- we currently have. GTFS leaves pickup_type/drop_off_type optional and
  -- defines an empty value as 0, but the pinned go-gtfs v1.1.1 does not:
  -- parsePickupDropOffPolicy (enums.go) falls through to PickupDropOffPolicy_No
  -- for anything that is not "0", "2" or "3", so a blank or absent column is
  -- parsed as 1 and stored as 1. On a feed that omits those columns this EXISTS
  -- matches nothing and the search returns no stops. See
  -- https://github.com/OneBusAway/go-gtfs/pull/5 for the parser fix; once that
  -- is released and go.mod is bumped, blank and absent parse to 0 and this
  -- comment should be rewritten to describe the fixed chain.
  -- TestImportedStopTimesOmittingPickupColumns fails until then.
  AND EXISTS (
      SELECT 1
      FROM stop_times st
      WHERE st.stop_id = s.id
        AND (
            COALESCE(st.pickup_type, 0) = 0
            OR COALESCE(st.drop_off_type, 0) = 0
        )
  )
ORDER BY s.id
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
