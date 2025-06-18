-- name: GetAgency :one
SELECT
    *
FROM
    agencies
WHERE
    id = ?
LIMIT
    1;

-- name: ListAgencies :many
SELECT
    *
FROM
    agencies
ORDER BY
    id;

-- name: CreateAgency :one
INSERT
OR REPLACE INTO agencies (
    id,
    name,
    url,
    timezone,
    lang,
    phone,
    fare_url,
    email
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateRoute :one
INSERT
OR REPLACE INTO routes (
    id,
    agency_id,
    short_name,
    long_name,
    desc,
    type,
    url,
    color,
    text_color,
    continuous_pickup,
    continuous_drop_off
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateStop :one
INSERT
OR REPLACE INTO stops (
    id,
    code,
    name,
    desc,
    lat,
    lon,
    zone_id,
    url,
    location_type,
    timezone,
    wheelchair_boarding,
    platform_code
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateCalendar :one
INSERT
OR REPLACE INTO calendar (
    id,
    monday,
    tuesday,
    wednesday,
    thursday,
    friday,
    saturday,
    sunday,
    start_date,
    end_date
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateShape :one
INSERT
OR REPLACE INTO shapes (shape_id, lat, lon, shape_pt_sequence)
VALUES
    (?, ?, ?, ?) RETURNING *;

-- name: CreateStopTime :one
INSERT
OR REPLACE INTO stop_times (
    trip_id,
    arrival_time,
    departure_time,
    stop_id,
    stop_sequence,
    stop_headsign,
    pickup_type,
    drop_off_type,
    timepoint
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateTrip :one
INSERT
OR REPLACE INTO trips (
    id,
    route_id,
    service_id,
    trip_headsign,
    trip_short_name,
    direction_id,
    block_id,
    shape_id,
    wheelchair_accessible,
    bikes_allowed
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

-- name: CreateCalendarDate :one
INSERT
OR REPLACE INTO calendar_dates(service_id, date, exception_type)
VALUES (?, ?, ?) RETURNING *;

-- name: ListRoutes :many
SELECT
    id,
    agency_id,
    short_name,
    long_name,
    "desc",
    type,
    url,
    color,
    text_color,
    continuous_pickup,
    continuous_drop_off
FROM
    routes
ORDER BY
    agency_id,
    id;

-- name: GetRouteIDsForAgency :many
SELECT
    r.id
FROM
    routes r
    JOIN agencies a ON r.agency_id = a.id
WHERE
    a.id = ?;

-- name: GetRouteIDsForStop :many
SELECT DISTINCT
    (routes.agency_id || '_' || routes.id) AS route_id
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stop_times.stop_id = ?;

-- name: GetAgencyForStop :one
SELECT DISTINCT
    a.id,
    a.name,
    a.url,
    a.timezone,
    a.lang,
    a.phone,
    a.fare_url,
    a.email
FROM
    agencies a
    JOIN routes r ON a.id = r.agency_id
    JOIN trips t ON r.id = t.route_id
    JOIN stop_times st ON t.id = st.trip_id
WHERE
    st.stop_id = ?
ORDER BY
    a.id
LIMIT
    1;

-- name: GetStopIDsForAgency :many
SELECT
    s.id
FROM
    stops s;

-- name: GetTrip :one
SELECT
    *
FROM
    trips
WHERE
    id = ?;

-- name: GetRoute :one
SELECT
    *
FROM
    routes
WHERE
    id = ?;

-- name: GetStop :one
SELECT
    *
FROM
    stops
WHERE
    id = ?;

-- name: GetRoutesForStop :many
SELECT DISTINCT
    routes.*
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stop_times.stop_id = ?;

-- name: GetAllShapes :many
SELECT
    *
FROM
    shapes;

-- name: GetShapeByID :many
SELECT
    *
FROM
    shapes
WHERE
    shape_id = ?;

-- name: GetStopIDsForRoute :many
SELECT DISTINCT
    stop_times.stop_id
FROM
    stop_times
        JOIN trips ON stop_times.trip_id = trips.id
WHERE
    trips.route_id = ?;

-- name: GetAllTripsForRoute :many
SELECT DISTINCT *
FROM trips t
WHERE t.route_id = @route_id
ORDER BY t.direction_id, t.trip_headsign;

-- name: GetStopIDsForTrip :many
SELECT DISTINCT
    stop_times.stop_id
FROM
    stop_times
WHERE
    stop_times.trip_id = ?;
-- name: GetShapesGroupedByTripHeadSign :many
SELECT DISTINCT s.lat, s.lon, s.shape_pt_sequence
FROM shapes s
         JOIN (
    SELECT shape_id
    FROM trips
    WHERE route_id = @route_id
      AND trip_headsign = @trip_headsign
      AND shape_id IS NOT NULL
    LIMIT 1
) t ON s.shape_id = t.shape_id
ORDER BY s.shape_pt_sequence;
-- name: GetActiveServiceIDsForDate :many
WITH formatted_date AS (
    SELECT STRFTIME('%w', SUBSTR(@target_date, 1, 4) || '-' || SUBSTR(@target_date, 5, 2) || '-' || SUBSTR(@target_date, 7, 2)) AS weekday
)
SELECT DISTINCT c.id AS service_id
FROM calendar c, formatted_date fd
WHERE c.start_date <= @target_date
  AND c.end_date >= @target_date
  AND (
    (fd.weekday = '0' AND c.sunday = 1) OR
    (fd.weekday = '1' AND c.monday = 1) OR
    (fd.weekday = '2' AND c.tuesday = 1) OR
    (fd.weekday = '3' AND c.wednesday = 1) OR
    (fd.weekday = '4' AND c.thursday = 1) OR
    (fd.weekday = '5' AND c.friday = 1) OR
    (fd.weekday = '6' AND c.saturday = 1)
    )
UNION
SELECT DISTINCT service_id
FROM calendar_dates
WHERE date = @target_date
  AND exception_type = 1;

-- name: GetTripsForRouteInActiveServiceIDs :many
SELECT DISTINCT *
FROM trips t
WHERE t.route_id = @route_id
  AND t.service_id IN (sqlc.slice(('service_ids')))
ORDER BY t.direction_id, t.trip_headsign;

-- name: GetOrderedStopIDsForTrip :many
SELECT stop_id
FROM stop_times
WHERE trip_id = ?
ORDER BY stop_sequence;
-- name: GetScheduleForStop :many
SELECT
    st.trip_id,
    st.arrival_time,
    st.departure_time,
    st.stop_headsign,
    t.service_id,
    t.route_id,
    t.trip_headsign,
    r.id as route_id,
    r.agency_id
FROM
    stop_times st
    JOIN trips t ON st.trip_id = t.id
    JOIN routes r ON t.route_id = r.id
WHERE
    st.stop_id = ?
ORDER BY
    r.id, st.arrival_time;

-- name: GetImportMetadata :one
SELECT
    *
FROM
    import_metadata
WHERE
    id = 1;

-- name: UpsertImportMetadata :one
INSERT
OR REPLACE INTO import_metadata (
    id,
    file_hash,
    import_time,
    file_source
)
VALUES
    (1, ?, ?, ?) RETURNING *;

-- name: ClearStopTimes :exec
DELETE FROM stop_times;

-- name: ClearShapes :exec
DELETE FROM shapes;

-- name: ClearTrips :exec
DELETE FROM trips;

-- name: ClearCalendar :exec
DELETE FROM calendar;

-- name: ClearStops :exec
DELETE FROM stops;

-- name: ClearRoutes :exec
DELETE FROM routes;

-- name: ClearAgencies :exec
DELETE FROM agencies;

-- Batch queries to solve N+1 problems

-- name: GetRoutesForStops :many
SELECT DISTINCT
    routes.*,
    stop_times.stop_id
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stop_times.stop_id IN (sqlc.slice('stop_ids'));

-- name: GetRouteIDsForStops :many
SELECT DISTINCT
    routes.agency_id || '_' || routes.id AS route_id,
    stop_times.stop_id
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stop_times.stop_id IN (sqlc.slice('stop_ids'));

-- name: GetAgenciesForStops :many
SELECT DISTINCT
    a.id,
    a.name,
    a.url,
    a.timezone,
    a.lang,
    a.phone,
    a.fare_url,
    a.email,
    stop_times.stop_id
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
    JOIN agencies a ON routes.agency_id = a.id
WHERE
    stop_times.stop_id IN (sqlc.slice('stop_ids'));
