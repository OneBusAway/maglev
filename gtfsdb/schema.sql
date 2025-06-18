PRAGMA
foreign_keys = ON;

-- migrate

CREATE TABLE IF NOT EXISTS agencies
(
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    url      TEXT NOT NULL,
    timezone TEXT NOT NULL,
    lang     TEXT,
    phone    TEXT,
    fare_url TEXT,
    email    TEXT
);

-- migrate

CREATE TABLE IF NOT EXISTS routes
(
    id                  TEXT PRIMARY KEY,
    agency_id           TEXT    NOT NULL,
    short_name          TEXT,
    long_name           TEXT,
    desc                TEXT,
    type                INTEGER NOT NULL,
    url                 TEXT,
    color               TEXT,
    text_color          TEXT,
    continuous_pickup   INTEGER,
    continuous_drop_off INTEGER,
    FOREIGN KEY (agency_id) REFERENCES agencies (id)
);

-- migrate

CREATE TABLE IF NOT EXISTS stops
(
    id                  TEXT PRIMARY KEY,
    code                TEXT,
    name                TEXT,
    desc                TEXT,
    lat                 REAL NOT NULL,
    lon                 REAL NOT NULL,
    zone_id             TEXT,
    url                 TEXT,
    location_type       INTEGER DEFAULT 0,
    timezone            TEXT,
    wheelchair_boarding INTEGER DEFAULT 0,
    platform_code       TEXT
);

-- migrate

CREATE
VIRTUAL TABLE IF NOT EXISTS stops_rtree USING rtree(
        id,              -- Integer primary key for the R*Tree
        min_lat, max_lat, -- Latitude bounds
        min_lon, max_lon  -- Longitude bounds
    )
/* stops_rtree(id,min_lat,max_lat,min_lon,max_lon) */;

-- migrate

CREATE TABLE IF NOT EXISTS "stops_rtree_rowid"
(
    rowid
    INTEGER
    PRIMARY
    KEY,
    nodeno
);

-- migrate

CREATE TABLE IF NOT EXISTS "stops_rtree_node"
(
    nodeno
    INTEGER
    PRIMARY
    KEY,
    data
);

-- migrate

CREATE TABLE IF NOT EXISTS "stops_rtree_parent"
(
    nodeno
    INTEGER
    PRIMARY
    KEY,
    parentnode
);

-- migrate

CREATE TRIGGER IF NOT EXISTS stops_rtree_insert_trigger
    AFTER INSERT
    ON stops
BEGIN
    INSERT INTO stops_rtree(id, min_lat, max_lat, min_lon, max_lon)
    VALUES (new.rowid, new.lat, new.lat, new.lon, new.lon);
END;

-- migrate

CREATE TRIGGER IF NOT EXISTS stops_rtree_update_trigger
    AFTER UPDATE
    ON stops
BEGIN
    UPDATE stops_rtree
    SET min_lat = new.lat,
        max_lat = new.lat,
        min_lon = new.lon,
        max_lon = new.lon
    WHERE id = old.rowid;
END;

-- migrate

CREATE TRIGGER IF NOT EXISTS stops_rtree_delete_trigger
    AFTER DELETE
    ON stops
BEGIN
    DELETE FROM stops_rtree WHERE id = old.rowid;
END;

-- migrate

CREATE TABLE IF NOT EXISTS calendar
(
    id         TEXT PRIMARY KEY,
    monday     INTEGER NOT NULL,
    tuesday    INTEGER NOT NULL,
    wednesday  INTEGER NOT NULL,
    thursday   INTEGER NOT NULL,
    friday     INTEGER NOT NULL,
    saturday   INTEGER NOT NULL,
    sunday     INTEGER NOT NULL,
    start_date TEXT    NOT NULL,
    end_date   TEXT    NOT NULL
);

-- migrate

CREATE TABLE IF NOT EXISTS trips
(
    id                    TEXT PRIMARY KEY,
    route_id              TEXT NOT NULL,
    service_id            TEXT NOT NULL,
    trip_headsign         TEXT,
    trip_short_name       TEXT,
    direction_id          INTEGER,
    block_id              TEXT,
    shape_id              TEXT,
    wheelchair_accessible INTEGER DEFAULT 0,
    bikes_allowed         INTEGER DEFAULT 0,
    FOREIGN KEY (route_id) REFERENCES routes (id),
    FOREIGN KEY (service_id) REFERENCES calendar (id)
);

-- migrate

CREATE TABLE IF NOT EXISTS shapes
(
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    shape_id          TEXT    NOT NULL,
    lat               REAL    NOT NULL,
    lon               REAL    NOT NULL,
    shape_pt_sequence INTEGER NOT NULL
);

-- migrate

CREATE TABLE IF NOT EXISTS stop_times
(
    trip_id             TEXT    NOT NULL,
    arrival_time        INTEGER NOT NULL,
    departure_time      INTEGER NOT NULL,
    stop_id             TEXT    NOT NULL,
    stop_sequence       INTEGER NOT NULL,
    stop_headsign       TEXT,
    pickup_type         INTEGER DEFAULT 0,
    drop_off_type       INTEGER DEFAULT 0,
    shape_dist_traveled REAL,
    timepoint           INTEGER DEFAULT 1,
    FOREIGN KEY (trip_id) REFERENCES trips (id),
    FOREIGN KEY (stop_id) REFERENCES stops (id),
    PRIMARY KEY (trip_id, stop_sequence)
);


-- migrate
CREATE TABLE IF NOT EXISTS calendar_dates
(
    service_id     TEXT    NOT NULL,
    date           TEXT    NOT NULL,
    exception_type INTEGER NOT NULL,
    PRIMARY KEY (service_id, date)
);
-- migrate

CREATE INDEX IF NOT EXISTS idx_routes_agency_id ON routes(agency_id);
-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_route_id ON trips(route_id);
-- migrate
CREATE INDEX IF NOT EXISTS idx_trips_service_id ON trips(service_id);
-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_trip_id ON stop_times(trip_id);
-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_id ON stop_times(stop_id);
-- migrate
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_id_trip_id ON stop_times(stop_id, trip_id);

-- migrate

CREATE TABLE IF NOT EXISTS import_metadata
(
    id           INTEGER PRIMARY KEY CHECK (id = 1), -- Only allow one row
    file_hash    TEXT NOT NULL,
    import_time  INTEGER NOT NULL,
    file_source  TEXT NOT NULL
);