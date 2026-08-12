# Multi-Agency Static GTFS Feeds and Stop Consolidation

## 1. What the Java feature is, in one paragraph

The Java OBA bundle builder can load **N per-agency GTFS zips** instead of one
pre-merged zip. Each zip gets a `defaultAgencyId` stamped onto its agency-less
entities (most importantly stops, since `stops.txt` has no `agency_id`
column), so every stop is owned by exactly one agency. A second mechanism —
`EntityReplacementStrategy`, driven by a plain-text mapping file — merges
physically shared stops across agencies ("absorb stop `UCSD_gtsg-1` into
canonical stop `MTS_60123`"), rewriting all `stop_times` references to the
canonical stop at load time. Together these give a multi-agency deployment
per-agency identity *plus* one map pin per physical curb.

This spec defines the Maglev equivalent: **multiple static feeds in
`config.json`, merged in memory at import time, with an optional
stop-consolidation file in the same format the Java tooling uses.** The
feed list and the consolidation config key are specified in §3.1; the
consolidation mechanism itself — file format, matching, rewrite semantics,
and error rules — is §5.

Review the spec describing the Java feature at https://github.com/OneBusAway/onebusaway-application-modules/blob/main/docs/oba-multi-agency-bundle-spec.md

---

## 2. How the two servers behave today

### 2.1 Maglev today (verified against `main`)

- **Exactly one static GTFS source.** Config is a single object
  (`gtfs-static-feed` in `internal/appconf/json_config.go:13-18,42`; CLI flag
  `-gtfs-url` in `cmd/api/main.go:69`; `Config.GtfsURL` in
  `internal/gtfs/config.go:24-34`). The load path is
  `loadGTFSData` → `gtfsdb.ParseGtfsData` → `Client.StoreGtfsData`
  (`internal/gtfs/static.go:127-143`, `gtfsdb/helpers.go:39,204`). There is no
  way to point Maglev at two zips.
- **Entity IDs are stored raw.** `stops.id`, `trips.id`, `routes.id`, etc.
  are the unprefixed GTFS values (`gtfsdb/helpers.go:265-475`). The
  `{agencyId}_{rawId}` combined form exists only at the API layer
  (`utils.FormCombinedID`).
- **Stop→agency association is transitive, not stored.** The `stops` table
  has no `agency_id` column (`gtfsdb/schema.sql` — only `routes` carries one).
  Queries like `GetStopIDsForAgency` and `GetStopForAgency` join
  `stops → stop_times → trips → routes` and filter on `routes.agency_id`
  (`gtfsdb/query.sql:265-274,315-339`).
- **The agency prefix on a stop's public ID is computed per response.**
  Handlers batch-fetch the agencies serving each stop via
  `GetAgenciesForStops` (`gtfsdb/query.sql:676-693`) and take the first row
  per stop (`internal/restapi/stops_for_location_handler.go:180-186`). Note
  that query has no `ORDER BY`, so for a stop served by multiple agencies the
  chosen prefix is whatever SQLite returns first. (`GetAgencyForStop`,
  `gtfsdb/query.sql:237-257`, does order by agency id, but no handler
  currently calls it.)
- **Routes with an omitted `agency_id` are only defaulted when the whole
  feed has exactly one agency** — the `singleAgencyID` fallback in
  `StoreGtfsData` (`gtfsdb/helpers.go:283-291,733-738`).
- **Import is atomic and hash-gated.** One `import_metadata` row stores
  `(file_hash, file_source)`; a matching hash skips reimport, a changed hash
  clears every table and reimports inside one transaction
  (`gtfsdb/helpers.go:218-259`).
- **GTFS-RT is already multi-feed.** `gtfs-rt-feeds` is a list with per-feed
  `agency-ids` filtering (`internal/gtfs/config.go:12-21`). Only static
  loading is single-feed.
- **No stop consolidation mechanism of any kind.**

### 2.2 Behavioral differences, side by side

The Java spec's motivating bug (§2 there: `stop-ids-for-agency` returns 0 for
three of four agencies when fed a pre-merged zip) is live in production
today — as of 2026-08, `realtime.sdmts.com` returns 6,110 stop IDs for MTS
and 0 for NCTD, SAN, and UCSD. It **does not reproduce in Maglev**. Because Maglev derives stop ownership transitively through routes,
feeding it the same pre-merged 4-agency zip already yields correct non-zero
per-agency stop lists. The differences that remain:

| Behavior | Java OBA (with the bundle spec applied) | Maglev today |
|---|---|---|
| Static inputs | N per-agency zips, one `GtfsBundle` each | Exactly one zip |
| Stop ownership | Exactly one agency — the feed's `defaultAgencyId` | Every agency whose routes serve the stop; a stop served by MTS and UCSD routes appears in **both** agencies' `stop-ids-for-agency` lists |
| Stops served by no trip | Owned by the feed's default agency | Owned by no agency (transitive joins find nothing); still returned by location/search endpoints |
| Public stop ID prefix | The owning agency, fixed at build time | Endpoint-dependent: per-agency/per-route endpoints stamp the *requested* agency; location/search endpoints use the first agency returned by `GetAgenciesForStops` (unordered) — see §3.3 |
| Absorbed-stop lookups | 404 (entity never stored) | n/a — no consolidation |
| Cross-feed ID collisions | Impossible — every ID is an `AgencyAndId` pair | n/a today (single feed); would be primary-key violations if feeds were naively concatenated |
| Shared physical stops | Consolidated via mapping file | Two stops, two map pins, arrivals split across them |

Two consequences worth internalizing before implementing:

1. Maglev does **not** need the Java `defaultAgencyId` mechanism to fix
   per-agency stop lists — they already work. What Maglev gains from this
   project is (a) no upstream pre-merge step, and (b) stop consolidation,
   which is the user-visible win (one pin per curb, arrivals from both
   agencies on one stop).
2. Maglev's raw-ID storage means cross-feed ID collisions are a real hazard
   the Java system never had. §4.3 defines how we handle them.

---

## 3. New state in Maglev

### 3.1 Configuration

`gtfs-static-feeds` (plural) replaces `gtfs-static-feed` as the preferred
form. The legacy singular key keeps working and is normalized internally into
a one-element list; specifying both is a validation error.

```json
{
  "gtfs-static-feeds": [
    { "url": "https://…/mts.zip",  "default-agency-id": "MTS" },
    { "url": "https://…/nctd.zip", "default-agency-id": "NCTD" },
    { "url": "https://…/san.zip",  "default-agency-id": "SAN" },
    { "url": "https://…/ucsd.zip", "default-agency-id": "UCSD",
      "auth-header-name": "Authorization", "auth-header-value": "Bearer …" }
  ],
  "stop-consolidation-file": "./config/sdmts-stop-consolidation.txt"
}
```

Per-feed fields: `url` (required), `default-agency-id` (optional),
`auth-header-name`/`auth-header-value` (optional, paired),
`enable-gtfs-tidy` (optional) — i.e. today's `GtfsStaticFeed` struct plus
`default-agency-id`. Validation rules:

- Each feed's URL gets the same checks the single feed gets today
  (`json_config.go:184-201`).
- A present `gtfs-static-feeds` key must contain at least one feed — an
  explicitly empty list is a validation error, not "no feeds" (the staged
  implementation reads `staticFeeds()[0]`, and an empty list would
  otherwise panic, or silently fall through to a legacy default).
- Specifying both `gtfs-static-feed` and `gtfs-static-feeds` is an error.
  Detecting this requires knowing the singular key was *present*, which
  the current value-struct config can't tell apart from an omitted key —
  an empty `gtfs-static-feed: {}` and no key at all produce the same zero
  value. PR 1 either makes the legacy field a pointer or checks presence
  against the raw JSON before defaults run.
  **Defaulting interaction:** `setDefaults()` currently injects a Sound
  Transit URL when the singular key is empty (`json_config.go:71-73`); that
  defaulting must be skipped whenever the plural list is non-empty,
  otherwise every plural-only config either trips the both-keys error or
  silently gains a phantom feed.
- Explicit `default-agency-id` values must be unique across feeds, and must
  not contain `_` (the combined-ID separator — see §9.1). Enforced at
  config time. Feed-derived defaults get the same two checks at import
  time, once `agency.txt` is available.
- At import time, a configured `default-agency-id` must name an agency
  present in that feed's `agency.txt` — §4.2's route stamping needs the
  agency row, and `routes.agency_id` carries an FK to `agencies(id)`
  (`gtfsdb/schema.sql:42`). Fail with a config-shaped error message, not a
  mid-transaction FK violation.

`default-agency-id` defaults to the first agency in that feed's `agency.txt`
— the same fallback the Java `GtfsReader` uses. It exists for exactly two
purposes:

1. Resolving `MTS_60123`-style tokens in the consolidation file to a stop in
   a specific feed (§5.2).
2. Stamping `agency_id` onto routes that omit it, per feed, before merging
   (§4.2) — the existing `singleAgencyID` fallback in `StoreGtfsData` sees
   the *merged* agency list and goes inert once there is more than one agency.

It is **not** stamped onto stops and does not change stop ownership — that
stays transitive (§2.1). For single-agency feeds (the normal case) you can
omit it entirely.

`stop-consolidation-file` is optional; a local path or `https://` URL.
Format and semantics are §5. When omitted, no consolidation runs and shared
physical stops remain one stop per feed, exactly as today.

CLI flags stay single-feed. Multi-feed and consolidation require the JSON
config file.

### 3.2 Import pipeline

Current: download one zip → parse → validate → store.

New (all inside the existing `ReloadStatic` / `staticUpdateMutex` envelope,
`internal/gtfs/static.go:196`):

1. Download and parse each configured feed independently (reusing
   `rawGtfsData` + `gtfsdb.ParseGtfsData`, which also gives per-feed gtfstidy
   and per-feed sha256 hashes). Any feed failing to download or parse fails
   the whole import — a partial multi-agency dataset silently drops agencies,
   which is worse than keeping yesterday's data (Java spec §8.9 reaches the
   same conclusion).
2. Per feed: resolve `default-agency-id` (config value or first agency), and
   stamp it onto any route with a nil/empty `Agency`.
3. **Consolidate** stops if a consolidation file is configured (§5) —
   applied to the **per-feed** structs, per §4.1's pointer-aliasing rule,
   through a provenance index built over the per-feed data. Absorbed
   stops are removed from their owning feed's slices here, so they never
   exist anywhere downstream.
4. **Collision-check** (§4.3) — scans the per-feed ID sets, which
   consolidation has already purged of absorbed stops, so declared shared
   stops don't trip it, and which-feed-owns-which-ID provenance is
   inherent for every entity type in the error message.
5. **Merge** the N parsed `gtfs.Static` structs into one (§4) — pure
   concatenation, assembled only after all per-feed mutations (§4.1).
6. Compute the combined hash (§4.4) and hand the merged result to the
   existing `importStaticIntoDB` / `StoreGtfsData` unchanged.

With one configured feed, no explicit `default-agency-id`, and no
consolidation file, steps 2–5 are no-ops and behavior is byte-for-byte
today's (§4.2's stamping condition is what keeps step 2 inert here).

### 3.3 Resulting API behavior (what to assert in tests)

Given MTS + UCSD feeds and a consolidation line `MTS_60123 UCSD_gtsg-1`:

A prefix rule to internalize first, because the assertions below depend on
it: **different endpoint families spell a stop's combined ID differently.**
Per-agency and per-route endpoints stamp the *requested* agency onto every
raw stop ID (`stop_ids_for_agency_handler.go:44`,
`stops_for_route_handler.go:174,276,502` — `FormCombinedID(requestedAgency,
rawID)`), while location/search endpoints derive the prefix from the first
row of `GetAgenciesForStops` (§2.1). The canonical stop is one row in the
DB, but its spelling varies by endpoint. v1 makes no handler changes, so the
tests must assert these spellings as-is:

- `stop-ids-for-agency/MTS` and `.../UCSD` are both non-empty; the canonical
  stop appears in **both** lists — the UCSD trips' stop_times now reference
  it, so the transitive query picks it up for UCSD automatically. It is
  spelled `MTS_60123` in the MTS list and `UCSD_60123` in the UCSD list
  (requested-agency stamping, above). Both spellings resolve via
  `stop/{id}` because `GetStopForAgency` validates through serving routes.
  (Java parity for the UCSD list existing at all; two deliberate
  divergences — multi-owner listing and request-dependent spelling — see
  §2.2. Flag both in the PR description per CONTRIBUTING.)
- `arrivals-and-departures-for-stop/MTS_60123.json` returns arrivals from
  both agencies' routes, with both agencies in `references.agencies`.
- `stops-for-route/UCSD_<route>.json` includes the canonical stop, spelled
  `UCSD_60123` (requested-agency stamping again).
- Any lookup of the absorbed ID (`UCSD_gtsg-1`) returns 404 — the entity was
  removed before import, matching the Java runtime behavior.
- One pin per physical curb in map/location queries.
- `agencies-with-coverage` lists all agencies with correct bounds — no code
  change needed; `computeRegionBounds` already works per agency off the DB
  (`internal/gtfs/shapes.go:16`).
- GTFS-RT keeps working unchanged: RT trip IDs are matched raw against
  `trips.id`, and the merge stores raw IDs. Configure one `gtfs-rt-feeds`
  entry per RT **source** (not per agency), with these rules:
  - **Omit `agency-ids` for a source that only carries one agency's data.**
    The filter is stricter than it looks: `filterTripsByAgency` /
    `filterVehiclesByAgency` drop any entity whose TripDescriptor lacks a
    `route_id` (`internal/gtfs/realtime.go:520-522,539-541`), and stop-only
    alerts are dropped whenever filtering is active (`realtime.go:564-566`).
    For a single-agency source the constraint is already satisfied
    structurally, so setting `agency-ids` only adds a way to silently lose
    data. Reserve it for genuinely mixed-agency RT feeds. (Follow-up worth
    filing, not part of this spec: fall back to a static trip lookup when
    `route_id` is absent from the descriptor.)
  - **Multiple RT sources for one agency are supported.** Each feed entry
    gets a unique `id` (duplicates rejected,
    `internal/appconf/json_config.go:293-296`); per-feed sub-maps merge by
    concatenation (`realtime.go:589-706`) and stale-vehicle expiry is
    tracked per feed (`feedVehicleLastSeen`; `realtime.go:30-31,77-95,
    381-408`). The one assumption: the sources must be
    **disjoint** (e.g. bus vs. rail). A vehicle or trip present in two
    sources appears twice in `vehicles-for-agency`, and by-ID lookups
    resolve to whichever feed sorts last.
  - Partial sets are fine — a feed entry needs at least one URL
    (trip-updates, vehicle-positions, or alerts) to activate
    (`internal/gtfs/config.go:37-45`); it doesn't need all three.
  - Independent of filtering, `VehiclesForAgencyID` attributes vehicles via
    the `route_id` in the vehicle's TripDescriptor
    (`internal/gtfs/gtfs_manager.go:526-538`) — an RT source that omits
    `route_id` yields an empty `vehicles-for-agency` even today. Verify each
    vendor feed carries `route_id` before blaming this change.

---

## 4. The merge step

New file `internal/gtfs/merge.go`.

```go
// parsedFeed pairs one feed's parsed GTFS data with its resolved agency
// identity and content hash.
type parsedFeed struct {
    static          *gtfs.Static
    defaultAgencyID string // config override or first agency in the feed
    hash            string // sha256 of the zip bytes (from gtfsdb.ParseGtfsData)
    source          string // URL or path, for error messages
}

// buildStopIndex indexes every feed's stops by (defaultAgencyID, stopID)
// for consolidation-token resolution (§5.2). Built over the per-feed
// structs, so mutations through it satisfy §4.1's pointer-aliasing rule.
func buildStopIndex(feeds []parsedFeed) map[stopKey]*gtfs.Stop

// checkCrossFeedCollisions fails if any two feeds share a raw
// stop/trip/route/service/shape/block/agency ID. It scans the per-feed
// data — from which consolidation has already removed absorbed stops —
// so both source feeds are known for every collision, for every entity
// type, without a separate provenance structure.
func checkCrossFeedCollisions(feeds []parsedFeed) error

// mergeFeeds concatenates per-agency feeds into a single gtfs.Static.
// Called only after all per-feed mutations (route stamping,
// consolidation) are complete — see the §4.1 pointer-aliasing rule.
func mergeFeeds(feeds []parsedFeed) (*gtfs.Static, error)
```

### 4.1 Concatenation

`gtfs.Static` (from `github.com/OneBusAway/go-gtfs`) is plain slices:
`Agencies`, `Routes`, `Stops`, `Transfers`, `Services`, `Trips`, `Shapes`.
Merging is slice concatenation. Cross-entity references inside each feed are
already pointers resolved at parse time, so concatenation cannot dangle them.
(Maglev does not persist `Transfers`; concatenate them anyway for
completeness, they're dropped by `StoreGtfsData` as they are today.)

**Pointer-aliasing rule — this ordering is load-bearing.** The slices hold
entities *by value* (`Stops []Stop`) while cross-references point into the
per-feed backing arrays (`StopTime.Stop *Stop`, `Stop.Parent *Stop`).
`append` copies the structs into a new backing array, so after
concatenation `&merged.Stops[i]` is a **different struct** from the one the
feed's `StopTime.Stop` points at. Therefore: every mutation — route-agency
stamping (§4.2), consolidation rewrites (§5.3), and §9's ID rewriting —
must operate on the **per-feed** structs (or match by ID value, never by
pointer identity against merged copies), and the merged slices must be
assembled **after** all mutations. `StoreGtfsData` reads stop_times through
the pointers (`t.StopTimes[].Stop.Id`, `gtfsdb/helpers.go:419`), so
mutating only the merged copies silently imports stale data, and comparing
pointers against merged copies silently rewrites nothing.

### 4.2 Route agency stamping (before merge, per feed)

For each feed, for each route where `route.Agency == nil` or
`route.Agency.Id == ""`, set it to the feed's default agency — but **only
when the feed ships exactly one agency in its `agency.txt`, or the
operator configured an explicit `default-agency-id`.** A multi-agency
feed with agency-less routes (invalid GTFS — `agency_id` is required when
multiple agencies exist) keeps today's behavior: the route stays
unstamped, exactly as the inert `singleAgencyID` fallback leaves it.
Stamping the first agency there would silently guess an owner and change
single-feed behavior, breaking §3.2's byte-for-byte claim.

The stamping must happen per feed because after merging, `StoreGtfsData`'s
`len(Agencies) == 1` fallback (`gtfsdb/helpers.go:283-286`) no longer fires.
Leave the existing fallback in place — it still covers the single-feed path.

### 4.3 Cross-feed ID collisions: detect and fail

Two feeds shipping the same `stop_id`, `trip_id`, `route_id`, `service_id`,
`shape_id`, or `block_id` would collide on Maglev's raw primary keys —
either a constraint violation mid-import or, worse, silent cross-agency data
corruption (e.g. two feeds' `service_id=WEEKDAY` calendars merging into one).
The Java system never faces this because every ID there is an
`(agency, id)` pair — the Java spec's §8.4 explicitly *blesses* cross-feed
stop_id reuse, which Maglev's raw-ID storage cannot tolerate.

Block IDs are on the list even though block *layovers* are keyed by
`(block_id, service_id)` (`gtfsdb/helpers.go:1374-1390`): `GetTripsByBlockID`
backing `GetVehicleForTrip` (`internal/gtfs/gtfs_manager.go:593`) and
`GetBlockDetails` backing `/block/{id}`
(`internal/restapi/block_handler.go:38`) both filter on `block_id` alone —
a cross-feed block_id collision silently cross-links two agencies' blocks.

**Empirical reality check (external observation, 2026-08 — not a code
citation; re-verify against current feeds): the published SDMTS-area feeds
do collide.** Intersecting the real MTS and NCTD zips: trip, block, service,
shape, and route IDs are disjoint, but **38 raw `stop_id` values appear in
both** (e.g. `10034`, `10374`, `11151`). Any collision policy has to survive
that fact. When the §9.4 decision-gate audit runs, check the full 38-ID list
into this repo next to this spec so the decision is reproducible.

**v1 policy: consolidation first, then any remaining cross-feed collision
fails the import**, with one error listing every collision: entity type, ID,
and the two source feeds.

- **Ordering matters:** stop consolidation (§5) is applied *before* the
  collision check. A colliding stop_id that refers to the *same physical
  stop* in both feeds is handled by declaring it in the consolidation file
  (`MTS_10034 NCTD_10034`) — the absorbed copy is removed before the check
  runs. Audit the 38 known collisions first: any that are genuinely shared
  curbs belong in the consolidation file, not in a renaming pipeline.
- Collisions between *physically distinct* stops (coincidental numeric
  overlap) cannot be consolidated — that would merge two different curbs.
  For those, the operator must rename upstream when producing the per-agency
  zips. Silent alternatives (first-wins, automatic prefixing) either corrupt
  data or change public IDs and break GTFS-RT matching for the renamed feed,
  so v1 refuses instead.
- This whole policy is a v1 compromise. §9 proposes the v2 architecture —
  namespaced entity storage — that makes cross-feed collisions structurally
  impossible and deletes this section outright.

Check agencies too: the same `agency.txt` ID appearing in two feeds is a
collision and fails. (Future work, not v1: per-feed `agency-id-mappings`
mirroring the Java `GtfsBundle` field, for feeds that both ship
`agency_id=1`.)

### 4.4 Change detection across N feeds

Replace the single-zip hash with a combined fingerprint, computed in the new
load path and passed through the existing `GtfsData.Hash`/`Source` fields:

- `Hash` = sha256 over, in config order: each feed's URL, resolved
  `default-agency-id`, and content hash, plus the consolidation file's
  content hash (or a fixed marker when absent) — with each field
  length-prefixed (or delimiter-escaped) so adjacent values can't collide
  ambiguously. Including URLs means reordering or re-pointing feeds
  triggers reimport; including the consolidation file means editing it
  triggers reimport; including the resolved `default-agency-id` means a
  config change that alters route stamping or consolidation-token
  resolution (§4.2, §5.2) triggers reimport even though no zip's bytes
  changed — all three required for correctness.
- `Source` = the feed URLs joined with `,` (it's a TEXT column used only for
  logging and the skip-check).
- **Single-feed compatibility:** with exactly one feed, no explicit
  `default-agency-id`, and no consolidation file, `Hash` degenerates to
  the raw zip-bytes sha256 exactly as today
  (`gtfsdb/helpers.go:40-41`) and `Source` to the single URL — otherwise
  every existing deployment would see a spurious hash mismatch and a forced
  full reimport on upgrade, violating the §3.2/§7 "identical behavior"
  guarantee.

No schema change; `import_metadata` semantics are untouched.

---

## 5. Stop consolidation

New file `internal/gtfs/consolidation.go`.

### 5.1 File format — identical to the Java tooling

Keep byte-level compatibility with the format in the Java spec §5.2 so
existing files (and files produced by existing candidate-scan tooling) work
unmodified.

The file is line-oriented. Each line is exactly one of:

- **Skipped:** a blank line (empty or only spaces/tabs), or a line whose
  first non-whitespace characters are `#`, `{{{`, or `}}}` (the latter two
  are artifacts of the original Trac-wiki-hosted Puget Sound file; skip them
  for compatibility).
- **A mapping rule:** two or more stop-ID tokens separated by runs of one or
  more spaces and/or tabs. The **first** token is the canonical stop — the
  entity that survives. **Every** subsequent token (one or more) is an
  absorbed stop: it is deleted and all references to it are rewritten to the
  canonical (§5.3).
- **Anything else is a parse error** that fails the import (§5.4): a line
  with only one token, or a token that isn't a well-formed stop ID.

Token rules:

- Each token is a combined stop ID, `{agencyId}_{stopId}`, split on the
  **first** `_` — stop IDs may themselves contain underscores.
  `utils.ExtractAgencyIDAndCodeID` (`internal/utils/api.go:99-105`) already
  implements exactly this split; reuse it. A token with no `_` is a parse
  error.
- Compatibility quirk: a line containing `"` splits on quote characters
  instead of whitespace, to allow IDs containing spaces (the Java factory
  does this). Support it — it's a few lines — but don't advertise it.
- There is no inline-comment syntax: `#` only starts a comment at the
  beginning of a line. A trailing `# note` on a rule line would parse as
  absorbed-stop tokens and fail as malformed IDs — which is the desired
  loud failure.

A worked example (SDMTS-flavored, matching the Java spec's Annex A):

```text
# SDMTS stop consolidation
# Format: <canonical>  <absorbed> [<absorbed> ...]
# Canonical's lat/lon and name survive; absorbed stops are deleted and
# every stop_times/parent_station reference to them is rewritten.

# UCSD shuttle stops sharing MTS curbs on Gilman Dr
MTS_60123   UCSD_gtsg-1
MTS_60125   UCSD_gtsg-3

# Same physical stop shipped by both MTS and NCTD under a colliding raw id
# (one of the 38 known collisions — see §4.3)
MTS_10034   NCTD_10034

# One canonical absorbing two stops (transit center bays)
MTS_70114   NCTD_OSTC_bay1  NCTD_OSTC_bay2
```

```go
// stopKey addresses a stop as an (agencyID, rawStopID) pair.
type stopKey struct{ AgencyID, StopID string }

// consolidationRule is one parsed mapping line. Line and RawText survive
// into apply-time errors: §5.4 requires canonical-not-found to name the
// offending line, and that condition is only detectable during
// application — a bare map would have discarded the metadata by then.
type consolidationRule struct {
    Canonical stopKey
    Absorbed  []stopKey
    Line      int
    RawText   string
}

func parseConsolidationFile(r io.Reader) ([]consolidationRule, error)

// applyStopConsolidation removes absorbed stops from their owning feed's
// data and rewrites every reference to point at the canonical stop.
// Operates on the per-feed structs via the §5.2 index — never on merged
// copies (§4.1) — and runs before mergeFeeds (§3.2).
func applyStopConsolidation(feeds []parsedFeed,
    index map[stopKey]*gtfs.Stop, rules []consolidationRule) error
```

### 5.2 Matching tokens to stops

A token's agency ID selects the feed whose resolved `default-agency-id`
matches; the stop ID then selects the stop within that feed's `Stops`. This
mirrors the Java semantics exactly: there, stops are addressable as
`(defaultAgencyId, stopId)` because the loader stamped them; here we keep the
per-feed provenance around through the merge instead of stamping anything.
The lookup (`map[stopKey]*gtfs.Stop`) is built over the per-feed data by
`buildStopIndex` before consolidation runs (§3.2, §4).

One inherited limitation: a feed containing multiple agencies (the real MTS
zip carries both MTS and SAN in `agency.txt`) has **one** default agency, so
all its stops are addressable in the consolidation file only under that
agency's prefix (`MTS_*`, never `SAN_*`). Java has the identical limitation —
`defaultAgencyId` stamps every stop in the feed. API behavior is unaffected;
this only constrains how consolidation tokens are written.

### 5.3 Applying a mapping

For each `absorbed → canonical` pair, on the **per-feed** data — never
the merged slices, which don't exist yet (§3.2) and whose value-copies
the per-feed pointers wouldn't reference anyway (§4.1):

1. Remove the absorbed `gtfs.Stop` from its owning feed's `Stops` — it
   must never reach the DB (the Java `_rejectionStore` equivalent, minus
   the side-store: Maglev has no consumer for rejected entities), and its
   absence from the per-feed data is what keeps it out of the §4.3
   collision check.
2. Rewrite every `StopTime.Stop` pointer that references the absorbed stop
   to the canonical stop (walk every feed's `Trips[i].StopTimes` — the
   absorbed agency's feed is the usual referrer, but don't assume it's
   the only one).
3. Rewrite every `Stop.Parent` pointer referencing the absorbed stop to the
   canonical stop, in every feed.
4. Rewrite `Transfers` to/from pointers the same way (harmless today, correct
   if transfers are ever persisted).

The canonical stop's lat/lon, name, and code win; the absorbed stop's are
discarded — same as Java (§5.5 there). Choose canonicals accordingly when
authoring the file (curb owner's coordinates are usually the trustworthy
ones).

### 5.4 Error rules

| Condition | Behavior | Why |
|---|---|---|
| Canonical ID not found in any feed | **Import fails**, error names the line | Equivalent of the Java pipeline's `GtfsStopReplacementVerificationMain` CI gate (Java spec §7.4). At runtime "fail the import" means: at startup, the process exits; on the 24h refresh, the reload errors and yesterday's data keeps serving — both safe. |
| Absorbed ID not found | **Warn and skip** that token | Absorbed stops legitimately vanish from upstream feeds (Java spec §8.11); a consolidation line becoming a no-op must not take the server down. |
| Same absorbed ID on two lines | **Import fails** | The Java parser silently lets the last line win (Java spec §8.8 calls this out as a footgun); we can do better than order-dependent silence. |
| Absorbed ID also used as a canonical | **Import fails** | Chained absorption (`A←B`, `B←C`) is ambiguous; the file must say `A B C` instead. |
| Malformed line: only one token, or any token without `_` | **Import fails**, error shows the line | Typos in this file silently un-merge stops otherwise. |
| Agency prefix matches no configured feed | **Import fails** | Almost certainly a stale or misspelled agency ID. |

### 5.5 What consolidation does *not* do in Maglev

No replacement audit CSV (`gtfs_stop_replacements.csv` in Java): log one
`slog` info line per applied consolidation instead — that lands in the
structured logs, which is Maglev's audit channel. No suggestion/candidate
tool: candidate detection stays upstream pipeline tooling, exactly where the
Java spec §7.2 puts it (`utils.Haversine` is available if anyone wants a
maglev-adjacent script later).

**Known limitation — GTFS-RT StopTimeUpdates referencing absorbed stop
IDs.** Consolidation rewrites *static* references only. The absorbed
agency's RT feed will keep emitting the absorbed ID (`gtsg-1`) in
`StopTimeUpdate.stop_id`, but the schedule rows now carry the canonical ID —
and Maglev matches StopTimeUpdates against schedule stop IDs
(`internal/restapi/trip_updates_helper.go:278,504`). Real-time predictions
from the absorbed agency at a consolidated stop can therefore fail to
match, partially masked by the stop-sequence fallback where present. v1
accepts this: for SDMTS, the canonical is chosen as the curb-owning agency,
whose RT feed uses the canonical ID, so the common case works. Rewriting
absorbed IDs at RT ingest (the consolidation map applied to incoming
StopTimeUpdates) is the fix if this bites — file it as follow-up work, and
verify the behavior via the §7 checklist row on absorbed-agency RT. If
that measurement shows material prediction loss for a deployment, the
RT-ingest rewrite stops being optional follow-up and becomes a
prerequisite for enabling consolidation there — record the decision
alongside the measurement.

---

## 6. Exact changes, by file

Per CONTRIBUTING, this is 3 PRs, each independently reviewable and under
~200 lines of non-test code.

### PR 1 — Config plumbing (no behavior change)

| File | Change |
|---|---|
| `internal/appconf/json_config.go` | Add `GtfsStaticFeeds []GtfsStaticFeed` (`json:"gtfs-static-feeds"`) and `StopConsolidationFile string` (`json:"stop-consolidation-file"`) to `JSONConfig`; add `DefaultAgencyID string` (`json:"default-agency-id"`) to `GtfsStaticFeed`. Validation: error if both singular and plural keys are set; run the existing URL/auth-pair checks over every feed; validate the consolidation path with `validatePath`. `ToGtfsConfigData` emits a normalized `[]StaticFeedConfigData` (legacy singular becomes a one-element list). |
| `internal/gtfs/config.go` | Add `StaticFeedConfig{URL, DefaultAgencyID, AuthHeaderKey, AuthHeaderValue, EnableGTFSTidy}` and `Config.StaticFeeds []StaticFeedConfig` + `Config.StopConsolidationFile`. Add a `staticFeeds()` accessor that falls back to a one-element list built from the legacy `GtfsURL`/auth fields so `cmd/api` flag users are unaffected. Move `isLocalFile` to the per-feed struct. |
| `cmd/api/main.go` | Populate the new fields from `GtfsConfigData`; CLI flags untouched. |
| `config.schema.json`, `config.example.json` | Document the new keys. |

Everything still reads `staticFeeds()[0]` at this point; tests assert
normalization and validation only.

### PR 2 — Multi-feed load and merge

| File | Change |
|---|---|
| `internal/gtfs/static.go` | `loadGTFSData` becomes: loop `staticFeeds()`, call `rawGtfsData`+`ParseGtfsData` per feed (thread per-feed auth/tidy through — `rawGtfsData`'s config params become per-feed), resolve default agency, stamp route agencies, `mergeFeeds`, compute combined hash/source (§4.4), validate timezones on the merged result. `updateStaticGTFS`/`ReloadStatic` log the feed count instead of the single URL. |
| `internal/gtfs/merge.go` (new) | `parsedFeed`, `buildStopIndex`, `mergeFeeds`, `checkCrossFeedCollisions` (§4.3) — the check scans per-feed data. In this PR (no consolidation yet) it runs immediately after route stamping; PR 3 inserts consolidation ahead of it (§3.2). |
| `internal/gtfs/merge_test.go` (new) | Table-driven: 2-feed happy path; every collision class errors with both feed sources named (including `block_id` and `agency_id`); route-agency stamping; single-feed passthrough produces identical output to today. |

### PR 3 — Stop consolidation

| File | Change |
|---|---|
| `internal/gtfs/consolidation.go` (new) | Parser + `applyStopConsolidation` (§5), fetching the file via the same local-path-or-URL logic as `rawGtfsData`. Include the consolidation file bytes in the combined hash from PR 2. Reorder the load pipeline so `checkCrossFeedCollisions` runs after consolidation (§3.2), with a test proving a declared shared stop no longer trips the check. |
| `internal/gtfs/consolidation_test.go` (new) | Parser cases (comments, `{{{`, quotes, multi-absorb lines, every §5.4 error); application cases (stop removed, stop_times rewritten, parent_station rewritten, canonical-missing fails, absorbed-missing warns). |
| `internal/restapi/...` | No handler changes. Add one integration test: two small fixture feeds + a consolidation file → assert the §3.3 behaviors through `serveApiAndRetrieveEndpoint`. |

### Test fixtures

`testdata/raba.zip` is the only static fixture. Add a second, tiny,
hand-authored zip (`testdata/consolidation-second-agency.zip` or similar): one
agency, one route, one trip, two stops — one placed at the exact coordinates
of a RABA stop to consolidate against, one distinct. Keep it minimal enough
to read in a zipinfo listing. The RT-mismatch warning in CLAUDE.md ("Test
Data Matching Requirements") doesn't apply — these tests are static-only.

---

## 7. Acceptance checklist

Adapt the Java spec's §7.6 smoke table to Maglev; run against a real
multi-feed config before calling the feature done:

- [ ] Single-feed config (legacy key and CLI flags): identical API responses
      and identical `import_metadata` skip behavior to `main`.
- [ ] N feeds: `agencies-with-coverage` lists all agencies with sane bounds.
- [ ] `route-ids-for-agency/{X}` counts match each agency's route count in
      its source feed (per-agency, not per-zip — a multi-agency zip like
      MTS+SAN contributes to two counts).
- [ ] `stop-ids-for-agency/{X}` is non-empty for every agency; the sum over
      agencies ≥ (stops served by at least one trip − absorbed count)
      (≥, not =, because multi-agency stops are counted once per serving
      agency; trip-less stops are excluded from the right side because
      they belong to no agency's list — §2.2).
- [ ] Consolidated stop: absorbed ID 404s; canonical returns arrivals from
      both agencies; both agencies appear in `references.agencies`.
- [ ] Editing the consolidation file (or any feed) triggers reimport on the
      next refresh; touching nothing skips it.
- [ ] A deliberately broken canonical ID fails startup with an error naming
      the file line; the same error on a periodic reload leaves the old data
      serving.
- [ ] GTFS-RT vehicles/trip updates still attach for a feed with realtime
      configured.
- [ ] Absorbed-agency realtime at a consolidated stop: check whether
      predictions from the absorbed agency's RT feed appear on the canonical
      stop, and record the result — §5.5 documents this as a known
      limitation, so the checklist's job is to measure how much it bites for
      this deployment, not to pass/fail.

---

## 8. Out of scope (deliberately)

- **`agency-id-mappings`** (Java spec §4.4): only needed when two feeds ship
  colliding agency IDs; v1 fails loudly instead. Add later as a per-feed map
  if a real feed pair needs it.
- **Per-feed stop-ID prefixing**: superseded as a concept by §9, which
  namespaces every entity uniformly instead of patching stops one feed at a
  time. Until either lands, collisions between physically distinct stops
  must be renamed upstream.
- **Candidate-detection tooling** for shared stops: upstream pipeline's job
  (Java spec §7.2 keeps it out of the server there, too).
- **Consolidation audit CSV**: structured logs cover it (§5.5).
- **Per-feed refresh intervals**: static refresh stays one 24h cycle for all
  feeds (`internal/gtfs/static.go:162-190`); feeds are fetched together so
  the dataset is always mutually consistent.

---

## 9. v2 proposal: namespaced entity storage (combined IDs in the DB)

Java OBA never has a collision problem because every entity ID there is an
`(agency, id)` tuple. This section proposes Maglev's equivalent — not as
two-column composite primary keys, but as the same tuple **flattened into
the string columns the schema already has**: at import time, store
`MTS_60123` in `stops.id` instead of `60123`, using the feed's resolved
`default-agency-id` as the namespace. Uniformly, for every ID kind that can
collide across feeds: stop, trip, route, service, shape, and block IDs.

This is a proposal, not part of v1. It deletes three problems in one move:

1. **The §4.3 collision policy, entirely.** Two feeds shipping `stop_id=10034`
   yield `MTS_10034` and `NCTD_10034` — distinct rows, the Java spec §8.4
   semantics. `checkCrossFeedCollisions` and the "rename upstream" operator
   contract are removed. (The duplicate-`agency.txt`-ID check stays; the
   namespace itself depends on agency IDs being distinct.)
2. **The prefix nondeterminism in §2.1.** Today a stop's public prefix is
   whatever unordered row `GetAgenciesForStops` returns first. With
   namespaced storage the stored ID *is* the public ID — stable across
   requests, restarts, and reimports.
3. **The deferred per-feed `stop-id-prefix` design.** Namespacing everything
   uniformly subsumes it.

### 9.1 Why flattened strings instead of composite key columns

A true `(agency_id, id)` two-column PK means adding a column to every table,
converting every FK join in `gtfsdb/query.sql` (90+ queries) to two-column
joins, and regenerating every sqlc model. The flattened form has identical
semantics — the tuple is bijectively encoded in the string *provided the
namespace contains no underscore*: the split on the first `_`
(`utils.ExtractAgencyIDAndCodeID`, `internal/utils/api.go:99-105`) decodes
`A_B_C` as `(A, B_C)`, so an agency ID like `A_B` would corrupt the
encoding. The §3.1 validation rule (default-agency-ids must not contain
`_`) is what makes this safe — and the schema, the queries' join
structure, and the transitive agency-ownership queries (§2.1) don't change
shape at all. The string form is also exactly what the OneBusAway API
already exposes, which is what makes §9.2 a simplification rather than a
rewrite.

### 9.2 What changes where

- **Import** (`internal/gtfs/merge.go` territory): after per-feed parsing
  and route-agency stamping, rewrite each feed's IDs in place —
  `stop.Id`, `trip.ID`, `route.Id`, `service.Id`, `shape.ID`, and non-empty
  `trip.BlockID` become `{defaultAgencyID}_{rawID}`. Because cross-entity
  references in `gtfs.Static` are pointers (§4.1), rewriting the ID field on
  the pointed-to struct updates every reference for free — **provided the
  rewrite happens on the per-feed structs before concatenation** (the §4.1
  pointer-aliasing rule; after `append` the pointers no longer target the
  merged copies). Only string-typed references (`BlockID`, and `StopTime`
  fields if any are by-value) need explicit rewriting. `StoreGtfsData` is untouched — it stores whatever IDs
  it's given.
- **Handlers** (the big-but-mechanical part): today every handler splits the
  request's combined ID and queries by the raw half, then re-prefixes on the
  way out with a guessed agency (`utils.FormCombinedID` at hundreds of call
  sites, e.g. `internal/restapi/arrivals_and_departures_for_stop_handler.go`).
  Both halves become deletions: look up by the full request ID as-is; emit
  DB IDs as-is. Net handler code shrinks. Agency-scoped validation queries
  (`GetStopForAgency` etc.) keep working unchanged — they filter on
  `routes.agency_id`, which is independent of ID spelling.
- **GTFS-RT ingest** (the genuinely new work): RT feeds carry raw IDs, and
  Maglev matches them against `trips.id` / stop IDs directly
  (`realTimeTripLookup` is keyed by exact string). Each RT feed entry needs
  a namespace to prefix incoming trip/stop/vehicle IDs with before
  matching. This must be an explicit per-feed field (`id-namespace`, or a
  reference to the originating static feed) — **do not derive it from
  `agency-ids`**: the namespace is the *static feed's* default agency, not
  the RT feed's agency. Counterexample: SAN lives inside the MTS zip
  (§5.2), so all its static IDs are `MTS_*`; an RT source for SAN with
  `agency-ids: ["SAN"]` would prefix `SAN_` and match nothing. This slice
  touches `internal/gtfs/realtime.go`, the most concurrency-sensitive code
  in the repo; it is the riskiest PR and should extend the mock-server RT
  tests.
- **Consolidation** (§5): gets simpler — file tokens like `MTS_60123` now
  match stored IDs byte-for-byte, so `stopKey` and the provenance lookup
  map disappear.

### 9.3 Behavior changes to sign off on

- **Alternate-prefix lookups stop working.** Today `UCSD_60123` resolves for
  a stop that came from the MTS feed, as long as a UCSD route serves it
  (§2.1). Under v2 the stored ID is `MTS_60123` and `UCSD_60123` is a 404 —
  which is exactly the Java server's behavior, so clients migrating from
  Java OBA lose nothing; only clients that grew to depend on Maglev's
  looser matching would notice.
- **The namespace is provenance, not ownership.** A stop in the MTS+SAN zip
  is `MTS_*` even if only SAN routes serve it (same as Java's
  `defaultAgencyId` stamping, §5.2). Ownership queries stay transitive:
  `stop-ids-for-agency/SAN` still returns that stop — just spelled `MTS_*`.
- **Single-feed deployments are wire-compatible.** Public IDs were already
  combined at the API layer, and the prefix for a single-agency feed is the
  same agency the handlers guess today. The DB contents change spelling, so
  one reimport (hash bump) and new ETags — no client-visible ID changes.

### 9.4 Phasing and the decision gate

v1 is not throwaway: config plumbing (PR 1), multi-feed fetch/merge (PR 2),
and consolidation (PR 3) all survive; v2 replaces `checkCrossFeedCollisions`
with the namespacing rewrite and then removes handler-side prefix juggling
in slices (per-endpoint-family PRs, each shrinking code), with the RT
namespace PR last.

**Decision gate:** audit the 38 known MTS×NCTD stop_id collisions (§4.3).
If they are mostly shared physical curbs, the consolidation file absorbs
them, v1 stands on its own for the SDMTS deployment, and v2 is scheduled
cleanup. If they are mostly coincidental numeric overlap, "rename upstream
forever" is a fragile permanent contract — promote v2 to the main plan and
land v1's PRs as its first phase.
