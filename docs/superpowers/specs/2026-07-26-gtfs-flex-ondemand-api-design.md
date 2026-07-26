# GTFS-Flex Support & the `/api/ondemand` Namespace

**Date:** 2026-07-26
**Status:** Approved design, pre-implementation
**Branch:** `flex-spec`

## Summary

Maglev will ingest GTFS-Flex data (merged into the GTFS Schedule spec in March 2024)
and expose demand-responsive transit (DRT) through a new, separate API namespace:
`/api/ondemand`. The namespace speaks the existing OneBusAway wire dialect — same
response envelope, same `references` system, same combined-ID convention, same API-key
and rate-limit middleware — but models on-demand mobility as its own product, distinct
from fixed-route service. The core abstraction, the **on-demand service**, is
provider-agnostic: GTFS-Flex populates it today, and GOFS (General On-demand Feed
Specification) can populate the identical model and endpoints later with zero contract
changes.

## Goals

1. Ingest all four GTFS-Flex files (`locations.geojson`, `location_groups.txt`,
   `location_group_stops.txt`, `booking_rules.txt`) plus the new `stop_times.txt` and
   `trips.txt` fields.
2. Serve three rider-facing jobs in the OBA iOS/Android apps:
   - **Zone discovery near me** — "what on-demand services cover this point/viewport,
     when do they run, how do I book?"
   - **Flex awareness on existing entities** — deviated-route trips and
     location-group stops surface their on-demand overlays.
   - **Agency/route browsing** — an agency's dial-a-ride offerings are enumerable.
3. Design the public surface so GOFS is a natural, non-conflicting extension.

## Non-Goals

- **Booking transactions.** Read-only discovery only. Booking happens out-of-band via
  the phone number or URL in the booking rule. No POST endpoints, no provider
  integrations, no state.
- **Trip planning.** Maglev has no routing engine; nothing here computes DRT
  itineraries or travel times.
- **GTFS-RT for flex trips.** Out of scope for v1.
- **GOFS ingestion.** Designed for, not built.

## Decisions of Record

| Decision | Choice |
|---|---|
| Scope | Read-only discovery |
| OpenAPI/sdk-config | Implement first, propose spec upstream after the design proves out |
| Compatibility | Strictly additive: existing response shapes never change meaning; flex-only entities are excluded where they would be misleading |
| Namespace | Separate `/api/ondemand` family, OBA wire conventions throughout |
| `/where` coupling | Additive pointer fields (`onDemandServiceIds`) on stop and route entries; omitted when empty |
| Geometry encoding | Embedded GeoJSON (not encoded polylines) |
| Parsing | Extend `OneBusAway/go-gtfs` upstream |
| Test data | Real feed: City of Alexandria VA flex v2 (Trillium), plus minimal synthetic fixtures only for patterns Alexandria doesn't exercise |

### Why a separate namespace (and why it is not "two protocols")

URL namespace and wire format are independent axes. `/api/ondemand` reuses the OBA
envelope (`code`/`currentTime`/`text`/`version`/`data` with `entry`|`list` +
`references`), agency-prefixed IDs, `.json` + HTTP GET, and the existing key/rate-limit
middleware. Client networking and reference-resolution code is reused wholesale; the
only new client code is the new model types, which any approach would require.

DRT is a distinct product: pure-zone dial-a-ride and every GOFS service have no stops,
no schedule, and no arrivals. A separate namespace also keeps `/api/where` conformant
with the upstream sdk-config OpenAPI contract while `/api/ondemand` iterates freely —
the only `/where` divergence in this design is the pointer fields.

The GTFS-Flex spec itself sanctions the split for interwoven patterns: its routing
rule instructs consumers computing fixed-route timing to **ignore** windowed
stop_times records. `/api/where/trip-details` returning only the fixed records is
therefore the spec's own fixed-route view, not a lossy compromise; the deviation
overlay is a legitimately separate resource.

## 1. Ingestion & Storage

### 1.1 Parsing: extend `OneBusAway/go-gtfs`

The new flex columns live inside `stop_times.txt`, which go-gtfs already parses;
parsing stop_times a second time in Maglev would be wasteful and fragile. The work
lands in the org's own fork (closing the ❌ rows in its README) as a prerequisite PR:

- `stop_times.txt`: `location_id`, `location_group_id`, `start_pickup_drop_off_window`,
  `end_pickup_drop_off_window`, `pickup_booking_rule_id`, `drop_off_booking_rule_id`
- `trips.txt`: `safe_duration_factor`, `safe_duration_offset`
- New files: `locations.geojson` (GeoJSON FeatureCollection; Polygon/MultiPolygon
  only), `location_groups.txt`, `location_group_stops.txt`, `booking_rules.txt`

Maglev consumes the new structs through its existing `ParseStatic` → bulk-insert path
(`gtfsdb/helpers.go`).

Reviewing the go-gtfs source (`static.go`, `interpolate.go`) pins down what the
upstream change actually is — it is more than adding optional columns:

- **Flex rows are dropped entirely today.** `stop_id` is a required column and any
  row whose stop doesn't resolve is silently skipped (`parseScheduledStopTimes`:
  `if stopTime.Stop == nil { continue }`). The Alexandria feed currently imports as
  a route with two trips and zero stop times. `stop_id` must become optional at
  both column level (a pure zone-based feed may omit the column entirely) and row
  level, with rows kept when exactly one of stop/location/location-group resolves
  and skipped — via the existing `Static.Warnings` mechanism, not `log.Print` —
  when none does.
- **Struct surface:** `Static` gains `Locations`, `LocationGroups`, and
  `BookingRules` slices; `ScheduledStopTime` gains `Location`/`LocationGroup`
  pointers (alongside the existing `Stop`), the two window durations, and
  pickup/drop-off booking-rule references; `ScheduledTrip` gains the two
  safe-duration fields.
- **`locations.geojson` is not CSV.** `ParseStatic`'s dispatch is a table of
  CSV-file handlers; the GeoJSON file needs its own parse path outside that loop.
- **Time interpolation must exempt windowed records.** go-gtfs interpolates
  missing arrival/departure times across each trip (`interpolateStopTimes`,
  by-shape-dist variant included). On a deviated trip this would fabricate times
  for the zone records sitting between timed stops — records the GTFS spec forbids
  times on. Windowed records are excluded from interpolation.
- **Missing vs. midnight:** `ScheduledStopTime.ArrivalTime` is a non-pointer
  `time.Duration`, so a zero value is ambiguous between "absent" and "00:00:00".
  No breaking pointer change is needed: window presence is the discriminator
  (the spec forbids times when windows are set), and Maglev's importer writes NULL
  arrival/departure exactly when a record carries windows.

Real-world feeds still carry draft-era GTFS-Flex columns. The Alexandria test feed
puts `mean_duration_factor`/`mean_duration_offset` (dropped from the adopted spec)
and `safe_duration_factor`/`safe_duration_offset` (adopted, but on `trips.txt`) in
`stop_times.txt`. The parser must tolerate the unknown draft columns, and the
importer reads `safe_duration_*` from `trips.txt` first, falling back to the
draft-era `stop_times.txt` placement when the trips columns are absent.

### 1.2 Schema (sqlc; `gtfsdb/schema.sql`)

Four new tables:

```sql
locations (
    id            TEXT PRIMARY KEY,   -- shared namespace with stops.stop_id, location_groups.id
    name          TEXT,               -- properties.stop_name
    description   TEXT,               -- properties.stop_desc
    geometry      TEXT NOT NULL,      -- GeoJSON geometry object, verbatim
    min_lat REAL NOT NULL, max_lat REAL NOT NULL,
    min_lon REAL NOT NULL, max_lon REAL NOT NULL   -- computed bounding box
)

location_groups (
    id    TEXT PRIMARY KEY,
    name  TEXT
)

location_group_stops (
    location_group_id TEXT NOT NULL REFERENCES location_groups(id),
    stop_id           TEXT NOT NULL REFERENCES stops(id),
    PRIMARY KEY (location_group_id, stop_id)
)

booking_rules (
    id                        TEXT PRIMARY KEY,
    booking_type              INTEGER NOT NULL CHECK (booking_type BETWEEN 0 AND 2),
    prior_notice_duration_min INTEGER,          -- minutes
    prior_notice_duration_max INTEGER,          -- minutes
    prior_notice_last_day     INTEGER,
    prior_notice_last_time    INTEGER,          -- ns since midnight, matching stop_times convention
    prior_notice_start_day    INTEGER,
    prior_notice_start_time   INTEGER,
    prior_notice_service_id   TEXT,
    message                   TEXT,
    pickup_message            TEXT,
    drop_off_message          TEXT,
    phone_number              TEXT,
    info_url                  TEXT,
    booking_url               TEXT
)
```

Additive columns:

- `stop_times`: the six flex columns above (times as int64 ns-since-midnight,
  matching the existing convention). `stop_id` becomes nullable with a CHECK that
  exactly one of `stop_id` / `location_id` / `location_group_id` is set.
  **`arrival_time` and `departure_time` also become nullable** — the spec forbids
  them on windowed records, and every windowed row in a real feed has them empty —
  with the existing `arrival_time <= departure_time` CHECK applying only when both
  are present. Downstream effects to handle explicitly: the cached
  `trips.min_arrival_time`/`max_departure_time` columns stay NULL for flex-only
  trips (which correctly drops them from time-window queries like
  trips-for-location), and every query ordering or filtering on `arrival_time`
  must exclude NULL rows rather than sort phantom zeros.
- `trips`: `safe_duration_factor REAL`, `safe_duration_offset REAL`.

Plus the compiled-rule table produced at import (§2.3):

```sql
ondemand_rules (
    id                        INTEGER PRIMARY KEY,
    service_id_key            TEXT NOT NULL,     -- the owning on-demand service (route id for flex)
    trip_id                   TEXT NOT NULL,
    from_id                   TEXT NOT NULL,     -- shared namespace: stop | location | location group
    from_kind                 INTEGER NOT NULL,  -- 0 stop, 1 location, 2 location group
    to_id                     TEXT NOT NULL,
    to_kind                   INTEGER NOT NULL,
    start_pickup_time         INTEGER,           -- ns since midnight; may exceed 24h.
    end_pickup_time           INTEGER,           -- NULL window = available all service-day
    end_drop_off_time         INTEGER,           -- hours (the GOFS windowless-rule case)
    gtfs_service_id           TEXT NOT NULL,     -- calendar reference; one row per calendar
    pickup_type               INTEGER NOT NULL,
    drop_off_type             INTEGER NOT NULL,
    pickup_booking_rule_id    TEXT,
    drop_off_booking_rule_id  TEXT,
    safe_duration_factor      REAL,
    safe_duration_offset      REAL
)
```

**ID namespace.** The GTFS spec requires location IDs, location-group IDs, and stop
IDs to share one uniqueness namespace. The existing `{agencyId}_{id}` combined-ID
convention therefore extends without disambiguation: zone and group IDs are
agency-prefixed exactly like stop IDs.

### 1.3 Spatial strategy

No SpatiaLite, no new R-tree. Flex feeds contain dozens of zones, not thousands.
`services-for-location` filters candidates by the bounding-box columns in SQL, then
runs an exact test in Go against the stored GeoJSON:

- **Point mode**: ray-casting point-in-polygon, honoring holes (right-hand-rule
  interior rings) and MultiPolygon members.
- **Region mode**: polygon–rectangle intersection against the viewport.

Revisit with an R-tree only if profiling demands it.

### 1.4 Import invariants

Enforced at import, mirroring the spec's presence rules; violations are logged and the
offending row skipped — never a failed import (consistent with existing malformed-GTFS
handling):

1. Exactly one of `stop_id` / `location_id` / `location_group_id` per stop_time.
2. Windows required when a location or location group is referenced; both ends
   present together.
3. Windows and `arrival_time`/`departure_time` are mutually exclusive.
4. With windows: `pickup_type` ∈ {1, 2}; `drop_off_type` ∈ {1, 2, 3} (the spec's
   asymmetry: drop-off 3 legal, pickup 3 not).
5. Dangling booking-rule / location / group references: row skipped, logged.
6. Feeds violating the spec's same-trip overlap prohibition are imported anyway
   (log-only) — consumers, not validators.

## 2. The On-Demand Service Model

### 2.1 `onDemandService` (the entry)

The rider-facing unit: "a bookable service that covers these areas, during these
windows, booked this way." Provider-agnostic by construction. For flex data, one
service exists per route that has at least one flex stop_time record (a windowed
record, or any record referencing a location or location group) — even when rule
compilation yields nothing (§2.3).

```jsonc
{
  "id": "25_dial-a-ride",        // flex: the route's combined ID
  "agencyId": "25",
  "routeId": "25_dial-a-ride",   // null for future GOFS services — they have no routes
  "name": "Brown County Dial-a-Ride",
  "description": null,
  "url": null,
  "rules": [ /* availabilityRule, below */ ]
}
```

### 2.2 `availabilityRule`

Deliberately shaped like a GOFS `operating_rule` — origin areas, destination areas,
a daily time window, a calendar:

```jsonc
{
  "fromIds": ["25_area_708"],          // shared stop/location/locationGroup namespace
  "toIds": ["25_area_708"],
  "startPickupTime": "08:00:00",       // "HH:MM:SS", >24h legal (both specs' semantics)
  "endPickupTime": "17:00:00",
  "endDropOffTime": "17:30:00",        // null when it matches endPickupTime
  "calendarIds": ["25_weekday"],
  "pickupType": 2,                     // 2 = must book; 3 = coordinate with driver
  "dropOffType": 2,
  "pickupBookingRuleId": "25_booking_route_74362",
  "dropOffBookingRuleId": "25_booking_route_74362",
  "safeDurationFactor": null,          // optional; clients with routing engines can
  "safeDurationOffset": null           // compute the spec's 95th-percentile estimate
}
```

The window is split into pickup and drop-off ends because both the real world and
GOFS require it: the Alexandria feed permits pickups until 24:50 but drop-offs until
25:00, and GOFS models exactly this as `end_dropoff_window`. A single intersected
window would silently discard the drop-off tail. All three time fields null means
the service runs all hours of its service days (GOFS's windowless-rule semantics).
`calendarIds` is an array because GOFS operating rules carry calendar arrays; flex
compilation emits one element, and rules identical except for calendar merge their
`calendarIds`.

### 2.3 Rule compilation (import time)

Rules are compiled from flex trips using the spec's reachability model — travel is
possible from any pickup-capable record to any *later* drop-off-capable record on the
same trip:

1. For each trip with at least one flex record, order records by `stop_sequence`.
2. A record is **pickup-capable** when it permits boarding: `pickup_type` = 2 for
   windowed records (1 means no pickup; 0 and 3 are forbidden with windows), or
   `pickup_type` ∈ {0, 2, 3} for timed-stop records. Symmetrically, a record is
   **drop-off-capable** when `drop_off_type` ∈ {2, 3} for windowed records or
   ∈ {0, 2, 3} for timed-stop records. Pair each pickup-capable record with each
   subsequent drop-off-capable record. Pairs where both records are timed fixed
   stops are skipped — that is ordinary fixed-route travel, not an on-demand rule.
3. Each pair emits one rule: `fromIds` from the pickup record, `toIds` from the
   drop-off record. Windows: `startPickupTime`/`endPickupTime` come from the pickup
   record's window (for a timed fixed stop, a point window at its departure time);
   `endDropOffTime` comes from the drop-off record's window end (or arrival time
   for a timed stop), nulled when equal to `endPickupTime`. Calendar = the trip's
   `service_id`; booking-rule IDs and safe-duration values carried through.
4. Rules identical except for `trip_id` collapse to one row per distinct
   (from, to, windows, calendar, types, booking) tuple, and rules identical except
   for calendar merge their `calendarIds`; the stored `trip_id` is a representative
   retained for traceability only and is not exposed in the API.

Pattern projections:

| Flex pattern | Compiles to |
|---|---|
| Dial-a-ride, single zone (Heartland Express) | zone→same zone, one rule per trip/window variant |
| Zone-to-zone (Minnesota River Valley) | zone A→zone B |
| Location group (RufBus) | group→same group; members via references |
| Deviated fixed route (Hermann Express) | fixed stop ↔ deviation zone pairs per segment |

A degenerate feed (e.g. the published Hermann example, which technically permits no
pickups anywhere) compiles to a service with areas and booking info but few or no
rules — still renderable. The algorithm is all-pairs by construction (that is what
the GTFS reachability rule requires — a deviated route's zones pair with every later
capable record, not just adjacent ones), but counts stay small in practice because
flex trips have few records (pure-zone trips have two; deviated trips a couple dozen),
and compilation happens once at import, so queries stay cheap.

### 2.4 New reference types

Added to the `references` block of `/api/ondemand` responses alongside
`agencies`/`routes`/`stops`/etc. **Mechanism:** the `/ondemand` handlers use an
extended references model that embeds the existing `ReferencesModel` struct and adds
the four new sections; the `/where` struct is untouched, so no new (even empty)
JSON keys ever appear in `/where` responses — that would violate the byte-identical
guarantee, and §5's regression test asserts it.

**`serviceAreas`** — GeoJSON Features with an always-present bounding box:

```jsonc
{
  "id": "25_area_708",
  "name": "Brown County",
  "bbox": [-94.87, 44.10, -94.25, 44.53],   // [minLon, minLat, maxLon, maxLat]
  "geometry": { "type": "Polygon", "coordinates": [ /* … */ ] }   // presence per §3
}
```

Real zone geometries are large — Alexandria's single polygon is a 4,239-point ring,
~340 KB of JSON — so `geometry` inclusion is endpoint policy (§3): full geometry on
`service/{id}` by default; list endpoints send `bbox` only unless the caller passes
`includeGeometry=true`. Gzip (already in the middleware chain) does the rest.

**`locationGroups`**:

```jsonc
{ "id": "25_476_stops", "name": "RufBus 476 stops", "stopIds": ["25_4149546", "…"] }
```

**`bookingRules`** — the flex vocabulary camelCased verbatim; this field set is
already the shared Flex/GOFS dialect:

```jsonc
{
  "id": "25_booking_route_74362",
  "bookingType": 2,                    // 0 real-time | 1 same-day | 2 prior-day(s)
  "priorNoticeDurationMin": null,      // minutes; booking_type 1
  "priorNoticeDurationMax": null,
  "priorNoticeLastDay": 1,
  "priorNoticeLastTime": "15:00:00",
  "priorNoticeStartDay": 14,
  "priorNoticeStartTime": "00:00:00",
  "priorNoticeCalendarId": null,       // exposed under GOFS's field name; sourced from
                                       // GTFS prior_notice_service_id
  "message": null,
  "pickupMessage": null,
  "dropOffMessage": null,
  "phoneNumber": "+15073591717",
  "infoUrl": null,
  "bookingUrl": null
}
```

**`calendars`** — GOFS's shape, compiled from GTFS `calendar` + `calendar_dates`:

```jsonc
{
  "id": "25_weekday",
  "days": ["mon", "tue", "wed", "thu", "fri"],
  "startDate": "2026-01-05",
  "endDate": "2026-12-18",
  "exceptedDates": ["2026-09-07"]
}
```

Removed-service exceptions (`calendar_dates` type 2) become `exceptedDates`;
added-service exceptions (type 1) become standalone single-range calendars referenced
by additional rules — the GOFS idiom. The *structure* matches GOFS; the date *format*
deliberately does not (GOFS uses `YYYYMMDD`) — a future GOFS importer normalizes on
ingest rather than passing dates through.

**Time semantics, stated once for the whole API:** dates are `YYYY-MM-DD` strings and
times of day are `"HH:MM:SS"` strings that may exceed `24:00:00`, both interpreted as
service-day values in the timezone of the service's referenced agency
(`references.agencies[].timezone` — per GTFS this is `agency.agency_timezone`, never
`stops.stop_timezone`; the Alexandria feed, which declares a Los Angeles agency
timezone alongside a New York stop timezone, is imported exactly per that rule). For
future GOFS services the synthesized agency (§4) carries
`system_information.timezone`.

Stops referenced by rules or group memberships appear in `references.stops` in the
standard shape; routes and agencies likewise.

## 3. Endpoints & Contracts

All endpoints: HTTP GET, `.json`, API-key validation, rate limiting, gzip, standard
OBA envelope. Registered in `internal/restapi/routes.go` next to the `/where` routes.

| Endpoint | Contract |
|---|---|
| `/api/ondemand/services-for-location.json` | `lat` + `lon` (required floats) → services whose areas contain the point **or** whose rule-referenced stops fall within `radius` meters (optional; same default as `stops-for-location`) — without the radius, stop-based services (location groups, windowed stops) would be unreachable from a point query. Optional `latSpan` + `lonSpan` switch to viewport-intersection mode, mirroring `stops-for-location` conventions. List response. |
| `/api/ondemand/services-for-agency/{id}.json` | All on-demand services for the agency. List response. |
| `/api/ondemand/service/{id}.json` | Single service, full rules + references. Entry response. |

Geometry policy (§2.4): `service/{id}` embeds full `serviceAreas` geometry by
default; the two list endpoints return `bbox`-only Features unless
`includeGeometry=true`. List responses use the standard list envelope with
`limitExceeded: false` and no `maxCount` in v1 — on-demand service counts are small
(most feeds have a handful; Alexandria has one).

That is the entire v1 surface. `services-for-stop` / `services-for-route` lookups are
deliberately omitted — pointer fields make them redundant (YAGNI).

Service IDs for flex services equal the underlying route's combined ID. OBA entity
types have separate ID spaces, so this collides with nothing.

### 3.1 Pointer fields on `/api/where` (the entire coupling surface)

- **Stop entries** gain `"onDemandServiceIds": [...]` when the stop is referenced by
  any compiled rule or location-group membership — this includes a deviated route's
  ordinary timed stops, which participate in rules without having windowed records
  themselves.
- **Route entries** gain the same when any trip of the route is flex-involved.

The field is **omitted when empty**: non-flex feeds produce byte-identical `/where`
responses to today. Trips need no pointer — every trip screen knows its route.
These two additive fields are the only `/where` divergence from the upstream OpenAPI
spec, to be proposed upstream together with the namespace once proven.

### 3.2 Legacy-surface policy for flex data

- Flex-only routes (dial-a-ride) **continue to appear** in `routes-for-agency` — the
  status quo for imported flex feeds; old apps see a route with no stops, new apps
  see the pointer and render zone UI.
- Flex-only (windowed) stop_times records are **excluded** from
  arrivals-and-departures, schedule-for-stop/route, and trip-details stop-time lists
  — no phantom arrival times. This follows the spec's own consumer routing rule.
- **Flex-only trips** (every record windowed — e.g. both Alexandria trips) have no
  timed stop_times at all, so each trip-serving `/where` endpoint gets an explicit
  disposition: excluded from `trips-for-route`, `trips-for-location`, and
  `block/{id}` (their NULL min/max arrival caches drop them from time-window
  queries naturally — see §1.2); `trip/{id}` and `trip-details/{id}` return the
  trip entity with an empty schedule and no status rather than 404, since the ID
  is real and referenceable. Deviated trips (mixed records) appear everywhere,
  showing only their timed records.
- Zones are not stops: `stops-for-location` and stop-ID surfaces never contain
  locations or location groups.

### 3.3 Errors

Standard OBA semantics: `404` entry-not-found for unknown service IDs; `400` with
`fieldErrors` for missing/invalid coordinates (existing `utils.ParseFloatParam`
path); an empty `list` (not an error) when nothing covers a location. The namespace
is always mounted — a feed with zero flex data yields empty lists, never 404s, so
clients can probe cheaply.

## 4. GOFS Forward Compatibility

GOFS (v1.0, MobilityData-stewarded since 2025) is **a second importer, not a second
API**. The mapping:

| GOFS file | Lands as |
|---|---|
| `zones.json` | `serviceAreas` (accept `zone_id` Feature placement; Polygon-only is a subset of what we store) |
| `operating_rules.json` | `availabilityRules` — the reason rules are shaped as (from, to, pickup window + `endDropOffTime`, calendar array); GOFS's `end_dropoff_window` and windowless all-hours rules map directly |
| `calendars.json` | `calendars` — same structure; dates normalized from GOFS's `YYYYMMDD` on ingest |
| `booking_rules.json` | `bookingRules` — the *field vocabulary* is shared near-verbatim (`prior_notice_calendar_id` is already our exposed name), but the *linkage* is not: GOFS entries have no ID and anchor to zones via `from_zone_ids`/`to_zone_ids`. The importer synthesizes one `bookingRule` (with a generated ID) per GOFS entry and attaches it to every availabilityRule whose from/to zones match. |
| `system_information.json` + `service_brands.json` | `onDemandService` entries with `routeId: null` |

**Identity for GOFS feeds:** GOFS has no agency concept and its IDs are not globally
unique. Each GOFS feed gets a synthesized agency built from
`system_information.json` (name, timezone, url, configured feed ID), registered in
`references.agencies`; that feed ID becomes the combined-ID prefix for its services,
zones, booking rules, and calendars, and is what `services-for-agency/{id}`
addresses. With that in place, all three endpoints serve GOFS-sourced services with
zero contract changes.

**Reserved extension points** — documented here, absent from v1 responses, never to
be occupied with different semantics:

- `brand` (object on `onDemandService`) **and `brandId` (string on
  `availabilityRule`)**: GOFS attaches `brand_id` per operating rule (absent = all
  brands), so the rule-level slot must be reserved too or a GOFS importer would be
  forced into service-per-brand duplication.
- `vehicleTypes` (array on `onDemandService` or rules): capacity, wheelchair enum.
- `waitTime` / `fares`: GOFS dynamic and metered-fare data.
- `deepLinks` (object on `bookingRules`): `iosUri` / `androidUri` / `webUri` /
  `phoneNumber` with GOFS's standardized pickup/drop-off handoff query params.
- Endpoint: `/api/ondemand/quote.json` — future proxy for GOFS `wait_time` /
  `realtime_booking`.

## 5. Testing

The primary integration fixture is a **real feed**: `testdata/alexandria-flex.zip`,
the City of Alexandria VA DOT Paratransit flex-v2 feed published by Trillium
(https://data.trilliumtransit.com/gtfs/cityofalexandria-va-us/cityofalexandria-va-us--flex-v2.zip,
snapshot committed to the repo). It is tiny (one route, two trips, four stop_times
rows, one zone) yet exercises exactly the quirks synthetic fixtures wouldn't:
draft-era `mean_duration_*` columns, `safe_duration_*` on stop_times instead of
trips, >24h windows, asymmetric pickup/drop-off ends (24:50 vs 25:00), header-only
empty `location_groups.txt`/`location_group_stops.txt`, a 4,239-point polygon, and
a mismatched agency-vs-stop timezone.

- **Parser tests** live upstream in go-gtfs, using fixtures built from the gtfs.org
  flex examples (all four patterns: Heartland, MRVT, RufBus, Hermann) plus
  Alexandria excerpts for draft-column tolerance and empty flex files.
- **Rule compilation**: unit tests mapping each pattern's stop_times to expected
  rules, including the degenerate no-pickup case and the dedup/merge steps.
  Alexandria end-to-end: exactly two rules (zone→same zone; 07:00–24:50 pickup /
  25:00 drop-off on the no-Sunday calendar, 05:00 start on the Sunday-only one),
  `safeDurationFactor` 1.0 / offset 0.0 via the stop_times fallback, and the
  `booking_type=2` rule (start day 14, last day 1 at 17:00) attached to both.
- **Geometry**: point-in-polygon with holes, MultiPolygon, viewport-intersection
  mode, points on boundaries.
- **Integration**: endpoint tests via `serveApiAndRetrieveEndpoint` against the
  Alexandria fixture for all three endpoints — point containment inside/outside the
  zone, bbox-only vs `includeGeometry=true` list behavior, and route pointer
  fields. Location-group and deviated-route *endpoint* coverage (stop pointer
  fields, stop-radius matching) uses one minimal synthetic fixture derived from the
  gtfs.org RufBus/Hermann examples, since Alexandria exercises neither pattern.
- **Additive-guarantee regression**: importing plain `raba.zip` must produce `/where`
  responses containing no flex fields and no new references keys anywhere.
- **Import invariants**: fixtures with each violation class (two location refs, a
  window plus arrival_time, dangling booking rule) assert log-and-skip behavior.

## 6. Implementation Sequencing (for the plan)

1. go-gtfs: parse the four files + new fields, tolerating draft-era columns
   (upstream PR; prerequisite).
2. Maglev schema (including the `stop_times` arrival/departure nullability
   migration and its query fallout) + import + rule compilation (no API changes;
   testable via DB). Includes an audit of every `st.Stop` dereference in Maglev —
   e.g. `gtfsdb/helpers.go:419` (`st.Stop.Id` at insert), `:1284` (first-stop
   lookup), `:1414`/`:1422` (block layover pairing) — which will panic on
   stop-less records the moment the upgraded go-gtfs stops dropping them; the
   go-gtfs version bump and these guards must land in the same change.
3. `/api/ondemand` endpoints + extended references model.
4. `/where` pointer fields + legacy-surface exclusions + additive regression test.
5. Test fixtures (`alexandria-flex.zip` + the minimal synthetic
   group/deviated fixture) threaded through 2–4.

## Appendix: Source Material

- GTFS Schedule reference (Flex merged March 2024): https://gtfs.org/documentation/schedule/reference/
- Flex examples (all four patterns): https://gtfs.org/documentation/schedule/examples/flex/
- Original merge PR: https://github.com/google/transit/pull/433
- GOFS spec: https://github.com/MobilityData/GOFS (v1.0 `reference.md`)
- GOFS positioning: https://mobilitydata.org/gofs-a-new-chapter-for-on-demand-transportation-data/
- Real test feed: City of Alexandria VA flex v2 (Trillium),
  https://data.trilliumtransit.com/gtfs/cityofalexandria-va-us/cityofalexandria-va-us--flex-v2.zip
  — snapshot at `testdata/alexandria-flex.zip`
- Notable spec facts relied on above: shared ID namespace across stops/locations/groups;
  windows forbid `arrival_time`/`departure_time` and `pickup_type` ∈ {0,3}
  (`drop_off_type` 3 allowed); `prior_notice_*` durations are minutes;
  `location_group_name` optional; `safe_duration_*` live on trips.txt (the draft-era
  `mean_duration_*` fields were dropped); consumers ignore windowed records when
  computing fixed-route timing.
