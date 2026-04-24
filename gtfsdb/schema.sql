PRAGMA foreign_keys = ON;

-- migrate
CREATE TABLE
    IF NOT EXISTS agencies (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        url TEXT NOT NULL,
        timezone TEXT NOT NULL,
        lang TEXT,
        phone TEXT,
        fare_url TEXT,
        email TEXT
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS routes (
        id TEXT PRIMARY KEY,
        agency_id TEXT NOT NULL,
        short_name TEXT,
        long_name TEXT,
        desc TEXT,
        type INTEGER NOT NULL CHECK (type >= 0),
        url TEXT,
        color TEXT CHECK (color IS NULL OR length(color) = 6),
        text_color TEXT CHECK (text_color IS NULL OR length(text_color) = 6),
        continuous_pickup INTEGER CHECK (continuous_pickup IS NULL OR continuous_pickup BETWEEN 0 AND 3),
        continuous_drop_off INTEGER CHECK (continuous_drop_off IS NULL OR continuous_drop_off BETWEEN 0 AND 3),
        FOREIGN KEY (agency_id) REFERENCES agencies (id)
    ) STRICT;

-- migrate
-- FTS5 external content table for full-text route search.
-- Data lives in 'routes' table; only the search index is stored here.
-- The triggers below keep the index synchronized with the content table.
CREATE VIRTUAL TABLE IF NOT EXISTS routes_fts USING fts5 (
    id UNINDEXED,
    agency_id UNINDEXED,
    short_name,
    long_name,
    desc,
    content = 'routes',
    content_rowid = 'rowid'
);

-- migrate
-- Trigger naming: ai=After Insert, ad=After Delete, au=After Update
CREATE TRIGGER IF NOT EXISTS routes_fts_ai AFTER INSERT ON routes BEGIN
INSERT INTO
    routes_fts(rowid, id, agency_id, short_name, long_name, desc)
VALUES
    (
        new.rowid,
        new.id,
        new.agency_id,
        coalesce(new.short_name, ''),
        coalesce(new.long_name, ''),
        coalesce(new.desc, '')
    );
END;

-- migrate
CREATE TRIGGER IF NOT EXISTS routes_fts_ad AFTER DELETE ON routes BEGIN
INSERT INTO
    routes_fts(routes_fts, rowid, id, agency_id, short_name, long_name, desc)
VALUES
    (
        'delete',
        old.rowid,
        old.id,
        old.agency_id,
        coalesce(old.short_name, ''),
        coalesce(old.long_name, ''),
        coalesce(old.desc, '')
    );
END;

-- migrate
CREATE TRIGGER IF NOT EXISTS routes_fts_au AFTER UPDATE ON routes BEGIN
INSERT INTO
    routes_fts(routes_fts, rowid, id, agency_id, short_name, long_name, desc)
VALUES
    (
        'delete',
        old.rowid,
        old.id,
        old.agency_id,
        coalesce(old.short_name, ''),
        coalesce(old.long_name, ''),
        coalesce(old.desc, '')
    );
INSERT INTO
    routes_fts(rowid, id, agency_id, short_name, long_name, desc)
VALUES
    (
        new.rowid,
        new.id,
        new.agency_id,
        coalesce(new.short_name, ''),
        coalesce(new.long_name, ''),
        coalesce(new.desc, '')
    );
END;

-- migrate
INSERT INTO routes_fts(routes_fts) VALUES ('rebuild');

-- migrate
CREATE TABLE
    IF NOT EXISTS stops (
        id TEXT PRIMARY KEY,
        code TEXT,
        name TEXT,
        desc TEXT,
        lat REAL NOT NULL CHECK (lat BETWEEN -90 AND 90),
        lon REAL NOT NULL CHECK (lon BETWEEN -180 AND 180),
        zone_id TEXT,
        url TEXT,
        location_type INTEGER DEFAULT 0 CHECK (location_type BETWEEN 0 AND 5),
        timezone TEXT,
        wheelchair_boarding INTEGER DEFAULT 0 CHECK (wheelchair_boarding IN (0, 1, 2)),
        platform_code TEXT,
        direction TEXT,
        parent_station TEXT,
        -- Add DEFERRABLE so that we don't get constraint violations during bulk inserts.
        FOREIGN KEY (parent_station) REFERENCES stops (id) DEFERRABLE INITIALLY DEFERRED
    ) STRICT;

-- migrate
CREATE VIRTUAL TABLE IF NOT EXISTS stops_rtree USING rtree (
    id, -- Integer primary key for the R*Tree
    min_lat,
    max_lat, -- Latitude bounds
    min_lon,
    max_lon -- Longitude bounds
)
/* stops_rtree(id,min_lat,max_lat,min_lon,max_lon) */;

-- migrate
CREATE TABLE
    IF NOT EXISTS "stops_rtree_rowid" (rowid INTEGER PRIMARY KEY, nodeno);

-- migrate
CREATE TABLE
    IF NOT EXISTS "stops_rtree_node" (nodeno INTEGER PRIMARY KEY, data);

-- migrate
CREATE TABLE
    IF NOT EXISTS "stops_rtree_parent" (nodeno INTEGER PRIMARY KEY, parentnode);

-- migrate
CREATE TRIGGER IF NOT EXISTS stops_rtree_insert_trigger AFTER INSERT ON stops BEGIN
INSERT INTO
    stops_rtree (id, min_lat, max_lat, min_lon, max_lon)
VALUES
    (new.rowid, new.lat, new.lat, new.lon, new.lon);

END;

-- migrate
CREATE TRIGGER IF NOT EXISTS stops_rtree_update_trigger AFTER
UPDATE ON stops BEGIN
UPDATE stops_rtree
SET
    min_lat = new.lat,
    max_lat = new.lat,
    min_lon = new.lon,
    max_lon = new.lon
WHERE
    id = old.rowid;

END;

-- migrate
CREATE TRIGGER IF NOT EXISTS stops_rtree_delete_trigger AFTER DELETE ON stops BEGIN
DELETE FROM stops_rtree
WHERE
    id = old.rowid;

END;

-- FTS5 external content table for full-text stop search.
-- Data lives in 'stops' table; only the search index is stored here.
-- migrate
CREATE VIRTUAL TABLE IF NOT EXISTS stops_fts USING fts5(
    id UNINDEXED,
    stop_name,
    tokenize = 'porter'
);

-- The triggers below keep the index synchronized with the content table.
-- migrate
DROP TRIGGER IF EXISTS stops_fts_insert_trigger;
CREATE TRIGGER IF NOT EXISTS stops_fts_insert_trigger
AFTER INSERT ON stops
BEGIN
    INSERT INTO stops_fts (rowid, id, stop_name)
    VALUES (new.rowid, new.id, new.name);
END;

-- migrate
DROP TRIGGER IF EXISTS stops_fts_update_trigger;
CREATE TRIGGER IF NOT EXISTS stops_fts_update_trigger
AFTER UPDATE ON stops
BEGIN
    DELETE FROM stops_fts WHERE rowid = old.rowid;
    INSERT INTO stops_fts (rowid, id, stop_name)
    VALUES (new.rowid, new.id, new.name);
END;

-- migrate
DROP TRIGGER IF EXISTS stops_fts_delete_trigger;
CREATE TRIGGER IF NOT EXISTS stops_fts_delete_trigger
AFTER DELETE ON stops
BEGIN
    DELETE FROM stops_fts WHERE rowid = old.rowid;
END;

-- migrate
CREATE TABLE
    IF NOT EXISTS calendar (
        id TEXT PRIMARY KEY,
        monday INTEGER NOT NULL CHECK (monday IN (0, 1)),
        tuesday INTEGER NOT NULL CHECK (tuesday IN (0, 1)),
        wednesday INTEGER NOT NULL CHECK (wednesday IN (0, 1)),
        thursday INTEGER NOT NULL CHECK (thursday IN (0, 1)),
        friday INTEGER NOT NULL CHECK (friday IN (0, 1)),
        saturday INTEGER NOT NULL CHECK (saturday IN (0, 1)),
        sunday INTEGER NOT NULL CHECK (sunday IN (0, 1)),
        start_date TEXT NOT NULL,
        end_date TEXT NOT NULL,
        CHECK (start_date <= end_date)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS trips (
        id TEXT PRIMARY KEY,
        route_id TEXT NOT NULL,
        service_id TEXT NOT NULL,
        trip_headsign TEXT,
        trip_short_name TEXT,
        direction_id INTEGER CHECK (direction_id IS NULL OR direction_id IN (0, 1)),
        block_id TEXT,
        shape_id TEXT,
        wheelchair_accessible INTEGER DEFAULT 0 CHECK (wheelchair_accessible IN (0, 1, 2)),
        bikes_allowed INTEGER DEFAULT 0 CHECK (bikes_allowed IN (0, 1, 2)),
        min_arrival_time INTEGER,    -- Cached MIN(stop_times.arrival_time)
        max_departure_time INTEGER,  -- Cached MAX(stop_times.departure_time)
        CHECK (min_arrival_time IS NULL OR max_departure_time IS NULL OR min_arrival_time <= max_departure_time),
        FOREIGN KEY (route_id) REFERENCES routes (id),
        FOREIGN KEY (service_id) REFERENCES calendar (id)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS shapes (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        shape_id TEXT NOT NULL,
        lat REAL NOT NULL CHECK (lat BETWEEN -90 AND 90),
        lon REAL NOT NULL CHECK (lon BETWEEN -180 AND 180),
        shape_pt_sequence INTEGER NOT NULL CHECK (shape_pt_sequence >= 0),
        shape_dist_traveled REAL,
        UNIQUE (shape_id, shape_pt_sequence)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS stop_times (
        trip_id TEXT NOT NULL,
        arrival_time INTEGER NOT NULL,
        departure_time INTEGER NOT NULL,
        stop_id TEXT NOT NULL,
        stop_sequence INTEGER NOT NULL CHECK (stop_sequence >= 0),
        stop_headsign TEXT,
        pickup_type INTEGER DEFAULT 0 CHECK (pickup_type IS NULL OR pickup_type BETWEEN 0 AND 3),
        drop_off_type INTEGER DEFAULT 0 CHECK (drop_off_type IS NULL OR drop_off_type BETWEEN 0 AND 3),
        shape_dist_traveled REAL,
        timepoint INTEGER DEFAULT 1 CHECK (timepoint IN (0, 1)),
        CHECK (arrival_time <= departure_time),
        FOREIGN KEY (trip_id) REFERENCES trips (id),
        FOREIGN KEY (stop_id) REFERENCES stops (id),
        PRIMARY KEY (trip_id, stop_sequence)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS frequencies (
        trip_id TEXT NOT NULL,
        start_time INTEGER NOT NULL, -- Nanoseconds since midnight (matches stop_times convention)
        end_time INTEGER NOT NULL, -- Nanoseconds since midnight
        headway_secs INTEGER NOT NULL CHECK (headway_secs > 0), -- Headway in seconds
        exact_times INTEGER NOT NULL DEFAULT 0 CHECK (exact_times IN (0, 1)), -- 0 = frequency-based, 1 = schedule-based
        CHECK (start_time < end_time),
        FOREIGN KEY (trip_id) REFERENCES trips (id),
        PRIMARY KEY (trip_id, start_time)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS calendar_dates (
        service_id TEXT NOT NULL,
        date TEXT NOT NULL,
        exception_type INTEGER NOT NULL CHECK (exception_type IN (1, 2)),
        PRIMARY KEY (service_id, date),
        FOREIGN KEY (service_id) REFERENCES calendar (id)
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS import_metadata (
        id INTEGER PRIMARY KEY CHECK (id = 1), -- Only allow one row
        file_hash TEXT NOT NULL,
        import_time INTEGER NOT NULL,
        file_source TEXT NOT NULL
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS block_trip_index (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        index_key TEXT NOT NULL UNIQUE, -- Hash or canonical key: service_ids + stop_sequence
        service_ids TEXT NOT NULL, -- Comma-separated sorted service IDs
        stop_sequence_key TEXT NOT NULL, -- Canonical ordered stop sequence (e.g., "stop1|stop2|stop3")
        created_at INTEGER NOT NULL
    ) STRICT;

-- migrate
CREATE TABLE
    IF NOT EXISTS block_trip_entry (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        block_trip_index_id INTEGER NOT NULL,
        trip_id TEXT NOT NULL,
        block_id TEXT,
        service_id TEXT NOT NULL,
        block_trip_sequence INTEGER NOT NULL CHECK (block_trip_sequence >= 0), -- Order of trip within the block
        FOREIGN KEY (block_trip_index_id) REFERENCES block_trip_index (id),
        FOREIGN KEY (trip_id) REFERENCES trips (id)
    ) STRICT;

-- migrate
CREATE INDEX IF NOT EXISTS idx_routes_agency_id ON routes (agency_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_route_id ON trips (route_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_service_id ON trips (service_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_trip_id ON stop_times (trip_id);

-- migrate
DROP INDEX IF EXISTS idx_stop_times_stop_id;

-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_arrival ON stop_times (stop_id, arrival_time);

-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_id_trip_id ON stop_times (stop_id, trip_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_calendar_dates_service_id ON calendar_dates (service_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_block_trip_index_service_ids ON block_trip_index (service_ids);

-- migrate
CREATE INDEX IF NOT EXISTS idx_block_trip_entry_index_id ON block_trip_entry (block_trip_index_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_block_trip_entry_trip_id ON block_trip_entry (trip_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_block_trip_entry_service_id ON block_trip_entry (service_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_block_id ON trips (block_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_shapes_shape_id ON shapes (shape_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_frequencies_trip_id ON frequencies (trip_id);

-- Problem reports for trips
-- migrate
CREATE TABLE
    IF NOT EXISTS problem_reports_trip (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        trip_id TEXT NOT NULL,
        service_date TEXT,
        vehicle_id TEXT,
        stop_id TEXT,
        code TEXT,
        user_comment TEXT,
        user_lat REAL CHECK (user_lat IS NULL OR user_lat BETWEEN -90 AND 90),
        user_lon REAL CHECK (user_lon IS NULL OR user_lon BETWEEN -180 AND 180),
        user_location_accuracy REAL,
        user_on_vehicle INTEGER CHECK (user_on_vehicle IS NULL OR user_on_vehicle IN (0, 1)),
        user_vehicle_number TEXT,
        created_at INTEGER NOT NULL,
        submitted_at INTEGER NOT NULL,
        CHECK (submitted_at >= created_at)
    ) STRICT;

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_trip_trip_service
    ON problem_reports_trip (trip_id, service_date);

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_trip_created
    ON problem_reports_trip (created_at);

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_trip_code
    ON problem_reports_trip (code);

-- Problem reports for stops
-- migrate
CREATE TABLE
    IF NOT EXISTS problem_reports_stop (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        stop_id TEXT NOT NULL,
        code TEXT,
        user_comment TEXT,
        user_lat REAL CHECK (user_lat IS NULL OR user_lat BETWEEN -90 AND 90),
        user_lon REAL CHECK (user_lon IS NULL OR user_lon BETWEEN -180 AND 180),
        user_location_accuracy REAL,
        created_at INTEGER NOT NULL,
        submitted_at INTEGER NOT NULL,
        CHECK (submitted_at >= created_at)
    ) STRICT;

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_stop_stop
    ON problem_reports_stop (stop_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_stop_created
    ON problem_reports_stop (created_at);

-- migrate
CREATE INDEX IF NOT EXISTS idx_problem_reports_stop_code
    ON problem_reports_stop (code);

-- Missing indexes to prevent full table scans on gtfs reads

-- migrate
DROP INDEX IF EXISTS idx_trips_route_id;

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_route_service ON trips (route_id, service_id);

-- migrate
DROP INDEX IF EXISTS idx_stop_times_stop_arrival;

-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_arrival ON stop_times (stop_id, arrival_time, departure_time);

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_route_headsign ON trips (route_id, trip_headsign, shape_id);

-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_time_window ON trips (max_departure_time, min_arrival_time);
