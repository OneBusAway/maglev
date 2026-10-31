# Multi-Feed Static GTFS Loading and Stop Consolidation

Status: draft for review, 2026-10-24.

This document replaces the 2026-08 version. The old version described a
merge of feeds in memory, a collision policy, and a "v2 proposal". This
version replaces all three with one design. It also replaces the pull
request stack #1791 to #1810. The rules from that stack are now part of §4.

This document is mirrored to the wiki page
`Multi-Agency-Static-Feeds-Stop-Consolidation` (see `docs/AGENTS.md`). Edit
this file first. Then copy it to the wiki.

---

## 1. Goal

Maglev loads one static GTFS zip file. Java OneBusAway loads many zip
files, one for each source agency. Java names each entity after the zip
file that it came from. Maglev must do the same. The result is:

- A deployment such as Puget Sound can be served from the agencies' own
  feeds. No upstream merge step is necessary. Puget Sound has King County
  Metro, Sound Transit, Community Transit, Pierce Transit, Kitsap Transit,
  Everett Transit, Intercity Transit and more.
- Each stop, route, trip, service, shape and block has one public ID. The
  ID is the same ID that Java gives it. Clients that were built for Java
  servers continue to work with Maglev.
- Stop consolidation can merge stops from different feeds. Stop
  consolidation shows one map pin for one curb that two agencies serve.

The measure of success is Java parity. Give Maglev the same zip files, the
same `defaultAgencyId` for each zip file, and the same consolidation file.
Maglev must then return the same IDs as application-modules 2.7.1. Where
this document departs from Java on purpose, §12 lists the difference and the
reason.

### 1.1 Why the stop-ID work stopped

Pull requests #1791 to #1810 gave Maglev the Java rule for stop names in a
single feed: each stop belongs to the first agency in `agency.txt`. That
rule is a special case of the full rule: each stop belongs to the default
agency of its feed. When Maglev applies the special case to one merged zip
file, it shows the known Java problem: all stops of a region belong to one
agency. We do not land the special case and then change it. This document
defines the full rule. The stop-ID rules are §4.

---

## 2. What Java does

Sources: `onebusaway-transit-data-federation-builder` (`FB:`) and
`onebusaway-transit-data-federation` (`TDF:`) in application-modules 2.7.1,
and `onebusaway-gtfs` 1.4.17 (`GM:`), the GTFS reader that application-modules
uses. The document `onebusaway-application-modules/docs/oba-multi-agency-bundle-spec.md`
describes the same feature for the bundle operator.

### 2.1 Bundles

A bundle is a list of `GtfsBundle` objects (`FB:bundle/model/GtfsBundle.java`).
Each object has these fields:

| Field | Meaning |
|---|---|
| `path` | The zip file or directory. |
| `defaultAgencyId` | The agency for entities that have no agency of their own. When not set, the first agency that the reader has seen. |
| `agencyIdMappings` | Rewrites of `agency_id` from an old value to a new value. Java applies them to `agency.txt` and `routes.txt` only. |

Java creates one `GtfsReader` for each bundle, with `overwriteDuplicates=true`.
`GtfsMultiReaderImpl.run()` reads one entity class at a time. For each
class, it reads each feed in order into one store
(`FB:bundle/tasks/GtfsMultiReaderImpl.java:115-135`).

### 2.2 How each ID gets its agency

Each ID in the Java graph is an `AgencyAndId`. Java writes it as
`{agencyId}_{rawId}`. Java parses it at the **first** underscore.

| Entity | Agency part of the ID | Source |
|---|---|---|
| Agency | `agency_id`, after `agencyIdMappings`. When the column is empty: `defaultAgencyId` if set, else `agency_name`. | `GM:serialization/GtfsReader.java:311-323` |
| Stop | The default agency of the feed. | `GM:model/Stop.java:42` |
| Stop `parent_station` | The agency of the child stop. | `FB:bundle/tasks/transit_graph/StopEntriesFactory.java:72-74` |
| Route | The agency of the route (`agency_id` after mappings). When the column is absent and the reader knows one agency, that agency. | `GM:serialization/mappings/RouteAgencyFieldMappingFactory.java:66-87` |
| Trip | **The agency of the route.** | `GM:serialization/mappings/TripAgencyIdFieldMappingFactory.java:22` |
| Service (`calendar`, `calendar_dates`, `trips.service_id`) | The default agency of the feed. | `GM:model/ServiceCalendar.java:39`, `Trip.java:35-36` |
| Shape (`shapes.txt`, `trips.shape_id`) | The default agency of the feed. | `GM:model/ShapePoint.java:34`, `Trip.java:53-54` |
| Fare attribute | The default agency of the feed. | `GM:model/FareAttribute.java:29` |
| Block | `AgencyAndId(trip agency, block_id)`. A trip with no `block_id` is its own block. The block ID is the trip ID. | `FB:bundle/tasks/transit_graph/BlockEntriesFactory.java:90-97` |
| Stop time `stop_id` | Java looks up `(default agency, raw)`. The result is null when not found. | `GM:serialization/mappings/StopLocationFieldMappingImpl.java:50-62` |

The default agency is `_defaultAgencyId` when set. Otherwise it is the first
agency in the list of the reader (`GM:serialization/GtfsReader.java:150-156`).
The multi-reader gives each reader all agencies that it has read so far.
When a second bundle has no `defaultAgencyId`, it gets the first agency of
the first bundle. Production configurations set `defaultAgencyId` for each
bundle.

### 2.3 Collisions and duplicates

- Two feeds can make the same `AgencyAndId`. This is possible only when
  they have the same default agency or the same route agency. For stops,
  routes and trips, the entity from the later feed replaces the entity from
  the earlier feed. Java writes a warning only
  (`GM:impl/GenericDaoImpl.java:100-109`). A duplicate `calendar.txt`
  service ID stops the build (`MultipleCalendarsForServiceIdException`).
- Two feeds can have the same `agency_id`. Java drops the second agency
  row. The first row wins (`GtfsReader.java:318-323`). The routes of the
  second feed then use the agency object of the first feed, with its name
  and time zone. `agencyIdMappings` is the solution.
- Two feeds can use the same raw stop ID under different default agencies.
  These are different entities. This is the normal case. No configuration
  is necessary.

### 2.4 Realtime

A `GtfsRealtimeSource` has `agencyIds`
(`TDF:impl/realtime/gtfs_realtime/GtfsRealtimeSource.java:404-409`). When
not set, it is each agency in the bundle. `GtfsRealtimeEntitySource`
resolves a raw ID in this order (`:197-214`, `:115-135`, `:220-269`):

1. For each agency in `agencyIds`, in order, try `AgencyAndId(agency, raw)`.
2. Parse the raw value as a combined `agency_id`.
3. For trips only, use fuzzy matching when it is enabled.

Java parses a vehicle ID as a combined ID when possible. Otherwise the
vehicle gets the agency of the matched block. Alerts and unmatched entities
get `agencyIds[0]`.

### 2.5 Coverage and time zones

- `agencies-with-coverage` computes the bounds of an agency from the stops
  that the trips of that agency visit (`TDF:impl/AgencyServiceImpl.java:111-134`).
  The agency in the stop ID is not used. A stop with no trips is not
  counted. An agency with no service is listed with empty bounds.
- Trip time math uses the time zone of the **route agency**
  (`FB:bundle/tasks/transit_graph/TripEntriesFactory.java:222-224`).
  Service dates use the time zone of the agency in the service ID. That is
  the default agency of the feed
  (`GM:impl/calendar/CalendarServiceDataFactoryImpl.java:129-161`).

### 2.6 Stop consolidation

`EntityReplacementStrategyFactory` (`FB:bundle/tasks/EntityReplacementStrategyFactory.java:84-101`)
reads a text file. Each line is `keptId absorbedId…`. Each ID is in
`agency_id` form. Lines that start with `#`, `{{{` or `}}}` are skipped. The
strategy works inside the store of the multi-reader
(`GtfsMultiReaderImpl.StoreImpl`), so it works across bundles. An absorbed
stop goes to a rejection store. A lookup of its ID (from `stop_times` or
`parent_station`) returns the kept stop. At runtime,
`ConsolidatedStopsServiceImpl` reads the same file. It translates absorbed
IDs that arrive in GTFS-RT and on the `stop` endpoint.

---

## 3. Maglev today

Checked against `main` at 2401ee38 and against the streaming-import stack
(#1662 to #1677, tip `ab/1659-connection-pragmas`).

- **One static source.** `gtfs-static-feed` is one object
  (`internal/appconf/json_config.go:13-18,43`). `ReloadStatic`
  (`internal/gtfs/static.go:196-249`) downloads one zip file and parses it.
  `StoreGtfsData` (`gtfsdb/helpers.go:205-571`) replaces each table in one
  transaction. One `import_metadata` row holds the hash of the zip file. An
  unchanged hash skips the import.
- **Raw IDs in the database.** Each ID column holds the GTFS value without
  a prefix. Each primary key is on that raw value (`gtfsdb/schema.sql`).
  The `{agency}_{id}` form exists only in the API layer
  (`internal/utils/api.go:83-123`). A handler splits the request ID,
  queries with the raw part, and adds a prefix to the result. The handler
  code has about 130 `FormCombinedID` calls and 37
  `ExtractCodeID`/`ExtractAgencyIDAndCodeID` calls.
- **Stops have no agency.** `stop_agencies` is a derived table. It has one
  row for each (stop, agency) pair where routes of the agency serve the
  stop. The agency in the public ID of a stop depends on the endpoint. The
  stopped stack #1791 to #1810 changed the table to one row for each stop,
  under the first agency of the feed.
- **Realtime matches raw IDs.** `rebuildMergedRealtimeLocked`
  (`internal/gtfs/realtime.go:1015-1149`) keys trips, vehicles, routes and
  alert targets by the raw ID in the feed. The `gtfs-rt-feeds[].agency-ids`
  list filters entities by the agency of their route and attributes
  vehicles.
- **The streaming stack changes the shape of the import.** On that branch,
  Maglev writes the feed to a temporary file and computes the hash during
  the download. It checks the hash before it parses. `go-gtfs` v1.4.0
  `ParseStaticStream` returns stop times and shapes as iterators. Rows are
  inserted in batches (`internal/gtfs/feed_file.go`,
  `gtfsdb/stream_import.go`, `gtfsdb/batch_insert.go`). The old design in
  this document joined `gtfs.Static` structs in memory. That design no
  longer fits: stop times and shapes never exist as slices. **This document
  is written for the streaming stack. The streaming stack must land first.**

---

## 4. Identity model

### 4.1 One rule: store the public ID

Maglev adopts the Java identity model. Each stop, route, trip, service,
shape and block is an (agency, raw ID) pair. The database stores the pair
as `{agencyId}_{rawId}`, the form that the API already serves. The import
rewrites IDs when it inserts rows. Handlers read IDs as stored. They do not
split or re-prefix IDs. Realtime ingest resolves raw IDs into stored IDs
once, at the feed boundary.

| Entity | Stored ID | Agency part |
|---|---|---|
| `agencies.id` | `agency_id` after `agency-id-mappings`. When empty: `default-agency-id` if set, else `agency_name`. | None. Agencies have no prefix. |
| `stops.id`, `stops.parent_station` | `{default}_{stop_id}` | The default agency of the feed. Also stored in `stops.agency_id`. |
| `routes.id` | `{route agency}_{route_id}` | `routes.agency_id` after mappings. When the column is absent and the feed has one agency, that agency. |
| `trips.id` | `{route agency}_{trip_id}` | The agency of the route of the trip. |
| `trips.service_id`, `calendar.id`, `calendar_dates.service_id` | `{default}_{service_id}` | The default agency of the feed. |
| `trips.shape_id`, `shapes.shape_id` | `{default}_{shape_id}` | The default agency of the feed. |
| `trips.block_id` | `{trip agency}_{block_id}`. Empty stays empty. | The agency of the trip. |
| `stop_times.trip_id`, `stop_times.stop_id`, `frequencies.trip_id` | The stored trip ID and stop ID. | Resolved in the same feed. |

A reference inside one feed resolves to the entity **of that feed**. A stop
time `stop_id` and a trip `route_id` are references. This is the Java
per-reader lookup. A reference to an ID that the feed does not define is a
validation error for that row. Maglev drops the row with a warning, as
today.

The separator rule is the Java rule: split at the **first** underscore. An
agency ID with `_` makes `A_B_C` ambiguous. A `default-agency-id` value and
an `agency_id` value (after mapping) must not contain `_`. The import rejects
a feed that breaks this rule (§5.4).

### 4.2 Default agency

The default agency of a feed is, in this order:

1. `default-agency-id` from the configuration, or `defaultAgencyId` from the
   bundle-inputs manifest (§5.2).
2. Otherwise, the first row of `agency.txt` in that feed.

The fallback never uses the agencies of another feed. This differs from
Java (§12). The default agency is normally one of the agencies of the feed.
When it is not, the import writes a warning. The time zone of that agency
is then not available for service-date math. Maglev uses the route agency
instead (§4.4).

### 4.3 What changes for a single-feed deployment

Public IDs do not change. A single-agency feed already serves its stops as
`{agency}_{stop}`. That agency is the first and only agency of the feed.
The database content changes. This causes one reimport on upgrade and a new
ETag. The behaviors below change. They match Java, and they were the subject
of #1791 to #1810:

- A stop has one public ID on each endpoint.
- A stop that no trip visits belongs to the default agency of the feed.
  `stop-ids-for-agency` lists it.
- A stop requested under another agency prefix returns 404. This is true
  also for an agency whose routes serve the stop.
- A trip ID has the agency of its route. A service ID has the default
  agency.
- For a merged multi-agency zip file, each stop and each service belongs to
  the first agency. `stop-ids-for-agency` is empty for the other agencies.
  Java does the same with the same input. This is the reason to load one
  feed for each agency.

### 4.4 Time zones

Each agency keeps its `agency_timezone`. Trip and stop-time math uses the
route agency of the trip, as today and as in Java. Service-date activation
uses the agency in the service ID, the default agency of the feed. When that
agency is not in the feed, Maglev uses the route agency and writes one
warning for each import. Java uses the JVM default time zone in that case.
The JVM default is wrong more often than the route agency.

---

## 5. Configuration

### 5.1 Feed list

`gtfs-static-feeds` is a list. The singular `gtfs-static-feed` continues to
work as a list with one element. Setting both keys is a validation error.
An empty list is a validation error. The CLI flag `-gtfs-url` stays
single-feed.

```json
{
  "gtfs-static-feeds": [
    { "id": "metro", "url": "https://…/metro.zip", "default-agency-id": "1" },
    { "id": "sound-transit", "url": "https://…/st.zip", "default-agency-id": "40",
      "agency-id-mappings": { "ST": "40" } },
    { "id": "community-transit", "url": "https://…/ct.zip", "default-agency-id": "29",
      "headers": { "Authorization": "Bearer …" } }
  ],
  "stop-consolidation-url": "https://…/stop-consolidation.txt"
}
```

Each feed has these keys:

| Key | Required | Meaning |
|---|---|---|
| `id` | No | A stable name for logs, metrics and `import_feeds`. Default: `feed-{n}`. |
| `url` | Yes | An `https://` URL or a local path. Same checks as today. |
| `default-agency-id` | Recommended | See §4.2. Must not contain `_`. Must be unique across feeds. |
| `agency-id-mappings` | No | Old value to new value, for `agency.txt` and `routes.txt` `agency_id`. Same as Java. |
| `auth-header-name`, `auth-header-value`, `headers` | No | Same as the single feed today. `headers` is new, for parity with `gtfs-rt-feeds`. |
| `enable-gtfs-tidy` | No | Same as today, for each feed. |

Feed order is load order. The order sets the order of `agencies` in list
responses. It also sets which feed a collision error names first.

### 5.2 Manifest mode

Production deployments get their inputs from obacloud. obacloud publishes a
`bundle-inputs.json` file (version 1). `gtfs-merge-service` writes the file.
obacloud promotes it and adds `stopConsolidationUrl`.

```json
{
  "version": 1,
  "feeds": [
    { "id": "metro", "name": "King County Metro", "defaultAgencyId": "1",
      "url": "https://…/metro.zip", "byteSize": 1234567, "sha256": "…" }
  ],
  "stopConsolidationUrl": "https://…/stop-consolidation.txt"
}
```

`gtfs-bundle-inputs-url` points Maglev at the manifest. Each `feeds` entry
maps to one `gtfs-static-feeds` item: `id`, `url` and `default-agency-id`.
`stopConsolidationUrl` maps to `stop-consolidation-url`. Maglev checks the
`sha256` value after the download. Setting `gtfs-bundle-inputs-url` together
with a static-feed key is a validation error. Maglev reads the manifest
again on each scheduled reload. A new published build is loaded without a
configuration change. The manifest does not carry `agency-id-mappings`,
headers or the tidy flag. These are not available in manifest mode. obacloud
renames agencies upstream with transform rules.

### 5.3 Realtime feeds

`gtfs-rt-feeds[].agency-ids` keeps its meaning. It also gets the Java
meaning: the ordered list of namespaces that Maglev tries when it resolves a
raw trip, route or stop ID (§8). The key stays optional. When it is not set,
Maglev tries each agency, in `agencies` order, as Java does. Some feeds
have stops under one agency and routes under another agency. Example: Sound
Transit stops are under `40`, and a route is under `ST` before mapping. Such
a feed lists both agencies.

### 5.4 Validation

At configuration time:

- The feed list is not empty. The `id` values are unique.
- The `default-agency-id` values are unique and contain no `_`.
- The `agency-id-mappings` target values contain no `_`.

At import time, each condition below stops the import. The error names the
feed and the bad value:

- An agency ID (after mapping) contains `_`.
- Two feeds produce the same agency ID. Java drops the second agency
  silently (§12).
- Two feeds produce the same stored entity ID. This is possible only when
  two feeds share a default agency or a route agency.

A `default-agency-id` in the manifest or configuration that is not in
`agency.txt` of the feed is a **warning**, not an error (§4.2).

---

## 6. Import pipeline

All steps run inside the `ReloadStatic` envelope and the
`storeConfiguredFeed` function of the streaming stack.

1. **Resolve the inputs.** Read the manifest when it is configured. Make the
   ordered feed list.
2. **Download and hash each feed** to a temporary file. The streaming stack
   does this for one feed. Check `sha256` against the manifest when the
   manifest is present. Download the consolidation file. A failed download
   stops the reload. The current data continues to serve. At startup, the
   process exits. Java §8.9 makes the same decision.
3. **Detect changes.** The new table `import_feeds` holds one row for each
   feed: `position`, `feed_id`, `source`, `hash`, `default_agency_id`. The
   dataset fingerprint is a sha256 over the ordered rows plus the hash of the
   consolidation file. Maglev stores the fingerprint in
   `import_metadata.file_hash`. That column is already the system ETag. When
   the fingerprint is unchanged, Maglev refreshes `feed_expires_at` only and
   skips the import. A change to any feed, to the order, to a default agency
   or to the consolidation file reimports everything. Incremental reimport
   of one feed is out of scope (§11).
4. **Parse and insert each feed, in one transaction.** Clear all tables
   once. For each feed: open its stream, resolve the default agency and the
   mappings, then insert agencies, routes, stops, calendar, trips, stop
   times, frequencies and shapes. Rewrite IDs in the row builders according
   to §4.1 (`stopTimeParams`, `shapePointParams` and the related functions
   on the streaming branch). Apply stop consolidation (§9) here, for each
   row.
5. **Check collisions.** A primary-key violation is caught and reported as
   a §5.4 error that names both feeds. An explicit check before insert
   covers agency IDs, so that the message is about agencies and not about a
   constraint.
6. **Build derived tables** once, after all feeds: block indexes, trip time
   bounds, R-tree and FTS (through triggers), direction precompute. Delete
   `stop_agencies`. `stops.agency_id` replaces it.
7. **Set the expiry.** `feed_expires_at` is the **earliest** expiry across
   feeds. The warning fires when the calendar of the first agency ends.
   `/metadata` and the health endpoint list the expiry of each feed.
8. **Publish** the region bounds, the default-agency map (`feed_id` to
   agency), the ETag and the statistics, as today.

With one feed and no consolidation file, this is the import of today with
IDs rewritten. The RABA parity golden files on the streaming branch
(`testdata/parity/raba/**`) are the regression baseline for that case. They
must be regenerated once, because each stored ID changes spelling.

---

## 7. API behavior

Tests must assert the rules below. They are the Java rules. They were
checked against production Puget Sound on 2026-10-24 and against OBA 2.7.1
in Docker on a synthetic two-agency feed (`testdata/java_parity`).

- **Lookup by ID is exact.** `stop/{id}`, `trip/{id}`, `route/{id}`,
  `block/{id}`, `shape/{id}` and the `-for-stop`, `-for-trip` and
  `-for-route` endpoints find the row whose stored ID equals the request
  ID. Otherwise they return 404. `stop/29_16140` returns 404 when the stop
  is `1_16140`, also when a Community Transit route serves it. Maglev
  returns 404 where Java returns 200 with a null body (§12).
- **Per-agency lists.** `stop-ids-for-agency/{a}` and `stops-for-agency/{a}`
  list the stops with `agency_id` equal to `a`, with or without service.
  `route-ids-for-agency` and `routes-for-agency` list the routes with
  `agency_id` equal to `a`.
- **References.** A stop appears once in `references.stops`, under its
  stored ID. `references.agencies` holds the agency of the stop and each
  agency whose routes are referenced. A route reference has the agency of
  the route.
- **Trip and service IDs.** A trip ID and a block ID have the agency of the
  route. `serviceId`, `serviceIds` and `activeServiceIds` have the default
  agency of the feed. A Sound Transit trip in the King County Metro feed has
  a trip ID `40_…` and a service ID `1_…`. Production shows this.
- **schedule-for-stop** names routes and trips with the agency of the route.
- **Nearby stops** (`arrivals-and-departures-for-stop`): each stop within
  100 m is listed and referenced. This includes stations and stops without
  service. This is true also when the time window has no arrivals.
- **agencies-with-coverage.** The bounds come from the stops that the trips
  of the agency visit (today's `GetStopBoundsPerAgency`). There is one entry
  for each agency, also for an agency without service.
- **Search.** `search-stop` and `search-route` return stored IDs. The sort
  keys use stored IDs.
- **Realtime.** `vehicles-for-agency/{a}` lists the vehicles whose resolved
  trip has a route of agency `a`. `trip-for-vehicle/{a}_{v}` resolves the
  vehicle ID in the namespace of `a`.

The stopped stack found and fixed three behaviors that are not ID rules.
They are Java parity items in the same area. They stay as follow-ups (§10,
phase F): the closest stop and next stop from the scheduled block position
with the Java dwell and tie rules, shapeless trips placed between their
stops, and raw-degree orientation.

---

## 8. Realtime

Realtime feeds carry raw IDs. Maglev resolves each entity once, at ingest,
in `updateFeedRealtime`, before the entity reaches the merged snapshot:

1. **Trips.** For each agency in `agency-ids` of the feed (or each agency),
   try the stored ID `{agency}_{raw}` against the `trips` table. Then accept
   the raw value when it is already a stored ID. An unresolved ADDED trip
   keeps its raw ID. Another unresolved trip is dropped and counted.
2. **Routes and stops.** Same order. This includes the informed entities of
   alerts.
3. **Vehicles.** The public vehicle ID is `{agency}_{raw}`. The agency is
   the agency of the resolved trip. Otherwise it is `agency-ids[0]`.
   Otherwise it is the first agency. A raw vehicle ID that already parses as
   a combined ID of a stored agency is kept.
4. **Consolidated stops.** An absorbed stop ID in a stop time update or an
   alert is translated to its kept stop (§9). `ConsolidatedStopsServiceImpl`
   does the same.

The maps in the merged snapshot (`tripLookup`, `vehicleLookupByVehicle`,
`vehiclesByRoute`, the alert index) are then keyed by stored IDs. Two feeds
can no longer overwrite each other's entries by accident. The `agency-ids`
filter keeps its filter role. Maglev applies it after resolution.

---

## 9. Stop consolidation

The semantics are the Java semantics. The decisions below were made on
2026-10-23.

### 9.1 File

The format and the parser are the same as Java
(`EntityReplacementStrategyFactory`). Each line is one rule:
`kept absorbed…`. Each token is a stored stop ID. Lines that start with
`#`, `{{{` or `}}}` are skipped. A line with a quote character splits at the
quotes. The only fatal parse error is a token without `_`. The parser and
the validator from #1771 and #1772 are reused without change.

| Condition | Behavior |
|---|---|
| Two rules claim one absorbed ID | The last rule wins. Warning. The real Puget Sound file has one such case. |
| Duplicate line | Ignored. Warning. |
| The kept stop is in no feed | The absorbed stops are dropped. Warning. Java drops them too: the stop of the stop time resolves to nothing. |
| An absorbed stop is in no feed | No effect. Warning. |
| Chain (`A B`, `B C`) or self-absorption | Same as Java. Warning. |

### 9.2 Application

Maglev applies the rules at insert time, for each row. A stop whose stored
ID is absorbed is not inserted. A stop time, a parent station or a transfer
that names an absorbed ID gets the kept ID. A trip with fewer than two stop
times after the rewrite is dropped. `TripEntriesFactory` does the same
(#1777, #1808).

### 9.3 Runtime

The `stop/{id}` endpoint resolves an absorbed ID to the kept stop. In
production, `stop/29_40` returns `1_16140`. Other endpoints return 404 for
the absorbed ID. Realtime translates absorbed stop IDs (§8). The loaded file
is part of the dataset fingerprint (§6).

---

## 10. Implementation plan

Prerequisite: the streaming-import stack #1662 to #1677 lands first. Each
phase below is a stack of pull requests. Each pull request is under about
200 lines and has a test that fails on its base. CONTRIBUTING.md applies.
Phases B and C are most of the work. They deliver Java parity.

### Phase A: configuration (no behavior change)

1. Add the `gtfs-static-feeds` list with `id`, `default-agency-id`,
   `agency-id-mappings` and `headers`. Normalize the singular key. Validate
   according to the configuration-time rules of §5.4. Update
   `config.schema.json` and the example files.
2. Add `gtfs-bundle-inputs-url`. Download the manifest and translate it into
   the feed list. Record `sha256` for the check.
3. Document `gtfs-rt-feeds[].agency-ids` as the namespace list. No code
   change yet.

The importer still reads `feeds[0]`.

### Phase B: namespaced storage for one feed

The handler layer moves to "IDs as stored" while there is one namespace.
Each pull request can be checked against the responses of today. Public IDs
do not change for a single-agency feed.

1. Schema: add `stops.agency_id` (NOT NULL, foreign key) and `import_feeds`.
   Drop `stop_agencies` and its backfill. Rewrite IDs in the stream row
   builders according to §4.1. Compute the dataset fingerprint according to
   §6 step 3. Regenerate the RABA parity golden files.
2. Make the shared stop builders (`stopModelsByIDs` and related functions)
   read stored IDs. Remove the agency parameter. This is the equivalent of
   #1793, #1797, #1798, #1801 and #1809 in the stopped stack. It is now
   mostly deletions.
3. Migrate the handler families, one pull request each. Delete the
   `ExtractCodeID` and `FormCombinedID` code. Query with the request ID as
   it is. The families: stop and stop lists; routes and stops-for-route;
   trips, trip-details, trip-for-vehicle and block; schedule-for-stop and
   schedule-for-route; arrivals (both endpoints and
   arrivals-for-location); stops-for-location, trips-for-location and
   trips-for-route; search; vehicles-for-agency and vehicle positions;
   the report-problem endpoints.
4. Resolve realtime IDs at ingest (§8), with one namespace. Extend the
   mock-server realtime tests.
5. Add the Java parity CI test from #1805, with its recorded data, for the
   single-feed case. `knownJavaDifferences` lists what remains.

### Phase C: multi-feed import

1. Make `storeConfiguredFeed` loop over the feeds in one transaction. Apply
   the default agency and the mappings for each feed. Name an agency without
   `agency_id` after `agency_name`.
2. Add the import-time validation of §5.4 with errors that name the feed.
   Add collision tests for each entity type.
3. Add expiry and metadata for each feed. Compute the ETag from the
   fingerprint. List the feeds in `/metadata` and in the health endpoint.
4. Use realtime `agency-ids` as the namespace list. Try the agencies in
   order. Default to all agencies.
5. Add a multi-feed Java parity fixture: two synthetic zip files, loaded as
   two `GtfsBundle` objects in the `oba-api` Docker stack with gtfstidy off.
   Record the IDs with `capture.py`. Replay them in CI.
6. Check Puget Sound: load the obacloud bundle-inputs manifest for the
   region. Compare stop, route, trip and service IDs with
   `api.pugetsound.onebusaway.org` on the three stop classes from #1791
   (Streetcar-only, shared, Sound Transit-only).

### Phase D: stop consolidation

1. Land the parser and the validator (#1771, #1772) as they are.
2. Apply the rules at insert time (§9.2). Add the `stop-consolidation-url`
   key and the manifest `stopConsolidationUrl` plumbing. Add the file to the
   fingerprint. This reworks #1773, #1775 and #1776.
3. Drop short trips (#1777, #1808).
4. Add the `stop/{id}` alias (#1778) and the realtime translation (#1779).
5. Check production: `stop/29_40` returns `1_16140`, and the absorbed-stop
   probes in the stop-consolidation plan give the same results.

### Phase E: documentation

Update the wiki endpoint pages whose Implementation Decisions describe
per-response agency derivation: `stops-for-location`, `search-stop`,
`trips-for-location`, `schedule-for-route` and `stop-ids-for-agency`.
Remove the rows of §13 as they are resolved.

### Phase F: remaining parity items from the stopped stack

These do not depend on the identity model. Open each one as its own pull
request against `main`: the closest stop and next stop from the block
snapshot (#1806), shapeless trip positions (#1807), and the Java
orientation formula (#1810).

### Open pull requests

- #1791 to #1810: replaced by phases B and C. Close them when phase B.1
  opens, with a link to this document. Reuse the Java-recorded data in
  `testdata/java_parity` and the tests on those branches.
- #1771, #1772, #1774: keep open. Land them in phase D.1.
- #1773, #1775 to #1779, #1808: drafts on the streaming stack. Rework them
  in phase D on the new identity model. Do not rebase them.

---

## 11. Out of scope

- **Incremental reimport of one feed.** A change rebuilds the whole dataset.
  A Java bundle rebuild does the same. Revisit when reload time becomes a
  problem.
- **A refresh interval for each feed.** One schedule covers all feeds. The
  dataset is always consistent.
- **Route collections grouped by short name** (`groupRoutesByShortName`).
  This is off by default in Java.
- **Fares, pathways, transfers and levels.** They are not stored, as today.
- **Fuzzy realtime trip matching.**
- **Candidate detection for consolidation and the audit CSV.** obacloud
  owns both.

---

## 12. Differences from Java, on purpose

| Java | Maglev | Reason |
|---|---|---|
| A second feed with no `defaultAgencyId` gets the agency of the first feed. | The feed uses its own first agency. | The Java value depends on load order by accident. Manifest mode always sets the value. |
| A duplicate `agency_id` across feeds: the second is dropped silently. | The import stops. The error names both feeds. | Silent loss of the name and time zone of an agency is the worse failure. |
| The same `AgencyAndId` from two feeds: the later one replaces the earlier one. | The import stops. | Same reason. The condition means that two feeds claim one agency. |
| An unknown ID returns HTTP 200 with `null`. | HTTP 404. | This is the existing Maglev convention. |
| Service-date time zone falls back to the JVM default when the default agency is unknown. | Falls back to the route agency, with a warning. | The JVM default is never correct. |
| `trips-for-location` ignores `time` (application-modules#486). | `time` is honored. | Java bug. |

---

## 13. Open deviations from legacy

Each row is a Maglev behavior that differs from Java because of the
identity model. The last column names the phase that resolves it. Remove
rows as phases land.

| Behavior | Legacy | Maglev today | Resolved by |
|---|---|---|---|
| Stop prefix on location endpoints | Fixed at build time. | The first row of `GetAgenciesForStops`, unordered. | B.3 |
| Stop references in `trips-for-location` | One ID for each stop. | The ID of the entry that named the stop first. | B.2 |
| `stops-for-location` stop-code search, stop without service | Returned. | Dropped. No agency can be resolved. | B.1 |
| `search-stop`, sort key of a stop without service | The combined ID. | None. The stop sorts last. | B.1 |
| Time-context agency for location queries | The agency of each entity. | `agencies[0]`. | B.3 |
| Merged multi-agency zip file: stops for each agency | All under the first agency. | One for each serving agency. | B.1 gives the Java behavior. C fixes it. |
