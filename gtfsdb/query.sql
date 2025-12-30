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
    platform_code,
    direction
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

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
OR REPLACE INTO shapes (shape_id, lat, lon, shape_pt_sequence, shape_dist_traveled)
VALUES
    (?, ?, ?, ?, ?) RETURNING *;

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
    shape_dist_traveled,
    timepoint
)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING *;

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
OR REPLACE INTO calendar_dates (service_id, date, exception_type)
VALUES
    (?, ?, ?) RETURNING *;

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
SELECT DISTINCT
    s.id
FROM
    stops s
    JOIN stop_times st ON s.id = st.stop_id
    JOIN trips t ON st.trip_id = t.id
    JOIN routes r ON t.route_id = r.id
WHERE
    r.agency_id = ?;

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

-- name: GetStopForAgency :one
-- Return the stop only if it is served by any route that belongs to the specified agency.
-- We join stop_times -> trips -> routes and filter by routes.agency_id to enforce agency ownership.
SELECT DISTINCT
    stops.*
FROM
    stops
    JOIN stop_times ON stops.id = stop_times.stop_id
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stops.id = ?
    AND routes.agency_id = ?;

-- name: ListStops :many
SELECT
    *
FROM
    stops
ORDER BY
    id;

-- name: GetRoutesForStop :many
SELECT DISTINCT
    routes.*
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
WHERE
    stop_times.stop_id = ?;

-- name: GetActiveStops :many
SELECT DISTINCT s.*
FROM stops s
INNER JOIN stop_times st ON s.id = st.stop_id;

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
    SELECT STRFTIME('%w', SUBSTR(?1, 1, 4) || '-' || SUBSTR(?1, 5, 2) || '-' || SUBSTR(?1, 7, 2)) AS weekday
),
base_services AS (
    SELECT c.id AS service_id
    FROM calendar c, formatted_date fd
    WHERE c.start_date <= ?1
      AND c.end_date >= ?1
      AND (
        (fd.weekday = '0' AND c.sunday = 1) OR
        (fd.weekday = '1' AND c.monday = 1) OR
        (fd.weekday = '2' AND c.tuesday = 1) OR
        (fd.weekday = '3' AND c.wednesday = 1) OR
        (fd.weekday = '4' AND c.thursday = 1) OR
        (fd.weekday = '5' AND c.friday = 1) OR
        (fd.weekday = '6' AND c.saturday = 1)
      )
),
removed_services AS (
    SELECT service_id
    FROM calendar_dates
    WHERE date = ?1
      AND exception_type = 2
),
added_services AS (
    SELECT service_id
    FROM calendar_dates
    WHERE date = ?1
      AND exception_type = 1
)
SELECT DISTINCT service_id
FROM base_services
WHERE service_id NOT IN (SELECT service_id FROM removed_services)
UNION
SELECT DISTINCT service_id FROM added_services;

-- name: GetTripsByRoute :many
SELECT * FROM trips WHERE route_id = ?;

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

-- name: GetScheduleForStopOnDate :many
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
    LEFT JOIN (
        SELECT c.id AS service_id
        FROM calendar c
        WHERE c.start_date <= @target_date
          AND c.end_date >= @target_date
          AND (
            (@weekday = 'sunday' AND c.sunday = 1) OR
            (@weekday = 'monday' AND c.monday = 1) OR
            (@weekday = 'tuesday' AND c.tuesday = 1) OR
            (@weekday = 'wednesday' AND c.wednesday = 1) OR
            (@weekday = 'thursday' AND c.thursday = 1) OR
            (@weekday = 'friday' AND c.friday = 1) OR
            (@weekday = 'saturday' AND c.saturday = 1)
          )
    ) base ON t.service_id = base.service_id
    LEFT JOIN (
        SELECT cd.service_id
        FROM calendar_dates cd
        WHERE cd.date = @target_date AND cd.exception_type = 2
    ) removed ON t.service_id = removed.service_id
    LEFT JOIN (
        SELECT cd.service_id
        FROM calendar_dates cd
        WHERE cd.date = @target_date AND cd.exception_type = 1
    ) added ON t.service_id = added.service_id
WHERE
    st.stop_id = @stop_id
    AND (
        (base.service_id IS NOT NULL AND removed.service_id IS NULL)
        OR
        added.service_id IS NOT NULL
    )
    AND r.id IN (sqlc.slice('route_ids'))
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

-- name: GetStopsWithActiveServiceOnDate :many
-- Returns stop IDs that have at least one trip with active service on the given date
SELECT DISTINCT st.stop_id
FROM stop_times st
JOIN trips t ON st.trip_id = t.id
WHERE st.stop_id IN (sqlc.slice('stop_ids'))
  AND t.service_id IN (sqlc.slice('service_ids'));

-- name: GetStopTimesForTrip :many
SELECT
    *
FROM
    stop_times
WHERE
    trip_id = ?
ORDER BY
    stop_sequence;

-- name: GetTripsByBlockID :many
SELECT
    id,
    route_id,
    service_id,
    trip_headsign,
    trip_short_name,
    direction_id,
    block_id,
    shape_id
FROM
    trips
WHERE
    block_id = ?;

-- name: GetCalendarByServiceID :one
SELECT
    *
FROM
    calendar
WHERE
    id = ?;

-- name: GetCalendarDateExceptionsForServiceID :many
SELECT
    *
FROM
    calendar_dates
WHERE
    service_id = ?;

-- name: GetStopsForRoute :many
SELECT DISTINCT
    stops.*
FROM
    stop_times
    JOIN trips ON stop_times.trip_id = trips.id
    JOIN routes ON trips.route_id = routes.id
    JOIN stops ON stop_times.stop_id = stops.id
WHERE
    routes.id = ?;

-- name: GetShapePointsByTripID :many
SELECT
    s.id,
    s.shape_id,
    s.lat,
    s.lon,
    s.shape_pt_sequence,
    s.shape_dist_traveled
FROM
    shapes s
    JOIN trips t ON t.shape_id = s.shape_id
WHERE
    t.id = ?
ORDER BY
    s.shape_pt_sequence ASC;

-- name: GetStopsWithShapeContextByIDs :many
SELECT 
    st.stop_id, 
    t.shape_id, 
    s.lat, 
    s.lon, 
    st.shape_dist_traveled
FROM stop_times st
JOIN trips t ON st.trip_id = t.id
JOIN stops s ON st.stop_id = s.id
WHERE st.stop_id IN (sqlc.slice('stop_ids'));    

-- name: GetTripsByBlockIDOrdered :many
SELECT
    t.id,
    t.block_id,
    t.service_id,
    MIN(st.departure_time) AS first_departure_time,
    MAX(st.arrival_time) AS last_arrival_time
FROM
    trips t
    JOIN stop_times st ON st.trip_id = t.id
WHERE
    t.block_id = ?
    AND t.service_id IN (sqlc.slice('service_ids'))
GROUP BY
    t.id,
    t.block_id,
    t.service_id
ORDER BY
    MIN(st.departure_time);

-- name: GetBlockIDByTripID :one
SELECT
    block_id
FROM
    trips
WHERE
    id = ?;

-- name: GetStopsByIDs :many
SELECT
    *
FROM
    stops
WHERE
    id IN (sqlc.slice('stop_ids'))
ORDER BY
    id;

-- name: GetRoutesByIDs :many
SELECT
    *
FROM
    routes
WHERE
    id IN (sqlc.slice('route_ids'))
ORDER BY
    id;

-- name: GetTripsByIDs :many
SELECT
    *
FROM
    trips
WHERE
    id IN (sqlc.slice('trip_ids'))
ORDER BY
    id;

-- name: GetBlockDetails :many
SELECT
    t.service_id,
    t.id as trip_id,
    t.route_id,
    st.arrival_time,
    st.departure_time,
    st.stop_id,
    st.stop_sequence,
    st.pickup_type,
    st.drop_off_type,
    s.lat,
    s.lon
FROM
    trips t
        JOIN
    stop_times st ON t.id = st.trip_id
        JOIN
    stops s ON st.stop_id = s.id
WHERE
    t.block_id = ?
ORDER BY
    t.id, st.stop_sequence;

-- name: GetStopTimesByStopIDs :many
SELECT
    *
FROM
    stop_times
WHERE
    stop_id IN (sqlc.slice('stop_ids'));

-- name: ListTrips :many
SELECT
    *
FROM
    trips;

-- name: GetArrivalsAndDeparturesForStop :many
SELECT
    st.trip_id,
    st.arrival_time,
    st.departure_time,
    st.stop_sequence,
    st.stop_headsign,
    t.service_id,
    t.route_id,
    t.trip_headsign,
    t.block_id,
    r.id as route_id,
    r.agency_id,
    r.short_name as route_short_name,
    r.long_name as route_long_name
FROM
    stop_times st
        JOIN trips t ON st.trip_id = t.id
        JOIN routes r ON t.route_id = r.id
WHERE
    st.stop_id = ?
ORDER BY
    st.arrival_time LIMIT 50;
-- name: GetTripsByServiceID :many
SELECT *
FROM trips
WHERE service_id IN (sqlc.slice('service_ids'));

-- name: GetShapePointsForTrip :many
SELECT DISTINCT shapes.lat, shapes.lon, shapes.shape_pt_sequence
FROM shapes
JOIN trips ON trips.shape_id = shapes.shape_id
WHERE trips.id = ?
ORDER BY shapes.shape_pt_sequence;

-- name: GetNextStopInTrip :one
SELECT stops.lat, stops.lon, stops.id
FROM stop_times
JOIN stops ON stops.id = stop_times.stop_id
WHERE stop_times.trip_id = ?
  AND stop_times.stop_sequence > ?
ORDER BY stop_times.stop_sequence ASC
LIMIT 1;

-- name: GetStopsWithTripContext :many
SELECT
    s.id, s.lat, s.lon, s.name, s.code,
    st.trip_id, st.stop_sequence,
    t.shape_id
FROM stops s
JOIN stop_times st ON s.id = st.stop_id
JOIN trips t ON st.trip_id = t.id
WHERE s.id = ?;

-- name: GetStopTimesForStopInWindow :many
SELECT
    st.*,
    t.route_id,
    t.service_id,
    t.trip_headsign,
    t.block_id
FROM stop_times st
         JOIN trips t ON st.trip_id = t.id
WHERE st.stop_id = @stop_id
  AND (
    (st.arrival_time BETWEEN @window_start_nanos AND @window_end_nanos)
        OR
    (st.departure_time BETWEEN @window_start_nanos AND @window_end_nanos)
    )
ORDER BY st.arrival_time;

-- name: UpdateStopDirection :exec
UPDATE stops
SET direction = ?
WHERE id = ?;

-- name: GetStopsWithShapeContext :many
SELECT
    s.id, s.lat, s.lon, s.name, s.code, s.direction,
    st.trip_id, st.stop_sequence, st.shape_dist_traveled,
    t.shape_id
FROM stops s
JOIN stop_times st ON s.id = st.stop_id
JOIN trips t ON st.trip_id = t.id
WHERE s.id = ?;

-- name: GetShapePointWindow :many
SELECT lat, lon, shape_pt_sequence, shape_dist_traveled
FROM shapes
WHERE shape_id = ?
  AND shape_pt_sequence BETWEEN ? AND ?
ORDER BY shape_pt_sequence;

-- name: GetShapePointsWithDistance :many
SELECT lat, lon, shape_pt_sequence, shape_dist_traveled
FROM shapes
WHERE shape_id = ?
ORDER BY shape_pt_sequence;



-- BlockTripIndex queries

-- name: CreateBlockTripIndex :one
INSERT INTO block_trip_index (
    index_key,
    service_ids,
    stop_sequence_key,
    created_at
)
VALUES
    (?, ?, ?, ?)
RETURNING id;

-- name: CreateBlockTripEntry :exec
INSERT INTO block_trip_entry (
    block_trip_index_id,
    trip_id,
    block_id,
    service_id,
    block_trip_sequence
)
VALUES
    (?, ?, ?, ?, ?);

-- name: ClearBlockTripEntries :exec
DELETE FROM block_trip_entry;

-- name: ClearBlockTripIndices :exec
DELETE FROM block_trip_index;

-- name: GetBlockTripIndexIDsForRoute :many
-- Get all block_trip_index IDs that contain trips for the specified route and service IDs
SELECT DISTINCT bti.id
FROM block_trip_index bti
JOIN block_trip_entry bte ON bti.id = bte.block_trip_index_id
JOIN trips t ON bte.trip_id = t.id
WHERE t.route_id = sqlc.arg('route_id')
  AND bte.service_id IN (sqlc.slice('service_ids'))
ORDER BY bti.id;

-- name: GetTripsByBlockTripIndexIDs :many
-- Get all trips that belong to the specified block_trip_index IDs within a time window
-- Matches Java's findBlockTripsInRange logic using binary search on maxDepartures and minArrivals
-- A trip is active if: maxDeparture >= timeFrom (could have started) AND minArrival <= timeTo (could still be active)
SELECT DISTINCT
    t.id, t.route_id, t.service_id, t.trip_headsign, t.trip_short_name,
    t.direction_id, t.block_id, t.shape_id, t.wheelchair_accessible, t.bikes_allowed,
    bte.block_trip_sequence
FROM trips t
JOIN block_trip_entry bte ON t.id = bte.trip_id
WHERE bte.block_trip_index_id IN (sqlc.slice('index_ids'))
  AND bte.service_id IN (sqlc.slice('service_ids'))
  AND EXISTS (
    -- Check if trip could be active: maxDeparture >= timeFrom AND minArrival <= timeTo
    SELECT 1
    FROM stop_times st
    WHERE st.trip_id = t.id
    GROUP BY st.trip_id
    HAVING MAX(st.departure_time) >= sqlc.arg('from_time')
       AND MIN(st.arrival_time) <= sqlc.arg('to_time')
  )
ORDER BY t.route_id, bte.block_trip_sequence, t.id;

-- name: GetActiveTripForRouteAtTime :one
-- Find the ONE trip from a specific route that is active at the given time
-- A trip is active if current_time falls within its stop times
SELECT
    t.id, t.route_id, t.service_id, t.trip_headsign, t.trip_short_name,
    t.direction_id, t.block_id, t.shape_id, t.wheelchair_accessible, t.bikes_allowed
FROM trips t
JOIN block_trip_entry bte ON t.id = bte.trip_id
JOIN stop_times st_first ON t.id = st_first.trip_id AND st_first.stop_sequence = (
    SELECT MIN(stop_sequence) FROM stop_times WHERE trip_id = t.id
)
JOIN stop_times st_last ON t.id = st_last.trip_id AND st_last.stop_sequence = (
    SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = t.id
)
WHERE bte.block_trip_index_id IN (sqlc.slice('index_ids'))
  AND t.route_id = sqlc.arg('route_id')
  AND bte.service_id IN (sqlc.slice('service_ids'))
  AND st_first.departure_time <= sqlc.arg('current_time')
  AND st_last.arrival_time >= sqlc.arg('from_time')
ORDER BY st_first.departure_time DESC
LIMIT 1;

-- name: GetBlockTripIndexIDsForBlocks :many
-- Get all BlockTripIndex IDs that contain trips from the specified blocks
SELECT DISTINCT bte.block_trip_index_id
FROM block_trip_entry bte
WHERE bte.block_id IN (sqlc.slice('block_ids'))
  AND bte.service_id IN (sqlc.slice('service_ids'))
ORDER BY bte.block_trip_index_id;

-- name: GetBlocksForBlockTripIndexIDs :many
-- Get all distinct block_ids that have trips in the specified BlockTripIndex IDs
SELECT DISTINCT bte.block_id
FROM block_trip_entry bte
WHERE bte.block_trip_index_id IN (sqlc.slice('index_ids'))
  AND bte.service_id IN (sqlc.slice('service_ids'))
  AND bte.block_id IS NOT NULL;

-- name: GetActiveTripInBlockAtTime :one
-- Find the currently active trip in a specific block at the given time
-- Returns the trip whose stop times contain the current time (with late/early windows)
-- Orders by departure time ASC to get the EARLIEST matching trip (the one currently in progress)
SELECT t.id
FROM trips t
JOIN stop_times st_first ON t.id = st_first.trip_id AND st_first.stop_sequence = (
        SELECT MIN(stop_sequence) FROM stop_times WHERE trip_id = t.id
)
JOIN stop_times st_last ON t.id = st_last.trip_id AND st_last.stop_sequence = (
        SELECT MAX(stop_sequence) FROM stop_times WHERE trip_id = t.id
)
WHERE t.block_id = :block_id
    AND t.service_id IN (sqlc.slice('service_ids'))
    AND st_first.departure_time <= :current_time
    AND st_last.arrival_time >= :current_time
ORDER BY st_first.departure_time ASC
LIMIT 1;

-- name: GetTripsInBlock :many
-- Get all trip IDs in a specific block for the given service IDs
SELECT id
FROM trips
WHERE block_id = sqlc.arg('block_id')
  AND service_id IN (sqlc.slice('service_ids'));

-- name: GetRoutesInBlockTripIndices :many
-- Get all unique route IDs that have trips in the specified block_trip_index IDs
SELECT DISTINCT t.route_id
FROM trips t
JOIN block_trip_entry bte ON t.id = bte.trip_id
WHERE bte.block_trip_index_id IN (sqlc.slice('index_ids'))
  AND bte.service_id IN (sqlc.slice('service_ids'));


-- name: GetShapePointsByIDs :many
SELECT * FROM shapes
WHERE shape_id IN (sqlc.slice('shape_ids'))
ORDER BY shape_id, shape_pt_sequence;
