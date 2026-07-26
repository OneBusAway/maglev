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
    start_time                INTEGER NOT NULL,  -- ns since midnight; may exceed 24h
    end_time                  INTEGER NOT NULL,
    gtfs_service_id           TEXT NOT NULL,     -- calendar reference
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
  "startTime": "08:00:00",             // "HH:MM:SS", >24h legal (both specs' semantics)
  "endTime": "17:00:00",
  "calendarId": "25_weekday",
  "pickupType": 2,                     // 2 = must book; 3 = coordinate with driver
  "dropOffType": 2,
  "pickupBookingRuleId": "25_booking_route_74362",
  "dropOffBookingRuleId": "25_booking_route_74362",
  "safeDurationFactor": null,          // optional; clients with routing engines can
  "safeDurationOffset": null           // compute the spec's 95th-percentile estimate
}
```

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
   drop-off record, window from the windowed side(s) (intersection when both are
   windowed), calendar = the trip's `service_id`, booking-rule IDs and
   safe-duration values carried through.
4. Rules identical except for `trip_id` collapse to one row per distinct
   (from, to, window, calendar, types, booking) tuple; the stored `trip_id` is a
   representative retained for traceability only and is not exposed in the API.

Pattern projections:

| Flex pattern | Compiles to |
|---|---|
| Dial-a-ride, single zone (Heartland Express) | zone→same zone, one rule per trip/window variant |
| Zone-to-zone (Minnesota River Valley) | zone A→zone B |
| Location group (RufBus) | group→same group; members via references |
| Deviated fixed route (Hermann Express) | fixed stop ↔ deviation zone pairs per segment |

A degenerate feed (e.g. the published Hermann example, which technically permits no
pickups anywhere) compiles to a service with areas and booking info but few or no
rules — still renderable. Rule counts are bounded in practice (flex trips have 2–3
records; deviated routes pair only adjacent segments), and compilation happens once
at import, so queries stay cheap.

### 2.4 New reference types

Added to the standard `references` block alongside `agencies`/`routes`/`stops`/etc.:

**`serviceAreas`** — GeoJSON Features, verbatim geometry:

```jsonc
{
  "id": "25_area_708",
  "name": "Brown County",
  "geometry": { "type": "Polygon", "coordinates": [ /* … */ ] }
}
```

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
by additional rules — the GOFS idiom. All API dates are `YYYY-MM-DD` strings in the
agency's timezone; all times of day are `"HH:MM:SS"` strings that may exceed 24:00:00.

Stops referenced by rules or group memberships appear in `references.stops` in the
standard shape; routes and agencies likewise.

## 3. Endpoints & Contracts

All endpoints: HTTP GET, `.json`, API-key validation, rate limiting, gzip, standard
OBA envelope. Registered in `internal/restapi/routes.go` next to the `/where` routes.

| Endpoint | Contract |
|---|---|
| `/api/ondemand/services-for-location.json` | `lat` + `lon` (required floats) → list of services whose areas contain the point. Optional `latSpan` + `lonSpan` switch to viewport-intersection mode, mirroring `stops-for-location` conventions. Services matched via location-group member stops or plain windowed stops use those stops' coordinates. |
| `/api/ondemand/services-for-agency/{id}.json` | All on-demand services for the agency. List response. |
| `/api/ondemand/service/{id}.json` | Single service, full rules + references. Entry response. |

That is the entire v1 surface. `services-for-stop` / `services-for-route` lookups are
deliberately omitted — pointer fields make them redundant (YAGNI).

Service IDs for flex services equal the underlying route's combined ID. OBA entity
types have separate ID spaces, so this collides with nothing.

### 3.1 Pointer fields on `/api/where` (the entire coupling surface)

- **Stop entries** gain `"onDemandServiceIds": [...]` when the stop belongs to a
  location group or appears in windowed stop_times.
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
| `operating_rules.json` | `availabilityRules` — the reason rules are shaped as (from, to, window, calendar) |
| `calendars.json` | `calendars` — deliberately identical shape |
| `booking_rules.json` | `bookingRules` — near-verbatim shared vocabulary; `prior_notice_calendar_id` already our exposed field name |
| `system_information.json` + `service_brands.json` | `onDemandService` entries with `routeId: null` |

All three endpoints serve GOFS-sourced services with zero contract changes.

**Reserved extension points** — documented here, absent from v1 responses, never to
be occupied with different semantics:

- `brand` (object on `onDemandService`): GOFS service brands (id, name, colors).
- `vehicleTypes` (array on `onDemandService` or rules): capacity, wheelchair enum.
- `waitTime` / `fares`: GOFS dynamic and metered-fare data.
- `deepLinks` (object on `bookingRules`): `iosUri` / `androidUri` / `webUri` /
  `phoneNumber` with GOFS's standardized pickup/drop-off handoff query params.
- Endpoint: `/api/ondemand/quote.json` — future proxy for GOFS `wait_time` /
  `realtime_booking`.

## 5. Testing

- **Parser tests** live upstream in go-gtfs, using fixtures built from the gtfs.org
  flex examples (all four patterns: Heartland, MRVT, RufBus, Hermann).
- **Rule compilation**: unit tests mapping each pattern's stop_times to expected
  rules, including the degenerate no-pickup case and the dedup step.
- **Geometry**: point-in-polygon with holes, MultiPolygon, viewport-intersection
  mode, points on boundaries.
- **Integration**: new `testdata/raba-flex.zip` — RABA augmented with a dial-a-ride
  zone, a location group, and one deviated trip — so `createTestApi`-style helpers
  work unchanged. Endpoint tests via `serveApiAndRetrieveEndpoint` for all three
  endpoints, plus pointer-field assertions on stop and route responses.
- **Additive-guarantee regression**: importing plain `raba.zip` must produce `/where`
  responses containing no flex fields anywhere.
- **Import invariants**: fixtures with each violation class (two location refs, a
  window plus arrival_time, dangling booking rule) assert log-and-skip behavior.

## 6. Implementation Sequencing (for the plan)

1. go-gtfs: parse the four files + new fields (upstream PR; prerequisite).
2. Maglev schema + import + rule compilation (no API changes; testable via DB).
3. `/api/ondemand` endpoints + reference types.
4. `/where` pointer fields + legacy-surface exclusions + additive regression test.
5. Test fixtures (`raba-flex.zip`) threaded through 2–4.

## Appendix: Source Material

- GTFS Schedule reference (Flex merged March 2024): https://gtfs.org/documentation/schedule/reference/
- Flex examples (all four patterns): https://gtfs.org/documentation/schedule/examples/flex/
- Original merge PR: https://github.com/google/transit/pull/433
- GOFS spec: https://github.com/MobilityData/GOFS (v1.0 `reference.md`)
- GOFS positioning: https://mobilitydata.org/gofs-a-new-chapter-for-on-demand-transportation-data/
- Notable spec facts relied on above: shared ID namespace across stops/locations/groups;
  windows forbid `arrival_time`/`departure_time` and `pickup_type` ∈ {0,3}
  (`drop_off_type` 3 allowed); `prior_notice_*` durations are minutes;
  `location_group_name` optional; `safe_duration_*` live on trips.txt (the draft-era
  `mean_duration_*` fields were dropped); consumers ignore windowed records when
  computing fixed-route timing.
