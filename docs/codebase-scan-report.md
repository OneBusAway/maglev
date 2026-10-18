# OneBusAway/Maglev — Comprehensive Local Codebase Scan Report

> Scope: scanning and documentation only. No bug hunting, no defect severities, no fixes, no code
> changes. Every technical claim below is traceable to a file (and line, where useful) in the local
> checkout. Line numbers were read against `main` at `d07c60a8`, and re-verified there: all 220
> `file:line` claims in this document resolve, and none points past the end of its file.

---

## Section A: Repository Overview

### A.1 Checkout information

| Item | Value | Evidence |
|---|---|---|
| Repository root | `/mnt/c/Users/adity/OneDrive/Desktop/Gsoc/Maglov_OBA/maglev` | `git rev-parse --show-toplevel` |
| Scanned against | `main` at `d07c60a8` | `git rev-parse upstream/main` |
| Working tree | **Clean.** `git status --porcelain` produced no output: no modifications, no untracked files | `git status --porcelain=v1` |
| Remotes | `origin` = `https://github.com/dev-aditya-hub/maglev.git`; `upstream` = `https://github.com/OneBusAway/maglev.git` | `git remote -v` |
| Tracked files | 411 | `git ls-files \| wc -l` |

The parent directory (`…/Maglov_OBA/`) also contains `java-oba/` and a `.claude/` directory. **Neither is
part of this git repository** and neither was scanned — see Section M (Exclusions).

### A.2 Languages, frameworks, runtimes

| Aspect | Finding | Evidence |
|---|---|---|
| Language | Go, exclusively for application code | 329 `.go` files; no other compiled language present |
| Module path | `maglev.onebusaway.org` | `go.mod:1` |
| Go directive | `go 1.26.0` | `go.mod:3` |
| HTTP framework | **None.** Standard library `net/http` + `http.ServeMux` with Go 1.22+ method/wildcard patterns (`"GET /api/where/stop/{id}"`) | `cmd/api/app.go:176`, `internal/restapi/routes.go:71-122` |
| Database | SQLite only. Two interchangeable drivers selected by build tag | `gtfsdb/driver_cgo.go`, `gtfsdb/driver_pure.go` |
| Query layer | `sqlc` v1.30.0 code generation + three hand-written query files | `gtfsdb/sqlc.yml`, `gtfsdb/db.go:1-3` |
| Other languages present | JavaScript (k6 load tests, `loadtest/k6/*.js`), SQL (`gtfsdb/*.sql`), Bash (`scripts/*.sh`), HTML (`index.html`, `internal/webui/debug_index.html`), YAML (CI, OpenAPI spec) |
| Build system | GNU Make wrapper over the `go` toolchain | `Makefile` |

### A.3 Code size

Non-test Go: **36,123 lines across 150 files.** Test Go: **55,758 lines across 179 files.**

| Directory | Non-test LOC | Role |
|---|---|---|
| `internal/restapi` | 17,352 | HTTP handlers, middleware, response assembly |
| `gtfsdb` | 9,634 | SQLite schema, sqlc-generated queries, import pipeline |
| `internal/gtfs` | 4,453 | GTFS static + GTFS-RT manager, direction calculation, metrics |
| `internal/models` | 1,274 | API response models |
| `internal/utils` | 1,236 | Shared parsing, validation, geometry, sorting |
| `cmd/api` | 567 | Entry point, wiring, lifecycle |
| `internal/appconf` | 517 | Configuration |
| `internal/metrics` | 284 | Prometheus registry |
| `internal/logging` | 219 | slog helpers |
| `internal/restapi/testdata` | 213 | RABA fixture constants (package `testdata`, not `_test.go`) |
| `internal/webui` | 211 | Landing page, marketing assets, debug page |
| `internal/clock` | 168 | Time abstraction |
| `internal/nulls` | 88 | `sql.Null*` helpers |
| `internal/servicedate` | 59 | GTFS service-date value object |
| `internal/app` | 46 | DI container |
| `internal/buildinfo` | 15 | ldflags-injected build metadata |

Of `gtfsdb`'s 9,634 lines, **7,243 are sqlc-generated** (`query.sql.go` 5,672 + `db.go` 1,358 +
`models.go` 213), leaving ~2,400 hand-written lines.

### A.4 Important manifests and entry points

| File | Purpose |
|---|---|
| `go.mod` / `go.sum` | Dependency manifest + lockfile; also declares two `tool` directives (`sqlc`, `gtfstidy`) at `go.mod:87-89` |
| `cmd/api/main.go:18` | **The single `main()`.** Only executable target in the repo |
| `Makefile` | All developer commands |
| `gtfsdb/schema.sql` | DDL, applied at runtime by splitting on `-- migrate` |
| `gtfsdb/query.sql` | 128 named sqlc queries |
| `gtfsdb/sqlc.yml` | Codegen config (`engine: sqlite`, `emit_prepared_queries: true`) |
| `config.schema.json` | JSON Schema for `config.json` (IDE validation; `additionalProperties: false`) |
| `config.example.json`, `config.docker.example.json` | Config templates |
| `testdata/openapi.yml` | 2,776-line vendored copy of the upstream OBA OpenAPI spec (header says fetched 2026-08-27) |
| `CLAUDE.md`, `CONTRIBUTING.md`, `README.markdown` | Developer and agent instructions |
| `.claude/skills/*/SKILL.md` | 5 repo-local agent skills (oba-api-review, oba-api-verify, oba-api-spec-check, oba-api-client-impact, oba-workspace) |

### A.5 Initial environment assessment

See Section L for detail. Headline: Go 1.26.0 is present and the module cache is warm, but **there is
no C compiler**, so the project's default CGO build/test mode cannot run here; the pure-Go mode can
and does.

---

## Section B: Directory and File Inventory

Statuses: **Inspected** = implementation read; **Partially inspected** = declarations, doc comments and
principal flow read, some function bodies not read line-by-line; **Excluded** = deliberately not read
as source, with reason.

### B.1 Top level

| Path | Priority | Purpose | Status |
|---|---|---|---|
| `cmd/api/` | P1 | Entry point: flag/config parsing, DI wiring, server construction, graceful shutdown | Inspected (`main.go`, `app.go` in full) |
| `internal/app/` | P1 | `Application` DI struct + API-key check | Inspected |
| `internal/appconf/` | P2 | JSON/CLI config structs, defaults, validation, env overrides | Inspected |
| `internal/gtfs/` | P1 | GTFS static loader, GTFS-RT pollers, merged realtime snapshot, direction calculator, metrics snapshot | Inspected (`gtfs_manager.go`, `realtime.go`, `static.go`, `config.go`, `shapes.go`, `location_params.go`, `route_search.go` in full); Partially inspected (`advanced_direction_calculator.go`, `direction_precomputer.go`, `metrics.go`, `tidy.go`, `gtfs_manager_mock.go`) |
| `internal/restapi/` | P1/P2 | 27 route registrations, 13 middleware, handlers, shared arrival/trip/reference helpers | Inspected (all middleware, envelopes, helpers, small/medium handlers, and the primary handler function of every large handler); Partially inspected (the ~40 private sub-helpers of the four largest handler files) |
| `internal/models/` | P1 | API response models and constructors | Inspected (all 27 files) |
| `internal/utils/` | P4 | Parse/validate/geometry/sort/polyline/batching helpers | Inspected (all 12 files) |
| `internal/nulls/`, `internal/clock/`, `internal/servicedate/`, `internal/logging/`, `internal/metrics/`, `internal/buildinfo/` | P2/P4 | Small focused support packages | Inspected |
| `internal/webui/` | P4 | `/`, `/debug/`, `/marketing/{file}` handlers | Inspected |
| `gtfsdb/` | P1 | SQLite layer: schema, import, queries | Inspected (`client.go`, `config.go`, `helpers.go`, `schema.sql`, `fts_queries.go`, `stops_rtree.go`, `debugging.go`, driver/constraint variants, `models.go`); query name inventory extracted from `query.sql` |
| `docs/` | P4 | 2 forward-looking specs + 1 profiling note + agent instructions | Inspected (README/AGENTS/mutex note in full; the two 800–900-line specs read at section-heading + "how it works today" depth) |
| `loadtest/` | P3 | k6 scenarios, thresholds, CSV fixtures, README | Inspected (`thresholds.js`, `scenarios.js` head, README size); Partially inspected (`hotswap_scenario.js`, `smoke.js`) |
| `scripts/` | P4 | OpenAPI sync, perf-data download, hotswap memory test | Inspected (`check-openapi.sh`); Partially inspected (`update-openapi.sh`, `download-perf-data.sh`, `hotswap-memory-test.sh` — sizes and purpose only) |
| `testdata/` | P3 | RABA GTFS zip, 4 protobuf RT fixtures, 4 config fixtures, OpenAPI spec | Inventoried; binary fixtures **Excluded** from content inspection |
| `.github/` | P3 | 5 workflows + dependabot + PR template + SECURITY.md | Inspected (all 5 workflows, dependabot) |
| `.claude/` | P4 | Repo-local agent skills + settings | Inventoried (file list and sizes); skill bodies **Partially inspected** |
| `bin/`, `configs/`, `migrations/`, `remote/` | — | **Empty placeholders**: each contains only `.gitkeep` | Inspected (confirmed empty) |
| `marketing/` | P4 | Single PNG header image | Inventoried |
| `.idea/`, `.run/` | — | JetBrains IDE config | **Excluded** (IDE metadata, no runtime role) |

### B.2 Exclusions and reasons

| Excluded | Reason |
|---|---|
| `gtfsdb/query.sql.go` (5,672 L), `gtfsdb/db.go` (1,358 L), `gtfsdb/models.go` (213 L) | `// Code generated by sqlc. DO NOT EDIT.` — `models.go` was read in full (it is the data-model contract); `db.go` was read for its `DBTX` interface and prepared-statement pattern; `query.sql.go` bodies were not read line-by-line. `query.sql` is the source of truth and its 128 query names were extracted. CI excludes all three from coverage math (`.github/workflows/go.yml`, "Enforce Coverage Threshold" step) |
| `testdata/raba.zip`, `testdata/gtfs.zip`, `testdata/*.pb` | Binary fixtures (GTFS zip, GTFS-RT protobuf) |
| `marketing/maglev-header.png` | Binary image |
| `.idea/*`, `.run/*` | IDE configuration, no runtime effect |
| `go.sum` | Lockfile hashes; `go.mod` carries the semantic dependency information |
| `java-oba/` (sibling directory outside the repo) | Not part of this git repository |

---

## Section C: Architecture Map

### C.1 Major components and responsibilities

| Component | Location | Responsibility |
|---|---|---|
| **Entry point / wiring** | `cmd/api/main.go`, `cmd/api/app.go` | Parse flags or `-f config.json` through one shared validation path; build `Application`; compose the middleware stack; run and shut down |
| **DI container** | `internal/app/application.go:12` | `Application{Config, GtfsConfig, GtfsManager, DirectionCalculator, Clock, Metrics}` — every handler embeds `*app.Application` via `RestAPI` |
| **Configuration** | `internal/appconf` | `JSONConfig` (wire format) → `setDefaults()` → env overrides → `Validate()` → `Config` + `GtfsConfigData`. `GtfsConfigData` exists specifically to avoid an import cycle with `internal/gtfs` (`json_config.go:287-288`) |
| **GTFS Manager** | `internal/gtfs/gtfs_manager.go:42` | Owns the DB client, the per-feed realtime maps, the published realtime snapshot, region bounds, and all background goroutines |
| **Static loader** | `internal/gtfs/static.go` | Download/read → optional `gtfstidy` → parse → validate timezones → import → precompute directions → recompute region bounds |
| **Realtime pollers** | `internal/gtfs/realtime.go` | One goroutine per enabled feed; fetch 3 sub-feeds in parallel; agency-filter; update per-feed maps; rebuild and atomically publish the merged snapshot |
| **Database layer** | `gtfsdb` | Schema migration, pooling, PRAGMAs, the transactional import, and 128 typed queries (+3 hand-written: FTS5 routes, FTS5 stops, R-Tree bounds) |
| **REST API** | `internal/restapi` | 27 registered routes, 13 middleware, and the shared arrival/trip-status/reference engines |
| **Models** | `internal/models` | OBA-shaped response structs with custom `ModelTime`/`ModelDuration` JSON marshalling |
| **Web UI** | `internal/webui` | Landing page, static marketing assets, and a non-production debug data dump |
| **Metrics** | `internal/metrics` | A private Prometheus registry served at `GET /metrics` with no authentication |

### C.2 Text architecture diagram

```
                          ┌───────────────────────────────┐
  flags  ─or─  -f cfg ───▶│ appconf.JSONConfig            │
                          │  setDefaults → env → Validate │
                          └───────┬───────────────┬───────┘
                       ToAppConfig│               │ToGtfsConfigData
                                  ▼               ▼
                        appconf.Config      gtfs.Config
                                  │               │
                                  └──────┬────────┘
                                         ▼
                             cmd/api.BuildApplication
                                         │
        ┌────────────────────────────────┼──────────────────────────────┐
        ▼                                ▼                              ▼
 metrics.Metrics            gtfs.InitGTFSManager            clock.Clock (Real | Environment)
 (private registry)                      │                   (Test env ⇒ FAKETIME file/env)
        │                 ┌──────────────┼─────────────────┐
        │                 ▼              ▼                 ▼
        │        gtfsdb.Client   AdvancedDirection   background goroutines
        │        (SQLite+WAL)      Calculator        ├─ updateStaticGTFS (24h ticker)
        │                 │                          └─ pollFeed × N (per RT feed)
        │                 │                                       │
        │                 ▼                                       ▼
        │     ┌──────────────────────┐              ┌──────────────────────────┐
        │     │ SQLite (file or      │              │ per-feed maps            │
        │     │ :memory:)            │              │  feedTrips/Vehicles/     │
        │     │ 17 tables + FTS5 ×2  │              │  Alerts/LastSeen/…       │
        │     │ + R-Tree + triggers  │              │        │ (realTimeMutex) │
        │     └──────────┬───────────┘              │        ▼                 │
        │                │                          │ rebuildMergedRealtime    │
        │                │                          │        │                 │
        │                │                          │  atomic.Pointer          │
        │                │                          │   [mergedRealtime]       │
        │                │                          └────────┬─────────────────┘
        │                │                                   │ lock-free reads
        │                └───────────────┬───────────────────┘
        │                                ▼
        │                   internal/restapi (RestAPI embeds *app.Application)
        │                                │
        │     ┌──────────────────────────┴──────────────────────────────┐
        │     │ shared engines:                                          │
        │     │  arrivals_core.go     — single & multi-stop arrivals      │
        │     │  trips_helper.go      — BuildTripStatus / BuildTripSchedule│
        │     │  scheduled_block_helper.go — block snapshot + snapshot cache│
        │     │  trip_updates_helper.go — GTFS-RT delay resolution        │
        │     │  reference_utils.go   — references{} + situations         │
        │     │  vehicles_helper.go   — status/phase, staleness, projection│
        │     └──────────────────────────┬──────────────────────────────┘
        │                                ▼
        │                      27 handlers → models.* envelopes
        │                                │
        └────────────────────────────────┼─────── GET /metrics (promhttp, no auth)
                                         ▼
    HTTP middleware (outermost → innermost, cmd/api/app.go:190-214):
      Recovery → SizeLimit(1MiB) → RequestID → RequestLogging → Metrics
        → SecurityHeaders → Freshness → Compression → VersionValidation
          → GtfsExpiry → ServeMux
    Per-route (routes.go): CacheControl → [API-key | protected-key] → RateLimit → [ETag] → handler

    webui: GET /  ·  GET /debug/  ·  GET /marketing/{file}
```

### C.3 Package dependency graph (non-test imports, verified)

```
internal/appconf      → (nothing internal)        ── leaf
internal/clock        → (nothing internal)        ── leaf
internal/logging      → (nothing internal)        ── leaf
internal/metrics      → (nothing internal)        ── leaf
internal/nulls        → (nothing internal)        ── leaf
internal/servicedate  → (nothing internal)        ── leaf
internal/buildinfo    → (nothing internal)        ── leaf

gtfsdb                → appconf, logging, nulls
internal/models       → gtfsdb, clock, nulls, servicedate
internal/utils        → gtfsdb, clock, models, nulls
internal/gtfs         → gtfsdb, appconf, logging, metrics, models, nulls, utils
internal/app          → appconf, clock, gtfs, metrics
internal/restapi      → gtfsdb, app, buildinfo, clock, gtfs, logging, metrics,
                        models, nulls, servicedate, utils
internal/webui        → app, appconf
cmd/api               → app, appconf, clock, gtfs, logging, metrics, restapi, webui
```

The graph is acyclic. Two deliberate decouplings are documented in-code: `appconf.GtfsConfigData`
exists so `appconf` need not import `gtfs` (`internal/appconf/json_config.go:287-288`), and
`gtfsdb.DBQueryMetricsRecorder` is a one-method interface so `gtfsdb` need not import
`internal/metrics` (`gtfsdb/config.go:24-28`).

### C.4 External systems referenced by code or configuration

| External system | Direction | Where |
|---|---|---|
| Static GTFS zip over HTTP(S) or local file | outbound fetch | `internal/gtfs/static.go:18-67`; 5-minute client timeout, 200 MiB body cap |
| GTFS-RT trip updates / vehicle positions / service alerts | outbound fetch, per feed | `internal/gtfs/realtime.go:306-340`; 10 s client timeout, 25 MiB body cap |
| `gtfstidy` subprocess | outbound exec, opt-in | `internal/gtfs/tidy.go` (enabled by `gtfs-static-feed.enable-gtfs-tidy`) |
| Prometheus scraper | inbound | `GET /metrics` (`cmd/api/app.go:182`) |
| pprof | inbound, loopback only | `127.0.0.1:6060` when `MAGLEV_ENABLE_PPROF=1` (`cmd/api/main.go:27-36`) |
| `raw.githubusercontent.com/OneBusAway/sdk-config` | outbound, CI/dev tooling only | `scripts/check-openapi.sh`, `scripts/update-openapi.sh` |
| ghcr.io / Docker Hub | CI publish | `.github/workflows/docker-publish.yml`, `docker-release.yml` |
| SonarQube | CI, currently gated off | `.github/workflows/go.yml` (`SONAR_CI_ANALYSIS_ENABLED: false`) |

### C.5 Startup, shutdown, background execution

**Startup** (`cmd/api/main.go:18` → `cmd/api/app.go:113`):
1. `signal.NotifyContext(SIGINT, SIGTERM)`; optional pprof goroutine; optional mutex/block profiling.
2. Config resolved by exactly one of two paths, both ending in `JSONConfig.Validate()`.
3. `--dump-config` prints a redacted config and exits 0 (`app.go:299-370`).
4. Logger built from `log-level`/`log-format`, installed as `slog` default.
5. `gtfs.InitGTFSManager` (`internal/gtfs/gtfs_manager.go:117`):
   `openGtfsDB` → `ReloadStatic` with retry backoffs `5s,15s,30s,60s` (a local file gets 1 attempt only,
   `gtfs_manager.go:128-130`) → `PrintStatistics` → validate each feed's configured `agency-ids`
   against `ListAgencyIds` (warn only) → **synchronous first fetch per enabled feed, 15 s timeout each**
   → `MarkReady()` → start `updateStaticGTFS` (skipped for local-file sources) → start one `pollFeed`
   goroutine per enabled feed.
6. `AdvancedDirectionCalculator` constructed and registered on the manager so `ReloadStatic` can evict
   its cache (`app.go:122-128`).
7. `Metrics.StartDBStatsCollector(provider, 15s)` — takes a `func() *sql.DB` provider rather than the
   handle, explicitly so metrics survive a DB swap (`app.go:142-150`, `internal/metrics/metrics.go:255-261`).
8. `CreateServer` then `Run`.

**Shutdown** (`cmd/api/app.go:233-297`), strictly ordered:
`srv.Shutdown(30s)` → `api.Shutdown()` → `Metrics.Shutdown()` (blocks on its WaitGroup) →
`GtfsManager.Shutdown(30s)`. The manager's shutdown is `sync.Once`-guarded, closes `shutdownChan`,
waits for the WaitGroup in a goroutine, then closes the DB — and if the context expires first it
returns the context error and **leaves the DB open for the still-running workers**
(`internal/gtfs/gtfs_manager.go:251-283`).

**Background goroutines**: `updateStaticGTFS` (24 h ticker, 5 min per-reload timeout);
`pollFeed` × N (timer-driven, exponential backoff ×2..×32 capped at 5 min with ±10 % jitter,
`realtime.go:958-977`); the DB-stats collector; and the optional pprof server.

---

## Section D: Execution-Flow Map

### D.1 Flow 1 — Static GTFS ingestion (async, no request behind it)

| # | Step | Symbol / location |
|---|---|---|
| 1 | Entry: startup, or the 24 h ticker | `InitGTFSManager` (`gtfs_manager.go:154`) / `updateStaticGTFS` (`static.go:162`) |
| 2 | Serialise concurrent reloads | `ReloadStatic` takes `staticUpdateMutex` (`static.go:197`) |
| 3 | Fetch bytes | `rawGtfsData` (`static.go:18`) — local file via `os.ReadFile`, else HTTP with optional static auth header, 200 MiB `io.LimitReader` cap |
| 4 | Optional normalisation | `tidyGTFSData` (`tidy.go:53`); on failure logs and **keeps the original bytes** (`static.go:73-77`) |
| 5 | Hash + parse + structural validation | `gtfsdb.ParseGtfsData` (`helpers.go:38`): sha256 → `gtfs.ParseStatic` → `ValidateAndFilterGTFSData` |
| 6 | Timezone validation | `validateStaticAgencyTimezones` (`static.go:145`) — empty or unloadable `agency_timezone` fails the whole reload; the trimmed value is written back |
| 7 | Import | `Client.StoreGtfsData` (`helpers.go:205`) — see Section E.2 |
| 8 | If changed: precompute stop directions | `DirectionPrecomputer.PrecomputeAllDirections` (`static.go:109-115`) — **runs after the import transaction commits**, and a failure is logged but does not fail the import |
| 9 | Recompute per-agency region bounds | `computeRegionBounds` (`shapes.go:17`) → stored under `staticMutex` (`static.go:223-228`) |
| 10 | If changed: evict direction cache | `DirectionCalculator.ClearCache()` (`static.go:232-234`) |
| 11 | Report feed expiry | `logFeedExpiry` (`static.go:255`) sets the `maglev_gtfs_feed_expires_at` gauge and warns at ≤7/≤3/≤1/overdue days |

Intermediate structures: `gtfsdb.GtfsData{Static *gtfs.Static, Hash, Source}`.
Associated tests: `internal/gtfs/static_test.go`, `reload_test.go`, `static_timezone_validation_test.go`,
`gtfsdb/conditional_import_test.go`, `validation_test.go`.

### D.2 Flow 2 — GTFS-RT polling (async, one goroutine per feed)

| # | Step | Symbol / location |
|---|---|---|
| 1 | Entry | `pollFeed` (`realtime.go:981`); first poll happens **after** one interval, because the warm fetch already ran at startup (`realtime.go:1006`) |
| 2 | Per-poll context | `context.WithTimeout(context.Background(), 15*time.Second)`, logger attached (`realtime.go:1019-1021`) |
| 3 | Parallel sub-feed fetch | `updateFeedRealtime` (`realtime.go:345`) — a goroutine per configured URL; each calls `loadRealtimeData` → `gtfs.ParseRealtime` |
| 4 | Agency filter (only when `agency-ids` set) | `collectRealtimeRouteIDs` → `buildRouteAgencyMap` (batched `GetRoutesByIDs`) → `tripAgencyIDs` (falls back to the static trip's route when GTFS-RT omits `route_id`) → `filterTripsByAgency` / `filterVehiclesByAgency` / `filterAlertsByAgency`. Runs **before** the lock is taken (`realtime.go:400-427`) |
| 5 | Critical section | `realTimeMutex.Lock()` (`realtime.go:429`) |
| 6 | Trips | stored verbatim; agency-unresolvable trips kept separately in `feedUnattributedTrips` for the metrics endpoint |
| 7 | Vehicles | feed-level freshness via `vehicleData.CreatedAt` (a zero value means "always apply", `realtime.go:446-448`); per-vehicle freshness via `isVehicleStale`; vehicles absent from this fetch are retained until `staleVehicleTimeout` (15 min) elapses (`realtime.go:522-534`) |
| 8 | Alerts | stored verbatim |
| 9 | Publish | `rebuildMergedRealtimeLocked` (`realtime.go:832`) builds a brand-new `mergedRealtime` and `atomic.Pointer.Store`s it |
| 10 | Success/backoff | on `hasNewData` reset the interval and the error counter; otherwise exponential backoff, and after `staleFeedThreshold` (5 min) of failure `clearFeedData` wipes that feed once per outage (`realtime.go:1044-1071`) |

`hasNewData` is an **OR** over the configured sub-feeds: one succeeding sub-feed counts as a successful
poll, and partial success is logged at Warn (`realtime.go:552-620`).

Associated tests: `internal/gtfs/realtime_test.go`, `realtime_snapshot_test.go`, `parallel_realtime_test.go`,
`multi_feed_test.go`, `agency_filter_test.go`, `realtime_benchmark_test.go`.

### D.3 Flow 3 — A generic authenticated API request

```
client
 └─ Recovery (recover → 500 JSON if nothing written yet)
    └─ SizeLimitMiddleware(1<<20)            MaxBytesReader on the body
       └─ RequestIDMiddleware                X-Request-ID validated by ^[a-zA-Z0-9-._:]+$, ≤128, else uuid
          └─ RequestLoggingMiddleware        puts the request logger into ctx; logs method/path/status/ms
             └─ MetricsHandler               labels by r.Pattern (falls back to "unmatched")
                └─ WithSecurityHeaders       nosniff, DENY, HSTS, XSS, Referrer-Policy, CSP, CORS *, OPTIONS→200
                   └─ FreshnessMiddleware    X-Data-Last-Updated (RFC3339)
                      └─ CompressionMiddleware  gzhttp, MinSize 1024, level 6
                         └─ VersionValidationMiddleware  /api/* : ?version must be "" or "2"
                            └─ GtfsExpiryMiddleware      /api/* : X-Data-Expired: true when expired
                               └─ ServeMux
                                  └─ CacheControlMiddleware(Long 300 | Short 30 | None 0)
                                     └─ API-key gate  (?key= vs ApiKeys, constant-time)
                                        │   or protected gate (vs ProtectedApiKeys)
                                        └─ RateLimit (global bucket; exempt keys bypass)
                                           └─ [ETagMiddleware on static endpoints]
                                              └─ handler
```

Handler-internal shape, consistent across the suite:
`ShouldIncludeReferences` → `extractAndValidateID` / `extractAndValidateAgencyCodeID` → parse params
(`utils.Parse*`) → `validationErrorResponse` on any field error → DB/manager reads → build entry →
build `references` when requested → `models.New{Entry,List}Response` → `api.sendResponse`.

### D.4 Flow 4 — `arrivals-and-departures-for-stop/{id}` (the hottest real path)

Entry `internal/restapi/arrivals_and_departures_for_stop_handler.go:54`.

1. `extractAndValidateAgencyCodeID` splits `{agency}_{stopCode}` on the first `_`.
2. **`ctx = WithSnapshotCache(r.Context(), newSnapshotCache())`** — request-scoped memoisation of block
   snapshots; without it `BuildTripStatus` recomputes the block for every arrival row (`:66`).
3. `parseArrivalsAndDeparturesParams` — `minutesAfter` default 35, `minutesBefore` default 5, each
   capped at 24 h; `time` as epoch millis (`:24-52`).
4. `GetStop`, `GetAgency` (`sql.ErrNoRows` → 404, other errors → 500), `loadAgencyLocation`; the query
   time is converted into the agency's zone.
5. `arrivalsForStop` (`arrivals_core.go:92`):
   - `activeStopTimesForWindow` → `discoverStopAgencies` (`GetRoutesForStop` + per-agency timezone
     resolution) → `groupAgenciesByTimezone` → for each timezone group, for `dayOffset ∈ {-1,0,+1}`:
     `GetActiveServiceIDsForDate` + `GetStopTimesForStopInWindow`. **Day 0 failure is fatal; ±1-day
     failures are logged and skipped** (`arrivals_core.go:306-327`). Results are stable-sorted by
     absolute scheduled arrival, then trip ID, then stop sequence.
   - `batchArrivalEntities` — 4 batched queries: `GetRoutesByIDs`, `GetTripsByIDs`,
     `GetStopTimesForTripIDs` (for stop counts), `GetFrequenciesForTrips`. A frequency failure is fatal;
     a stop-count failure degrades one field (`arrivals_core.go:696-780`).
   - per row: `buildArrival` → `GetVehicleForTrip` → `getPredictedTimes` → `tripStatusForArrival`
     (`BuildTripStatus` + `snapshot.metricsForStop`) → `models.NewArrivalAndDeparture` → `applyFrequency`.
6. If nothing matched the window, the bare envelope is returned without any reference work (`:123-127`).
7. `buildArrivalsReferences` — trips, stops (+ parent stations), routes, agencies.
8. `GetAlertsForStop` adds stop-level situations; `getNearbyStopIDs` runs a 100 m uncapped bounds query
   and resolves each nearby stop's own agency via `GetAgenciesForStops` (`:154-206`).
9. `models.NewArrivalsAndDepartureResponse`.

### D.5 Flow 5 — `BuildTripStatus` (the shared real-time engine)

`internal/restapi/trips_helper.go:50`. Called by arrivals (×1 per row), trip-details, trip-for-vehicle,
trips-for-route, trips-for-location, and (field-selectively) vehicles-for-agency.

1. Resolve the vehicle if the caller did not supply one; normalise `serviceDate` to midnight.
2. `situationRefsForTrip` → `status.SituationIDs`.
3. `calculateBlockTripSequence` up front, so it survives the CANCELED early return (`:74-77`).
4. `BuildVehicleStatus` (`vehicles_helper.go:107`) — staleness gate (15 min, `StaleDetector`), raw GPS
   into `LastKnownLocation`/`Position`, GTFS bearing → OBA orientation (`90 − bearing`, wrapped),
   status/phase from the trip's schedule relationship, `ActiveTripID`.
5. Resolve `dbTripID`: normally the status's active trip, falling back to `tripID` when that ID has no
   DB row (the DUPLICATED-trip case) (`:107-122`).
6. `frequencyForEntry` — window-matched frequency row.
7. **CANCELED short-circuits here** (`:136-140`).
8. `blockShiftTripIDsSortedByStartTime` → `GetScheduleDeviationForBlock`
   (`trip_updates_helper.go:40`): last trip-level delay wins; a |delay| > 1 h discards the record
   entirely (Java's `blockNotActive`); otherwise closest-in-time StopTimeUpdate; otherwise the first
   available StopTimeUpdate delay.
9. `SetPredicted(freshVehicle || hasRealtimeTripUpdate)`.
10. Closest/next stop by one of four strategies, in order of available data: explicit `vehicle.StopID`;
    `CurrentStopSequence`; schedule-deviation search; or (no live position) per-stop delays from
    TripUpdates. `fillStopsFromSchedule` backfills whatever is still empty.
11. `computeScheduledBlockSnapshot` at `currentTime − scheduleDeviation`
    (`scheduled_block_helper.go:144`) — 4 queries uncached, 0 on a sibling cache hit.
12. Snapshot-derived fields, gated on `snap.InRange`: `ActiveTripID`, `ScheduledDistanceAlongTrip`,
    `TotalDistanceAlongTrip`, and a two-tier `DistanceAlongTrip` (GPS projection when the snapshot's
    active trip is the vehicle's own declared trip and the vehicle is fresh, else the schedule value).
13. Target-shape work: projected `Position`, inferred `Orientation`, or interpolated schedule position.
14. Returns `(*models.TripStatus, *tripStatusExtras{snapshot, situations}, error)` so callers reuse both.

### D.6 Other mapped flows

| Flow | Entry | Notes |
|---|---|---|
| `arrivals-and-departures-for-location` | `arrivals_and_departures_for_location_handler.go:37` | Uncapped clamped bounds query → `agenciesForStops` → sort by distance → `arrivalsForStops` (batched multi-stop variant) → nearby stops in an expanded box → `truncateLocationLists` applies one `maxCount` to all three lists → references built **only** for retained entries (`retainedAccumulator`) |
| `trips-for-route/{id}` | `trips_for_route_handler.go:31` | Per-timezone service-day resolution; block selection per service day; interlined-entry trip resolution; DUPLICATED trips appended from realtime with numeric-suffix stripping. Deliberately returns trips of other routes sharing a block (`:178-182`) |
| `trips-for-location` | `trips_for_location_handler.go:22` | Realtime vehicles in bounds ∪ scheduled trips in bounds (block and block-less paths), then `buildTripsForLocationEntries` |
| `trip-details/{id}` | `trip_details_handler.go:147` | Two-phase param parse (syntax first, then localized once the agency timezone is known); explicit `serviceDate` must be active or 404; `vehicleId` must be on this trip or 404; Extension 4e nils an untracked status |
| `schedule-for-stop/{id}` | `schedule_for_stop_handler.go:24` | `GetScheduleForStopOnDate` → group by route→direction → plurality headsign vote → `exact_times` frequency expansion → sorted output |
| `stops-for-route/{id}` | `stops_for_route_handler.go:19` | Optional service-date filter (only when `time` is given) → direction groups → `OrderStopsAlongRoute` topological sort → polyline merge |
| `search/stop.json`, `search/route.json` | `search_stops_handler.go:68`, `route_search_handler.go:13` | FTS5 with sanitisation, quoted-prefix terms, and a non-wildcard retry on FTS5 syntax errors |
| `vehicles-for-agency/{id}` | `vehicles_for_agency_handler.go:19` | Builds most of `TripStatus` itself and copies only stop/distance/deviation/predicted from the shared builder (`fillTripStatusFromSharedBuilder`, documented divergence at `:339-356`) |
| `report-problem-with-{trip,stop}/{id}` | `report_problem_with_*_handler.go` | The only write path reachable from HTTP: `CreateProblemReportTrip/Stop` INSERTs; comments truncated to 500 runes |
| `/healthz` | `health_handler.go:28` | Unauthenticated. Liveness → readiness (`IsReady`) → `DB.PingContext` → optional `?verbose=true` freshness block |

---

## Section E: Data-Flow and Data-Model Map

### E.1 Unit and timezone conventions (established in code)

| Quantity | Representation | Evidence |
|---|---|---|
| `stop_times.arrival_time` / `departure_time` | **int64 nanoseconds since service-day midnight** (a `time.Duration` cast) | `gtfsdb/helpers.go:416-417`; `schema.sql:348-349` |
| `frequencies.start_time` / `end_time` | int64 nanoseconds since midnight | `schema.sql:373-374`, `helpers.go:440-441` |
| `block_layover.layover_start` / `_end` | int64 nanoseconds since service-day midnight | `schema.sql:471-472` |
| `calendar.start_date`, `end_date`, `calendar_dates.date` | TEXT `YYYYMMDD` | `schema.sql:300-301`, `helpers.go:372-373` |
| `import_metadata.import_time` | int64 Unix **nanoseconds** | `helpers.go:477`, read back by `time.Unix(0, …).UTC()` (`gtfs_manager.go:873`) |
| `import_metadata.feed_expires_at` | int64 Unix **seconds**, = end date 23:59:59 | `helpers.go:607-609` |
| `problem_reports_*.created_at` / `submitted_at` | int64 Unix **milliseconds** | `report_problem_with_trip_handler.go:65` |
| API times (`ModelTime`) | Unix milliseconds; zero time marshals as `0` | `internal/models/times.go:19-24` |
| API durations (`ModelDuration`) | **seconds** | `internal/models/times.go:48-50` |
| Service-day base | `servicedate.Date.Start(loc)` = local noon − 12 h (DST-safe); `Midnight(loc)` is what responses report | `internal/servicedate/servicedate.go:37-44` |
| Seconds-since-service-date | **wall-clock**, not elapsed, so GTFS offsets stay aligned across DST | `utils.CalculateSecondsSinceServiceDate` (`utils/api.go:49-64`) |
| Distances | metres; `RadiusOfEarthInMeters = 6371010.0`; equirectangular fast path under 0.2° | `utils/geometry.go:5-55` |
| Orientation | OBA convention, 0° = East, 90° = North (`90 − GTFS bearing`, wrapped) | `vehicles_helper.go:136-143`, `trips_helper.go:1285-1294` |

### E.2 Static import: feed → SQLite (one transaction)

`gtfsdb.Client.StoreGtfsData` (`helpers.go:205-564`), in order:

1. `GetImportMetadata`; **if `file_hash` AND `file_source` both match, return `(false, nil)` and skip
   everything** (`:221-229`).
2. `BeginTx`, `defer SafeRollbackWithLogging`.
3. If prior metadata existed, `clearAllGTFSDataWithQueries` deletes 13 tables in FK-safe order (`:620-661`).
4. `agencies` one-by-one; `routes` one-by-one with `singleAgencyID` fallback when a route omits
   `agency_id` **and** the feed has exactly one agency (`:284-292`).
5. `stops` — rows with a nil latitude or longitude are **skipped entirely**, with a documented rationale
   referencing PR #209 (`:312-325`); `direction` is left NULL for later precomputation.
6. `calendar`; `trips` (bulk); `stop_times` (bulk, parallel-prepared); `frequencies`; `shapes` (bulk).
7. `UpsertImportMetadata` (hash, `time.Now().UnixNano()`, source).
8. `calendar_dates` from `AddedDates` (type 1) and `RemovedDates` (type 2).
9. `buildBlockTripIndex` (`:1242`) — groups trips by `(service_id, first stop_id)`, sorts by
   `(block_id, trip_id)`, writes `block_trip_index` + `block_trip_entry`.
10. `BulkUpdateTripTimeBounds` — fills `trips.min_arrival_time` / `max_departure_time`.
11. `buildStopAgencyIndex` — `ClearStopAgencies` + `BuildStopAgencies`.
12. `buildBlockLayoverIndex` (`:1352`) — one row per consecutive same-terminal pair in a block;
    window is `[prev departure, next arrival]`; inverted pairs are skipped to protect the CHECK.
13. `updateFeedExpiresAtFromCalendar` — `GetFeedEndDate` → `feed_expires_at`.
14. `tx.Commit()`, then `TableCounts()` for logging.

`ValidateAndFilterGTFSData` (`helpers.go:1441`) runs **before** the import: hard-fails on empty
agencies/routes/stops/trips or no service at all; clears `parent_station` references to missing stops;
drops trips with no route, no service, no stop_times, or a stop_time naming a missing stop; hard-fails
if that filtering removed every trip.

### E.3 Relational schema (17 tables + 3 virtual + 3 R-Tree shadow tables)

All base tables are `STRICT` with CHECK constraints. `gtfsdb/schema.sql`.

| Table | Key | Notable columns / constraints |
|---|---|---|
| `agencies` | `id` | `name`, `url`, `timezone` NOT NULL |
| `routes` | `id` | FK `agency_id`; `type >= 0`; `color`/`text_color` must be exactly 6 chars or NULL; `continuous_*` 0–3 |
| `stops` | `id` | `lat` −90..90, `lon` −180..180 NOT NULL; `location_type` 0–5; `wheelchair_boarding` ∈ (0,1,2); self-FK `parent_station` **DEFERRABLE INITIALLY DEFERRED** |
| `stop_agencies` | `(stop_id, agency_id)` | Precomputed stop→agency index; rebuilt every import; `idx_stop_agencies_agency` covers the reverse direction |
| `calendar` | `id` | 7 weekday flags ∈ (0,1); `CHECK (start_date <= end_date)` |
| `calendar_dates` | `(service_id, date)` | `exception_type` ∈ (1,2) |
| `trips` | `id` | FK `route_id`, `service_id`; `direction_id` NULL/0/1; cached `min_arrival_time`, `max_departure_time` with an ordering CHECK |
| `stop_times` | `(trip_id, stop_sequence)` | FKs to trips and stops; `CHECK (arrival_time <= departure_time)`; `timepoint` ∈ (0,1) |
| `frequencies` | `(trip_id, start_time)` | `headway_secs > 0`; `CHECK (start_time < end_time)` |
| `shapes` | `id` AUTOINCREMENT | `UNIQUE (shape_id, shape_pt_sequence)` |
| `import_metadata` | `id` with `CHECK (id = 1)` | Single-row table: `file_hash`, `import_time`, `file_source`, `feed_expires_at` |
| `block_trip_index` | `id` | `index_key` UNIQUE = `"{service_ids}|{stop_sequence_key}"` |
| `block_trip_entry` | `id` | FKs `block_trip_index_id`, `trip_id`; `block_trip_sequence >= 0` |
| `block_layover` | `id` | `CHECK (layover_start <= layover_end)`; covering index `(route_id, service_id, layover_end, layover_start, block_id)` |
| `problem_reports_trip` | `id` | lat/lon range CHECKs; `user_on_vehicle` NULL/0/1; `CHECK (submitted_at >= created_at)` |
| `problem_reports_stop` | `id` | same time and coordinate CHECKs |
| `routes_fts` | virtual, FTS5 | External-content over `routes` (`short_name`, `long_name`, `desc`); 3 triggers + a `'rebuild'` migration step |
| `stops_fts` | virtual, FTS5 | `stop_name`, `tokenize = 'unicode61'`; 3 triggers |
| `stops_rtree` (+ `_rowid`, `_node`, `_parent`) | virtual, R-Tree | Point bounding boxes maintained by 3 triggers on `stops` |

`transfers` and `feed_info` are **not created by the schema**, although `TableCounts`
(`gtfsdb/debugging.go:92-93`) carries queries for them — harmlessly, because it only counts tables that
`sqlite_master` actually lists.

### E.4 Field-level mapping: GTFS stop_time → `ArrivalAndDeparture`

| API field | Source | Transformation |
|---|---|---|
| `routeId`, `tripId`, `stopId`, `vehicleId` | route/trip/stop/vehicle IDs | `utils.FormCombinedID(agencyID, rawID)`; returns `""` if either part is empty (`utils/api.go:110-115`) |
| `serviceDate` | matched service date | `serviceDate.Start(location)` → millis |
| `scheduledArrivalTime` | `stop_times.arrival_time` | `serviceStart.Add(time.Duration(ns))` → millis |
| `scheduledDepartureTime` | `stop_times.departure_time` | same |
| `predictedArrivalTime` / `predictedDepartureTime` | GTFS-RT StopTimeUpdate | `getPredictedTimes` 3-tier: exact stop (`Time` → delta, else `Delay`) → nearest prior stop's propagated delay → trip-level `Delay`. Zeroed when `predicted` is false |
| `predicted` | — | True when a prediction was produced |
| `stopSequence` | `stop_times.stop_sequence` | **`int(stop_sequence) − 1`** in the plural path (`arrivals_core.go:866`); the singular path emits the **0-based array index** instead (`arrival_and_departure_for_stop_handler.go:423`) |
| `totalStopsInTrip` | `GetStopTimesForTripIDs` count | batched map lookup; 0 on query failure |
| `numberOfStopsAway` | block snapshot | `targetStop.BlockSequence − nextStop.BlockSequence`, only when `InRange` and `NextStopIndex >= 0` |
| `distanceFromStop` | block snapshot | `targetStop.DistanceAlongBlock − snapshot.DistanceAlongBlock`, same gate |
| `blockTripSequence` | reused from `tripStatus` | `GetBlockTripSequence` (`ROW_NUMBER()`) |
| `frequency` | `frequencies` rows | window containing the query time, else `freqs[0]` |
| `situationIds` | `alertIndex` | IDs are `situationID(alertID, agencyID)` — prefixing is **idempotent** (`reference_utils.go:867-872`) |
| `status` | — | Hard-coded `"default"` at both call sites; `tripStatus.status` carries the real value |
| `arrivalEnabled` / `departureEnabled` | — | Hard-coded `true` at both call sites |
| `actualTrack`, `scheduledTrack`, `*Interval`, `*Occupancy` | — | Always empty/nil in `models.NewArrivalAndDeparture` |

### E.5 The merged realtime snapshot

`internal/gtfs/realtime.go:39-48`. Rebuilt wholesale on every feed update, published through
`atomic.Pointer[mergedRealtime]`, read **without any lock** via `manager.mergedRealtime()`
(`:68-73`). The documented invariant (`:34-38`) is that nothing reachable from a published snapshot is
ever mutated.

| Field | Shape | Built by |
|---|---|---|
| `trips`, `vehicles` | concatenation over sorted feed IDs | `rebuildMergedRealtimeLocked:832-857` |
| `tripLookup` | trip ID → index into `trips` | `:865-870` |
| `vehicleLookupByTrip` | trip ID → index into `vehicles` | `:876-879` |
| `vehicleLookupByVehicle` | vehicle ID → index | `:883-885` |
| `vehiclesByRoute` | route ID → vehicles | `:880-882` |
| `duplicatedVehicleByRoute` | route ID → DUPLICATED-trip vehicles, with a TripUpdate fallback for a missing `route_id` | `:886-900` |
| `alerts` | `alertIndex{byTrip, byRoute, byAgency, byStop}` | `:901-944` — route buckets only for entities with no stop/trip restriction; agency buckets only for entities with no route/trip restriction; per-alert per-bucket dedup |

`vehicleKey` (`:161-178`) keeps identity sources distinct: exactly one of `id`, `label`,
`licensePlate` is set, so a real ID can never collide with a label of the same text.

### E.6 Representative end-to-end data path

```
GTFS zip bytes
  → sha256 (skip-if-unchanged gate)
  → gtfs.ParseStatic → *gtfs.Static
  → ValidateAndFilterGTFSData  (drops structurally invalid trips)
  → StoreGtfsData              (17 tables, one transaction)
  → DirectionPrecomputer       (stops.direction)
  → computeRegionBounds        (in-memory map[agencyID]*RegionBounds)

HTTP GET /api/where/arrivals-and-departures-for-stop/25_1001.json?key=test
  → GetStop / GetAgency / LoadLocation
  → GetActiveServiceIDsForDate × 3 days × timezone groups
  → GetStopTimesForStopInWindow            (ns offsets vs the window)
  → GetRoutesByIDs / GetTripsByIDs / GetStopTimesForTripIDs / GetFrequenciesForTrips
  → per row: mergedRealtime snapshot (vehicle + TripUpdate, lock-free)
             + scheduledBlockSnapshot (cached per request)
  → models.ArrivalAndDeparture{…ModelTime millis…}
  → references{agencies, routes, stops, trips, situations, stopTimes}
  → models.NewArrivalsAndDepartureResponse
  → gzip (≥1 KiB) → client
```

---

## Section F: API and Integration Reference

### F.1 Registered HTTP routes

All from `internal/restapi/routes.go:71-122` unless noted. Every `/api/...` route is `GET` only, takes
`?key=`, and accepts optional `?version=2`. `CC` = Cache-Control tier (L = `public, max-age=300`,
S = `public, max-age=30`, N = `no-cache, no-store, must-revalidate`).

| Route | Handler | CC | ETag | Auth |
|---|---|---|---|---|
| `GET /healthz` | `healthHandler` | — | — | **none** |
| `GET /metrics` | `promhttp` (`cmd/api/app.go:182`) | — | — | **none** |
| `GET /api/v2/metadata.json` | `metadataHandler` | S | — | key |
| `GET /api/where/current-time.json` | `currentTimeHandler` | S | — | key |
| `GET /api/where/agencies-with-coverage.json` | `agenciesWithCoverageHandler` | L | ✓ | key |
| `GET /api/where/agency/{id}` | `agencyHandler` | L | ✓ | key |
| `GET /api/where/routes-for-agency/{id}` | `routesForAgencyHandler` | L | ✓ | key |
| `GET /api/where/route-ids-for-agency/{id}` | `routeIDsForAgencyHandler` | L | ✓ | key |
| `GET /api/where/stops-for-agency/{id}` | `stopsForAgencyHandler` | L | ✓ | key |
| `GET /api/where/stop-ids-for-agency/{id}` | `stopIDsForAgencyHandler` | L | ✓ | key |
| `GET /api/where/route/{id}` | `routeHandler` | S | — | key |
| `GET /api/where/stop/{id}` | `stopHandler` | L | ✓ | key |
| `GET /api/where/trip/{id}` | `tripHandler` | L | ✓ | key |
| `GET /api/where/shape/{id}` | `shapesHandler` | L | ✓ | key |
| `GET /api/where/block/{id}` | `blockHandler` | L | ✓ | key |
| `GET /api/where/stops-for-route/{id}` | `stopsForRouteHandler` | L | ✓ | key |
| `GET /api/where/schedule-for-stop/{id}` | `scheduleForStopHandler` | L | ✓ | key |
| `GET /api/where/schedule-for-route/{id}` | `scheduleForRouteHandler` | L | ✓ | key |
| `GET /api/where/stops-for-location.json` | `stopsForLocationHandler` | S | — | key |
| `GET /api/where/routes-for-location.json` | `routesForLocationHandler` | S | — | key |
| `GET /api/where/trips-for-location.json` | `tripsForLocationHandler` | S | — | key |
| `GET /api/where/arrivals-and-departures-for-location.json` | `arrivalsAndDeparturesForLocationHandler` | S | — | key |
| `GET /api/where/search/stop.json` | `searchStopsHandler` | S | — | key |
| `GET /api/where/search/route.json` | `routeSearchHandler` | S | — | key |
| `GET /api/where/trip-details/{id}` | `tripDetailsHandler` | S | — | key |
| `GET /api/where/trip-for-vehicle/{id}` | `tripForVehicleHandler` | S | — | key |
| `GET /api/where/trips-for-route/{id}` | `tripsForRouteHandler` | S | — | key |
| `GET /api/where/vehicles-for-agency/{id}` | `vehiclesForAgencyHandler` | S | — | key |
| `GET /api/where/arrival-and-departure-for-stop/{id}` | `arrivalAndDepartureForStopHandler` | S | — | key |
| `GET /api/where/arrivals-and-departures-for-stop/{id}` | `arrivalsAndDeparturesForStopHandler` | S | — | key |
| `GET /api/where/report-problem-with-trip/{id}` | `reportProblemWithTripHandler` | N | — | key |
| `GET /api/where/report-problem-with-stop/{id}` | `reportProblemWithStopHandler` | N | — | key |
| `GET /api/where/problem-reports-for-trip/{id}` | `problemReportsForTripHandler` | N | — | **protected key** |
| `GET /api/where/problem-reports-for-stop/{id}` | `problemReportsForStopHandler` | N | — | **protected key** |
| `GET /api/where/config.json` | `configHandler` | *(none)* | — | key |
| `GET /api/where/metrics.json` | `metricsHandler` | *(none)* | — | key |
| `GET /` | `webui.indexHandler` | — | — | none |
| `GET /debug/` | `webui.debugIndexHandler` | — | — | none (404 in production env) |
| `GET /marketing/{file}` | `webui.marketingHandler` | — | — | none |

`config.json` and `metrics.json` are the only `/api/where` routes registered **without**
`CacheControlMiddleware` (`routes.go:89-90`).

### F.2 Request parameter vocabulary

| Parameter | Parser | Semantics |
|---|---|---|
| `key` | `app.IsInvalidAPIKey` / `isProtectedAPIKey` | `crypto/subtle.ConstantTimeCompare`; empty is always invalid |
| `version` | `VersionValidationMiddleware` | Absent/empty → treated as 2; any other value → 400 `unknown version: X` |
| `includeReferences` | `ShouldIncludeReferences` → `utils.ParseBoolParam` | Absent → `true`; present-but-empty → `false`; non-boolean → field error |
| `includeTrip`, `includeSchedule`, `includeStatus`, `includePolylines` | `utils.ParseBoolParam` | Default `true` |
| `lat`, `lon`, `radius`, `latSpan`, `lonSpan` | `parseLocationParams` → `ParseFloatParam` + `ValidateLocationParams` | NaN/Inf rejected via `isFinite`; radius takes precedence over spans; default radius 600 m, query radius 10 km, clamp 20 km |
| `maxCount` | `ParseMaxCount` (reject >ceiling) / `ParseMaxCountClamped` (silently clamp) | Ceiling `MaxAllowedCount = 250`; `routes-for-location` clamps to 50; `arrivals-for-location` to 1000 |
| `minutesBefore` / `minutesAfter` | `parseMinutesValue` / inline in the singular handler | Defaults 5/35 (plural) and 5/30 (singular); both capped at 24 h |
| `time` | `utils.ParseTimeParameter` (epoch ms, `YYYY-MM-DD`, or `YYYY-MM-DD_HH-mm-ss`) or `parseEpochMillisValue` (epoch ms only) | Varies by handler |
| `date` | `utils.ValidateServiceDate` then `utils.ParseDate` | `YYYY-MM-DD` or epoch ms (bounded to year 3000) |
| `serviceDate` | `ParseEpochMillis` | Required on `arrival-and-departure-for-stop` |
| `tripId` | required on `arrival-and-departure-for-stop` | Missing → 400 `missingRequiredField` |
| `stopSequence` | `strconv.Atoi` | Interpreted as the **0-based position in the trip's stop list**, not the raw GTFS value (documented at `arrival_and_departure_for_stop_handler.go:774-778`) |
| `vehicleId` | `ExtractAgencyIDAndCodeID` + `GetVehicleByID` | Must be on the requested trip, else ignored (singular arrivals) or 404 (trip-details) |
| `routeType` | CSV of ints, max 100 tokens | `stops-for-location`, `arrivals-for-location` |
| `input` | `ParseRequiredStringParam` | Required on both search endpoints |
| `query` | `utils.SanitizeInput` (strips HTML tags) | `stops-for-location`, `routes-for-location` |
| `ageInSeconds` | `strconv.ParseInt` | `vehicles-for-agency` freshness cutoff |
| `offset`, `limit` | `utils.ParsePaginationParams` | `maxCount` wins over `limit`; limit capped at 1000; only used by `agencies-with-coverage` |
| `verbose` | literal `"true"` | `/healthz` freshness block |
| `dataType` | switch | `/debug/` |
| `userLat`, `userLon`, `userLocationAccuracy`, `userComment`, `userOnVehicle`, `userVehicleNumber`, `code`, `stopId`, `vehicleId`, `serviceDate` | problem-report handlers | `userComment` truncated to 500 runes |

### F.3 Response envelopes

Success: `{code, currentTime, data, text, version}` where `version` is always `2`
(`models.APIVersion`) and `currentTime` comes from the injected `Clock`.

| Constructor | `data` shape |
|---|---|
| `NewEntryResponse` | `{entry, references}` |
| `NewListResponse` | `{limitExceeded, list, references}` |
| `NewListResponseWithRange` | `{limitExceeded, list, outOfRange, references}` |
| `NewArrivalsAndDepartureResponse` | `{entry:{arrivalsAndDepartures, nearbyStopIds, situationIds, stopId}, references}` |
| `NewArrivalsAndDeparturesForLocationResponse` | `{entry:{arrivalsAndDepartures, limitExceeded, nearbyStopIds, situationIds, stopIds}, references}` — nil slices normalised to `[]` because the spec marks them required |

`references` is always `{agencies, routes, situations, stopTimes, stops, trips}`
(`models/references.go:4-11`), emitted with empty arrays when `includeReferences=false`.

Errors: `401 permission denied` (`invalidAPIKeyResponse`), `400` with
`data.fieldErrors{field:[msg]}` (`validationErrorResponse`), `404 resource not found`
(`sendNotFound`), `429` with `Retry-After` + `X-RateLimit-*`, `500 internal server error`,
`504 gateway timeout` on `context.DeadlineExceeded`, and **no response body at all** on
`context.Canceled` (`errors.go:121-131`).

### F.4 Outbound integrations

| Target | Client | Timeouts / limits |
|---|---|---|
| Static GTFS | ad-hoc `http.Client` (`static.go:40-46`) | 5 min total, 10 s TLS handshake, 30 s response header, 90 s idle; 200 MiB cap; optional single auth header |
| GTFS-RT | package-level `realtimeHTTPClient` (`realtime.go:123-146`) | 10 s total; transport cloned from `DefaultTransport` with MaxIdleConns 50 / 10 per host; 25 MiB cap; arbitrary `headers` map per feed; callers additionally impose a 15 s context |
| `gtfstidy` | `os/exec` subprocess | `internal/gtfs/tidy.go` |

No outbound call is retried at the HTTP level. Static load retries happen at the
`InitGTFSManager` layer; realtime retries happen via the poll loop's backoff.

### F.5 Integration-relevant tests and fixtures

`internal/restapi/openapi_conformance_test.go` loads `testdata/openapi.yml` with
`getkin/kin-openapi`, validates the document, resolves `allOf`, and checks live handler responses
against the spec schemas. Realtime integration uses `httptest.NewServer` serving
`testdata/raba-{trip-updates,vehicle-positions}.pb`; `testdata/unitrans-*.pb` exist specifically to
exercise the mismatched-agency case.

---

## Section G: Configuration Reference

### G.1 Sources and precedence

Two mutually exclusive entry paths (`cmd/api/main.go:86-93` — `-f` plus any other flag except
`--dump-config` is a fatal error), both funnelling into the same `JSONConfig.Validate()`:

**JSON path** (`appconf.LoadFromFile`, `json_config.go:370-474`):
```
file bytes → json.Unmarshal → setDefaults() → environment overrides → Validate()
```
**Flag path** (`main.go:120-169`): flag values are packed into a synthetic `JSONConfig`
(always feed ID `"feed-0"`, `refresh-interval: 30`, `log-level: info`, `log-format: text`) and validated
identically.

So effective precedence is: **environment variables > JSON file > built-in defaults**, and for the
flag path: **flag > environment (protected keys only) > per-environment default**.

File loading hardening: `os.Lstat` (no symlinks), regular files only, 10 MiB maximum
(`json_config.go:375-389`).

### G.2 Settings → consumers

| Key | Default | Consumed by |
|---|---|---|
| `port`, `host` | 4000, `""` | `net.JoinHostPort` in `CreateServer` (`app.go:217`) |
| `env` | `development` | `createClock` (Test ⇒ `EnvironmentClock`); `createDB` (Test ⇒ `:memory:` only); `debugIndexHandler` (Production ⇒ 404) |
| `api-keys` | `["test"]` | `app.IsInvalidAPIKey` |
| `protected-api-keys` | `["protected-test-key"]` in dev/test, **nil in production** | `isProtectedAPIKey`; `Validate` rejects an empty list, so production must supply one |
| `exempt-api-keys` | `["org.onebusaway.iphone"]` | `RateLimitMiddleware.exemptKeys` |
| `rate-limit` | 100 | `NewRateLimitMiddleware` — `<0` ⇒ `rate.Inf`, `0` ⇒ block all, else `rate.Every(1s/n)` with burst = n |
| `log-level`, `log-format` | `info`, `text` | `parseLogLevel`, `newLogHandler` |
| `tls-cert-path`, `tls-key-path` | `""` | `Run` → `ListenAndServeTLS`; `Validate` requires both or neither |
| `gtfs-static-feed.url` | Sound Transit 40_gtfs.zip | `gtfs.Config.GtfsURL`; `file://` is explicitly rejected; non-HTTP values go through `validatePath` |
| `gtfs-static-feed.auth-header-{name,value}` | `""` | Single request header on the static fetch; `Validate` requires both or neither |
| `gtfs-static-feed.enable-gtfs-tidy` | `false` | `tidyGTFSData` |
| `gtfs-rt-feeds[].id` | `feed-%d` | Partition key for every per-feed map; duplicates are a hard error |
| `gtfs-rt-feeds[].agency-ids` | `[]` | `config.feedAgencyFilters()`; unknown IDs warn at startup but do not fail |
| `gtfs-rt-feeds[].{trip-updates,vehicle-positions,service-alerts}-url` | `""` | `updateFeedRealtime`; a feed with no URL is never activated |
| `gtfs-rt-feeds[].headers` | `{}` | Added to every RT request |
| `gtfs-rt-feeds[].realtime-auth-header-{name,value}` | `""` | **Legacy**: merged into `headers` unless the key already exists (then warn and discard) |
| `gtfs-rt-feeds[].refresh-interval` | 30 s | `pollFeed` base interval (`<= 0` normalised to 30 twice over) |
| `gtfs-rt-feeds[].enabled` | `true` | `config.enabledFeeds()` |
| `data-path` | `./gtfs.db` | `gtfsdb.Config.DBPath`; `:memory:` is a special case for pooling and is the **only** value allowed in Test env |

### G.3 Environment variables (every `os.Getenv` in non-test source)

| Variable | Effect | Location |
|---|---|---|
| `MAGLEV_ENABLE_PPROF` | `=1` starts pprof on `127.0.0.1:6060` | `cmd/api/main.go:27` |
| `MAGLEV_PROFILE_MUTEX` | `=1` enables mutex + block profiling | `cmd/api/main.go:39` |
| `GTFS_API_KEYS` | Comma-separated; replaces `api-keys` (JSON path only) | `json_config.go:407` |
| `GTFS_PROTECTED_API_KEYS` | Comma-separated; replaces `protected-api-keys`; also consulted by the flag path | `json_config.go:67`, `cmd/api/app.go:81` |
| `MAGLEV_LOG_LEVEL`, `MAGLEV_LOG_FORMAT` | Override logging (JSON path only) | `json_config.go:421-425` |
| `GTFS_STATIC_AUTH_NAME` / `_VALUE` | Override static feed auth | `json_config.go:434-439` |
| `GTFS_REALTIME_AUTH_NAME` / `_VALUE` | Override RT auth — **first configured feed only**, documented at `json_config.go:442` | `json_config.go:443-458` |
| `FAKETIME` (+ `/etc/faketimerc`) | Read by `EnvironmentClock` in Test env | `clock/clock.go:112`, `cmd/api/app.go:161` |
| `TZ` | Container timezone | `docker-compose*.yml` |

Documented-but-unimplemented: the README's environment table lists **`HEALTH_CHECK_KEY`**; a
repository-wide grep finds no reference to it in any `.go`, `.yml` or `.json` file, and `/healthz`
requires no key.

### G.4 Sensitive-value handling observed

- `--dump-config` redacts API keys (count only), the static auth header value and all RT header values
  (`cmd/api/app.go:313-358`).
- Config load logs only `port`, `env`, key **counts**, rate limit and log settings (`json_config.go:465-471`).
- API-key comparison is constant-time in both the normal and protected paths.
- No secret is logged by the handlers inspected. Feed **URLs** (which in the shipped examples embed
  `?key=org.onebusaway.iphone`) are logged on RT fetch errors and in the poller's startup line
  (`realtime.go:360-362`, `:1000-1002`) — recorded here as an observation about where credentials can
  surface in logs, not as a finding.
- `.gitignore` excludes `config.json`, `config.docker.json`, `configs/*.json`, `.env` and `*.db`.
- No credential values were printed during this scan.

---

## Section H: Dependency and Build Reference

### H.1 Direct dependencies and confirmed usage

| Module | Version | Confirmed use |
|---|---|---|
| `github.com/OneBusAway/go-gtfs` | v1.2.0 | `ParseStatic`, `ParseRealtime`, and the `gtfs.*` domain types throughout `internal/gtfs`, `internal/restapi`, `gtfsdb` |
| `github.com/mattn/go-sqlite3` | v1.14.52 | CGO driver, `!purego` build (`gtfsdb/driver_cgo.go`, `constraint_cgo.go`) |
| `modernc.org/sqlite` | v1.60.1 | Pure-Go driver, `purego` build (`gtfsdb/driver_pure.go`, `constraint_pure.go`) |
| `github.com/prometheus/client_golang` | v1.24.1 | `internal/metrics`, `promhttp` at `cmd/api/app.go:182` |
| `github.com/klauspost/compress` | v1.20.1 | `gzhttp` in `compression_middleware.go` |
| `golang.org/x/time` | v0.16.0 | `rate.Limiter` in `rate_limit_middleware.go` |
| `github.com/google/uuid` | v1.6.0 | `RequestIDMiddleware` |
| `github.com/twpayne/go-polyline` | v1.1.1 | Declared direct; **`internal/utils/polyline.go` hand-rolls the encoder** to match Java's floor semantics. Only non-test reference found is the module line itself — a dependency whose runtime use could not be confirmed in non-test source |
| `google.golang.org/protobuf` | v1.36.12 | Transitively required by go-gtfs' `proto` package, used directly via `gtfsrt.TripDescriptor_*` constants |
| `github.com/getkin/kin-openapi` | v0.149.0 | **Test-only**: `openapi_conformance_test.go` |
| `github.com/stretchr/testify` | v1.12.1 | **Test-only**: assertions across the suite |
| `golang.org/x/sync` | v0.24.0 | Declared direct; no non-test import found in the files inspected |
| `github.com/sqlc-dev/sqlc` | v1.30.0 | `tool` directive; `make models` |
| `github.com/patrickbr/gtfstidy` | pseudo-version | `tool` directive; built into `bin/gtfstidy` by `make gtfstidy` and invoked as a subprocess |

63 further modules are listed as indirect.

### H.2 Build tags (load-bearing)

| Tag set | Meaning |
|---|---|
| `sqlite_fts5 sqlite_math_functions` + `CGO_ENABLED=1` | **Default/production.** FTS5 is required for the schema migration itself (`routes_fts`, `stops_fts`); `sqlite_math_functions` is required by `GetActiveRoutesWithinBounds` (`acos`, `cos`, `sin`, `radians`) |
| `purego` + `CGO_ENABLED=0` | Pure-Go driver; modernc ships FTS5 and math functions in-tree |
| `perftest` | Gates 4 large-dataset test files that need `testdata/perf/trimet.zip` (gitignored) |

Running untagged `go test` is explicitly called out as unsupported (`CLAUDE.md`), because the default
`mattn` driver without `sqlite_fts5` fails migration with `no such module: fts5`.

### H.3 Commands

| Command | Make target | Expansion |
|---|---|---|
| Build (CGO) | `make build` | `CGO_ENABLED=1 go build -tags "sqlite_fts5 sqlite_math_functions" $(LDFLAGS) -o bin/maglev ./cmd/api`, preceded by `make gtfstidy` |
| Build (pure) | `make build-pure` | `CGO_ENABLED=0 go build -tags purego …` |
| Run | `make run` | `bin/maglev -f config.json` |
| Test (CGO) | `make test` | `CGO_ENABLED=1 go test -tags "sqlite_fts5 sqlite_math_functions" ./...` |
| Test (pure) | `make test-pure` | `CGO_ENABLED=0 go test -tags purego ./...` |
| Vet | `make lint` | `go vet -tags "sqlite_fts5 sqlite_math_functions" ./...` |
| Format | `make fmt` | `go fmt ./...` |
| Coverage | `make coverage` / `make coverage-report` | `-coverprofile=coverage.out` + `go tool cover -html`; the report variant needs `jq` |
| Regenerate models | `make models` | `go tool sqlc generate -f gtfsdb/sqlc.yml` |
| Latency / benchmarks | `make test-latency`, `make bench-sqlite-all`, `make bench-sqlite-perftest` | gtfsdb-scoped |
| OpenAPI sync | `make update-openapi` / `make check-openapi` | `scripts/update-openapi.sh` / `check-openapi.sh` |
| Load test | `make load-test` (= `smoketest` + `stresstest`) | Writes `config.ci.json`, starts the binary, polls `/healthz` up to 60×5 s, runs k6, cleans up via `trap` |
| Live reload | `make watch` | `air` |
| Docker | `make docker-*` | 10 targets; `docker-clean-all` prompts interactively before deleting volumes |

`LDFLAGS` injects 11 `internal/buildinfo` variables from git, surfaced by
`/api/where/config.json`.

### H.4 CI workflows

| Workflow | Trigger | Stages |
|---|---|---|
| `go.yml` — **Go CI** | push to `main`, all PRs | **fmt** job: `go fmt ./...` must produce no output (gated to the upstream repo). **test** job: `go mod download` → `go mod verify` → `check-openapi.sh` (PRs only) → `go vet` in **both** modes → `go test -v` CGO mode with `-timeout 40m` and coverage → **coverage gate: ≥75 %** after stripping `query.sql.go`, `db.go`, `models.go`, `*_mock.go` → SonarQube (currently disabled via `SONAR_CI_ANALYSIS_ENABLED: false`). **test-race** job: `go test -race ./internal/gtfs/...`, 20 m timeout |
| `docker-publish.yml` | push to `main`; PRs touching `Dockerfile`/`go.mod`/itself | Superseded-commit check → QEMU + Buildx → amd64/arm64 → push to `ghcr.io/onebusaway/maglev:{latest,sha}` (push events only) |
| `docker-release.yml` | published non-prerelease release | amd64/arm64 → Docker Hub `opentransitsoftwarefoundation/maglev:{tag,latest}` |
| `loadtest-smoke.yml` | every PR | Build → start with the RABA fixture → install k6 → `smoke.js` (5 VUs × 30 s) → post a summary comment → upload artifact → fail on threshold breach |
| `loadtest-stress.yml` | push to `main`, nightly 02:00 UTC, manual | pprof-enabled run → `scenarios.js` (ramp to 500 VUs) → heap/goroutine/mutex/CPU profiles → artifacts |

`dependabot.yml` tracks `gomod`, `docker` and `github-actions` weekly.

### H.5 Containers and deployment

`Dockerfile` — two stages: `golang:1.27-alpine` with `gcc musl-dev`, cache mounts for the module and
build caches, CGO build with the FTS5 tags and the full ldflags block; runtime `alpine:3.24` with
`ca-certificates tzdata wget sqlite`, a non-root `maglev` user (UID/GID build args), `/app/data` for
the database, `EXPOSE 4000`, and a `HEALTHCHECK` wgetting `/healthz` every 30 s.
`Dockerfile.dev` adds `air@v1.61.7` and `dlv`, exposing 4000 and 2345.
`docker-compose.yml` mounts `config.docker.json` read-only, persists `maglev-data`, sets `TZ=UTC`, and
caps JSON-file logs at 10 MiB × 3. `docker-compose.dev.yml` bind-mounts the source and caches modules
in a named volume. **No Kubernetes, Helm or Terraform manifests exist**; `remote/` and `migrations/`
are empty placeholders.

---

## Section I: Test Architecture

### I.1 Organisation

179 `_test.go` files, co-located with the code under test. 1,378 `Test*` functions, 37
`Benchmark*`, 2 `Fuzz*`.

| Package | Test files | `Test*` | `Benchmark*` | `Fuzz*` |
|---|---|---|---|---|
| `internal/restapi` | 84 | 813 | 11 | 0 |
| `internal/gtfs` | 23 | 201 | 11 | 0 |
| `gtfsdb` | 23 | 91 | 12 | 0 |
| `internal/models` | 18 | 91 | 0 | 0 |
| `internal/utils` | 13 | 70 | 3 | 2 |
| `internal/appconf` | 2 | 32 | 0 | 0 |
| `cmd/api` | 3 | 17 | 0 | 0 |
| `internal/clock` | 1 | 17 | 0 | 0 |
| `internal/metrics` | 1 | 14 | 0 | 0 |
| `internal/webui` | 5 | 10 | 0 | 0 |
| `internal/nulls` | 1 | 7 | 0 | 0 |
| `internal/logging` | 2 | 6 | 0 | 0 |
| `internal/app` | 1 | 4 | 0 | 0 |
| `internal/servicedate` | 1 | 4 | 0 | 0 |
| `internal/buildinfo` | 1 | 1 | 0 | 0 |

### I.2 Harness, fixtures, mocks

- **`internal/restapi/http_test.go`** is the canonical harness. `TestMain` deletes
  `testdata/raba-test.db` (+ `-shm`/`-wal`) before and after the run. A `sync.Once` builds **one shared
  `gtfs.Manager`** from `testdata/raba.zip` into that file-backed DB and reuses it across all 84 test
  files; `createTestApi(t)` / `createTestApiWithClock(t, c)` wrap it in an `Application` with
  `RateLimit: 5` and a fixed key list; `newTestManagerNoData(t)` gives an `:memory:` client with the
  real schema and no feed.
- `internal/restapi/testdata/models.go` — package `testdata`, a non-test file holding expected RABA
  model values (`Raba`, `Route19`, `Route15`, …) used as golden data.
- `internal/gtfs/gtfs_manager_mock.go` — a **non-test** file (excluded from CI coverage by the
  `_mock.go` filter) exposing `MockAddAgency/Route/Vehicle/Trip/TripUpdate/Alert`,
  `MockAddDuplicatedVehicle`, `MockResetRealTimeData`.
- Manager test seams on the production type: `SetRealTimeTripsForTest`, `SetRealTimeVehiclesForTest`,
  `AddAlertForTest`, `SetFeedUpdateTimeForTest`, `SetStaticLastUpdatedForTest`,
  `SetFeedExpiresAtForTest` — all writing under the synthetic feed ID `"_test"` so a real feed refresh
  cannot silently discard them.
- `clock.MockClock` (`Set`/`Advance`) is the determinism mechanism for every time-dependent assertion.
- Realtime integration: `httptest.NewServer` serving `testdata/raba-*.pb`; `unitrans-*.pb` for the
  mismatched-agency case; `testdata/config_{valid,full,invalid,malformed}.json` for config tests.
- `internal/utils/testdata/fuzz/FuzzCalculateBounds/3f62f84f0a0ec5a8` — a committed fuzz corpus entry.

### I.3 Component-to-test mapping (representative)

| Component | Direct tests |
|---|---|
| Lifecycle / wiring | `cmd/api/app_test.go`, `main_test.go`, `lock_safety_test.go` (`TestHandlerLockSafety`) |
| Config | `internal/appconf/{config,json_config}_test.go` |
| Static import | `gtfsdb/{conditional_import,validation,bulk_insert,bulk_insert_frequency,foreign_keys,error_handling,stop_codes_backfill,stop_time_revenue_import,shape_dist_traveled,nil_shape}_test.go` |
| Block/layover indexes | `gtfsdb/block_layover_test.go`, `internal/restapi/calculate_block_trip_sequence_test.go`, `scheduled_block_helper_test.go` |
| FTS5 / R-Tree | `gtfsdb/fts_queries_test.go`, `stops_rtree_test.go`, `internal/gtfs/spatial_index_test.go` |
| DB performance | `gtfsdb/{query_latency,sqlite_performance,connection_pool,query_latency_perftest}_test.go` |
| Realtime | `internal/gtfs/{realtime,realtime_snapshot,parallel_realtime,multi_feed,agency_filter,realtime_benchmark}_test.go` |
| Shutdown | `internal/gtfs/{shutdown,shutdown_timeout}_test.go`, `internal/restapi/rate_limit_shutdown_test.go` |
| Directions / shapes | `internal/gtfs/{advanced_direction_calculator,shapes,shape_cache,shape_dist_monotonic,stop_distances}_test.go` |
| Every middleware | one `*_middleware_test.go` per middleware file |
| Every handler | one `*_handler_test.go` per handler file |
| Cross-cutting correctness | `openapi_conformance_test.go`, `dst_service_day_test.go`, `servicedate_timezone_regression_test.go`, `negative_epochs_test.go`, `entity_agency_prefix_test.go`, `situation_references_test.go`, `location_multi_agency_refs_test.go`, `strict_params_test.go`, `input_validation_integration_test.go`, `context_cancellation_test.go`, `db_error_status_test.go` |
| Coverage padding | `internal/restapi/coverage_test.go`, `internal/utils/coverage_test.go`, `internal/webui/webui_coverage_test.go` |

Areas with no dedicated test file found in the inspected scope: `internal/restapi/db_errors.go`,
`compression_middleware.go` (exercised indirectly through `http_test.go`'s gzip helpers),
`internal/gtfs/tidy.go`, and `gtfsdb/debugging.go`.

### I.4 Test execution results in this environment

Commands run, verbatim, and their observed results:

| # | Command | Result |
|---|---|---|
| 1 | `CGO_ENABLED=0 go vet -mod=readonly -tags purego ./internal/utils/... ./internal/models/... ./internal/nulls/... ./internal/servicedate/...` | **exit 0**, no diagnostics (19.9 s) |
| 2 | `CGO_ENABLED=0 go vet -mod=readonly -tags purego ./...` | **exit 0, whole module clean, no output** |
| 3 | `CGO_ENABLED=1 go vet -mod=readonly -tags "sqlite_fts5 sqlite_math_functions" ./gtfsdb/` | **exit 1 — BLOCKED**: `cgo: C compiler "gcc" not found` |
| 4 | `CGO_ENABLED=0 go test -mod=readonly -tags purego -count=1 ./internal/utils/... ./internal/servicedate/... ./internal/nulls/... ./internal/clock/... ./internal/models/... ./internal/logging/... ./internal/metrics/... ./internal/buildinfo/...` | **exit 0 — all 8 packages `ok`** |
| 5 | `CGO_ENABLED=0 go test -mod=readonly -tags purego -count=1 ./gtfsdb/ -run 'TestPerformDatabaseMigration_Idempotency\|TestNewConfig\|TestSafeBatchSize\|TestUpdateFeedExpiresAtFromCalendar' -v` | **exit 0 — all PASS**, confirming the pure-Go driver applies the real schema (FTS5 + R-Tree + triggers) successfully |

**Not run**: the full suite (`./...`), including all 813 `internal/restapi` tests and all
`internal/gtfs` tests. CI allots 40 minutes for it in CGO mode; this checkout sits on a `/mnt/c`
Windows mount in WSL2 where the file-backed fixture DB would be substantially slower, and the
authoritative mode cannot run here at all. No test is recorded as failing; the unrun portion is
recorded as **not executed**, not as passing.

**Blocked**: everything requiring CGO (`make test`, `make build`, `make lint`, `make coverage`,
`make test-latency`, all `bench-sqlite-*`); everything requiring `make`, `jq`, `k6` or `sqlite3`;
all `perftest`-tagged tests (need the gitignored `testdata/perf/trimet.zip`).

---

## Section J: Code Relationship Index

### J.1 Highest-fan-in symbols

| Symbol | Location | Called from |
|---|---|---|
| `utils.FormCombinedID` | `utils/api.go:110` | Essentially every handler and reference builder; returns `""` if either part is empty |
| `utils.ExtractAgencyIDAndCodeID` | `utils/api.go:101` | `extractAndValidateAgencyCodeID` and ~20 direct call sites |
| `api.sendResponse` / `sendNotFound` / `serverErrorResponse` / `validationErrorResponse` | `responses.go`, `errors.go` | All 27 handlers |
| `BuildTripStatus` | `trips_helper.go:50` | `arrivals_core.go:943`, `arrival_and_departure_for_stop_handler.go:365`, `trip_details_handler.go:259`, `trip_for_vehicle_handler.go:73`, `trips_for_route_handler.go:315,398`, `trips_for_location_handler.go`, `vehicles_for_agency_handler.go:357` (field-selective) |
| `computeScheduledBlockSnapshot` | `scheduled_block_helper.go:144` | `BuildTripStatus:244`; memoised via `WithSnapshotCache` |
| `WithSnapshotCache` | `scheduled_block_helper.go:122` | Installed by 4 handlers: plural arrivals, arrivals-for-location, trips-for-route, vehicles-for-agency |
| `manager.mergedRealtime()` | `realtime.go:68` | Every realtime read: `GetRealTimeTrips/Vehicles`, `GetVehicleForTrip`, `GetVehicleByID`, `GetTripUpdate*`, `GetAlerts*`, `VehiclesForAgencyID`, `GetDuplicatedVehiclesForRoute` |
| `nulls.StringOrEmpty` / `StringOrNonEmpty` / `Int64OrDefault` / `WheelchairBoardingOrUnknown` | `nulls/database.go` | All sqlc-row→model conversions |
| `utils.QueryInBatches` | `utils/batches.go:17` | `buildRouteAgencyMap`, `tripAgencyIDs`, `fetchFrequenciesForTrips`, `routeIDsForStops`, `fetchStopCoordsForStopTimes`, `stopsReferencedBySchedulesAndStatuses`, `tripsForLocation` paths |
| `loadAgencyLocation` | `timezone_helper.go:8` | Every handler that needs an agency timezone |
| `utils.CalculateSecondsSinceServiceDate` | `utils/api.go:49` | All stop-offset and block-interpolation math |
| `servicedate.Date.Start/Midnight` | `servicedate/servicedate.go:37,42` | `arrivals_core.go`, `schedule_for_stop_handler.go`, `models/frequency.go` |

### J.2 Interfaces and implementations

| Interface | Location | Implementations |
|---|---|---|
| `clock.Clock` | `clock/clock.go:18` | `RealClock`, `MockClock`, `EnvironmentClock` |
| `gtfsdb.DBTX` | `gtfsdb/db.go:13` | `*sql.DB`, `*sql.Tx`, and `gtfsdb.metricsWrapper` (`helpers.go:55`) |
| `gtfsdb.DBQueryMetricsRecorder` | `gtfsdb/config.go:26` | `*metrics.Metrics.RecordDBQuery` |
| `http.Handler` / middleware | `internal/restapi/*_middleware.go` | 13 middleware, 2 distinct shapes: `func(http.Handler) http.Handler` and methods on `*RestAPI` |
| `http.ResponseWriter` wrappers | — | `statusCapturingWriter`, `cacheControlWriter` |
| `restapi.testingFatalf` | `test_helper.go:5` | Narrow `*testing.T` seam for the shared ID collectors |

Composition rather than inheritance throughout: `RestAPI` embeds `*app.Application`
(`rest_api.go:9`); `webui.WebUI` embeds `*app.Application`; `models.TripResponse` embeds `*Trip`;
`models.BlockStopTimeData` embeds `StopTime`; `models.Frequency` and `ScheduleFrequency` embed
`FrequencyWindow`.

### J.3 Shared state and ownership

| State | Owner | Protection |
|---|---|---|
| `feedTrips`, `feedVehicles`, `feedAlerts`, `feedUnattributedTrips`, `feedVehicleLastSeen`, `feedVehicleTimestamp`, `feedLastUpdate` | `gtfs.Manager` | `realTimeMutex` (writers hold it across the snapshot rebuild) |
| `merged` (published snapshot) | `gtfs.Manager` | `atomic.Pointer`; **readers take no lock**; the invariant is that a published snapshot is never mutated |
| `regionBounds` | `gtfs.Manager` | `staticMutex`; `GetRegionBounds` returns a value copy |
| `feedAgencyFilter` | `gtfs.Manager` | Written once in `InitGTFSManager` before any goroutine starts; read-only after, documented at `gtfs_manager.go:67-69` |
| Reload serialisation | `gtfs.Manager` | `staticUpdateMutex` |
| `isReady` | `gtfs.Manager` | `atomic.Bool` |
| Lock ordering | — | `staticMutex` → `realTimeMutex`, never the reverse (`gtfs_manager.go:36-41`) |
| Direction + trip-distance caches | `AdvancedDirectionCalculator` | Generation-swapped `directionCache` so in-flight work writes only to the retired generation |
| `snapshotCache` | request `context.Context` | Request-scoped; no cross-request sharing |
| Rate limiter | `RestAPI.rateLimiter` | One `*rate.Limiter` process-wide (internally synchronised) |
| Prometheus registry | `metrics.Metrics` | Private registry; collector goroutine guarded by `collectorStarted atomic.Bool` + `sync.WaitGroup` |

### J.4 Event producers and consumers

| Producer | Channel / medium | Consumer |
|---|---|---|
| `pollFeed` (per feed) | per-feed maps → `atomic.Pointer` snapshot | All realtime read paths |
| `updateStaticGTFS` (24 h) | SQLite + `regionBounds` + cache eviction | All static read paths |
| `shutdownChan` (closed once) | `chan struct{}` | `pollFeed`, `updateStaticGTFS` |
| `signal.NotifyContext` | context cancellation | `Run` |
| `serverErrors chan error` (cap 1) | channel | `Run`'s select |
| `RequestIDMiddleware` | `context.WithValue(RequestIDKey)` | `RequestLoggingMiddleware`, response header |
| `RequestLoggingMiddleware` | `logging.WithLogger(ctx)` | `logging.ForComponent` in every handler |
| `WithSnapshotCache` | `context.WithValue(snapshotCacheKey{})` | `computeScheduledBlockSnapshot` |
| `metricsWrapper` | `RecordDBQuery(name, op, err)` | `maglev_db_query_total` |

### J.5 Important constants

| Constant | Value | Location |
|---|---|---|
| `models.APIVersion` | `2` | `models/constants.go:47` |
| `DefaultSearchRadiusInMeters` / `QuerySearchRadiusInMeters` / `MaxSearchRadiusInMeters` | 600 / 10000 / 20000 | `models/constants.go:14-16` |
| `CacheDurationLong/Short/None` | 300 / 30 / 0 | `models/constants.go:21-23` |
| `DefaultMaxCountForStops` / `MaxAllowedCount` | 100 / 250 | `models/constants.go:27-28` |
| `DefaultMaxCountForRoutesForLocation` / `MaxCountForRoutesForLocation` | 10 / 50 | `models/constants.go:31-34` |
| `DefaultMaxCountForArrivalsForLocation` / `MaxCountForArrivalsForLocation` | 250 / 1000 | `models/constants.go:39-40` |
| `RangeSearchBufferMeters` | 50.0 | `models/constants.go:44` |
| `UnknownValue` / `Accessible` / `NotAccessible` | `UNKNOWN` / `ACCESSIBLE` / `NOT_ACCESSIBLE` | `models/constants.go:4-11` |
| `DefaultTripStatusValue` | `"default"` | `models/trip_details.go:69` |
| `staleVehicleTimeout` / `staleFeedThreshold` | 15 min / 5 min | `realtime.go:113,116` |
| `StaleDetector.threshold` | 15 min | `vehicles_helper.go:29` |
| `blockNotActiveThresholdSeconds` | 3600 | `trip_updates_helper.go:81` |
| `runningLate` / `runningEarly` | 30 min / 10 min | `trips_helper.go:1349-1350` |
| `maxRoutesFetchedForShuffle` | 5000 | `gtfs_manager.go:572` |
| `sqliteMaxVariables` | 32766 | `gtfsdb/config.go:12` |
| `utils.IDsPerBatchedQuery` | 900 | `utils/batches.go:13` |
| `utils.MaxCommentLength` | 500 | `utils/api.go:339` |
| `utils.RadiusOfEarthInMeters` | 6371010.0 | `utils/geometry.go:7` |
| `maxArrivalWindow` | 24 h | `arrival_params.go:13` |
| `maxRouteTypeValues` | 100 | `arrivals_and_departures_for_location_handler.go:35` |
| `nearbyBoxHalfSizeMeters` | 100.0 | `arrivals_and_departures_for_location_handler.go:289` |
| `gtfsShutdownTimeout` | 30 s | `cmd/api/app.go:31` |
| `maxBodySize` (RT) / `maxStaticSize` | 25 MiB / 200 MiB | `realtime.go:329`, `static.go:59` |
| `maxConfigSize` | 10 MiB | `json_config.go:386` |
| Special vehicle route types | 711, 712, 713, 714 | `search_stops_handler.go:24-27` |

### J.6 Table → query → handler relationships (selected)

| Table | Principal queries | Reaching handlers |
|---|---|---|
| `agencies` | `GetAgency`, `ListAgencies`, `GetAgenciesByIDs`, `GetAgenciesForStops`, `ListAgencyIds` | almost all |
| `routes` (+`routes_fts`) | `GetRoute`, `GetRoutesByIDs`, `GetRoutesForAgency`, `GetRoutesForStop(s)`, `SearchRoutesByFullText`, `GetActiveRoutesWithinBounds` | route, routes-for-agency, routes-for-location, search/route |
| `stops` (+`stops_fts`, `stops_rtree`) | `GetStop`, `GetStopsByIDs`, `GetStopsByCode`, `GetActiveStopsWithinBounds`, `GetStopIDsWithinBounds`, `SearchStopsByName`, `GetStopBoundsPerAgency` | stop, stops-for-*, search/stop, agencies-with-coverage |
| `stop_agencies` | `BuildStopAgencies`, `ClearStopAgencies`, `GetStopIDsForAgency`, `SearchStopsByName` subquery | stops-for-agency, stop-ids-for-agency, search/stop |
| `trips` | `GetTrip`, `GetTripsByIDs`, `GetAllTripsForRoute`, `GetTripsForRouteInActiveServiceIDs`, `GetTripTimeBoundsByIDs` | trip, trip-details, trips-for-*, schedule-for-route |
| `stop_times` | `GetStopTimesForTrip(IDs)`, `GetStopTimesForStop(s)InWindow`, `GetScheduleForStopOnDate`, `GetArrivalsAndDeparturesForStop` | all arrivals and schedule endpoints |
| `calendar`, `calendar_dates` | `GetActiveServiceIDsForDate`, `GetCalendarByServiceID`, `GetCalendarDateExceptionsForServiceID`, `GetFeedEndDate`, `RouteHasFutureService` | everything service-date-aware |
| `block_trip_index`, `block_trip_entry` | `GetBlockTripSequence`, `GetTripsByBlockID(s|Ordered)`, `GetBlockDetails`, `GetNextAndPreviousTripsInBlock`, `GetFirstStopOfNextTripInBlock` | block, trip-details, trips-for-route, `BuildTripStatus` |
| `block_layover` | `GetActiveLayoverBlockIDsForRoute` | trips-for-route block selection |
| `frequencies` | `GetFrequenciesForTrip(s)`, `GetFrequencyTripIDs` | arrivals, trip-details, schedule-for-stop, trips-for-* |
| `shapes` | `GetShapeByID`, `GetShapePointsByTripID(s)`, `GetShapePointsWithDistance`, `GetShapesGroupedByTripHeadSign` | shape, stops-for-route, `BuildTripStatus`, direction calculation |
| `import_metadata` | `GetImportMetadata`, `UpsertImportMetadata`, `UpdateImportTime`, `UpdateFeedExpiresAt` | ETag, freshness, expiry middleware, `/healthz`, metadata |
| `problem_reports_trip/stop` | `CreateProblemReport*`, `GetProblemReportsBy*` | the 4 problem-report endpoints |

---

## Section K: Documentation and Behavioural Contracts

### K.1 Documented purpose and authority

`README.markdown:5` — "A complete rewrite of the OneBusAway (OBA) REST API server in Golang."

`CLAUDE.md` (REST API Documentation section) sets the binding contract: **"All API endpoints MUST
behave identically to what is defined in this OpenAPI spec… If the codebase diverges from the spec, the
spec wins."** The spec is `OneBusAway/sdk-config/stainless/openapi.yml`, vendored at
`testdata/openapi.yml` and enforced two ways: `scripts/check-openapi.sh` (byte comparison skipping a
2-line header, run in CI on PRs) and `internal/restapi/openapi_conformance_test.go` (schema validation
of live responses).

`docs/README.md` states the **wiki** is canonical for specs and that the in-repo `docs/` copies are
"historical artifacts"; `docs/AGENTS.md` requires any spec change here to be pushed to its wiki page in
the same piece of work, and carries the current file↔page mapping.

### K.2 Explicit behavioural guarantees found in code or docs

- `version=2` is the only accepted API version; an absent parameter is accepted for Wayfinder/legacy
  compatibility (`version_middleware.go:12-16`).
- `includeReferences=false` returns the `references` block **present but empty**, matching Java
  (stated in `trip_handler.go:87-89`, `routes_for_agency_handler.go:57`, and others).
- A trip's `routeShortName` is always `""` because Maglev does not store the `trips.txt`
  `route_short_name` extension; clients fall back to the route reference
  (`trip_handler.go:72-77`).
- `models.Route.NullSafeShortName` is documented as "an accidental leak from the legacy Java server,
  preserved only for backward compatibility with the Wayfinder web client" (`models/routes.go:11-14`).
- `TripStatus.Scheduled` is `!Predicted` and is a **known, documented deviation** from the OpenAPI
  `TripStatus` schema, retained for existing consumers (`models/trip_details.go:66`).
- `trips-for-route` intentionally returns trips of other routes that share a block, citing
  onebusaway-application-modules issue #90 (`trips_for_route_handler.go:178-182`).
- Polyline encoding **floors** at 1e5 rather than rounding, to stay byte-for-byte identical to the Java
  `PolylineEncoder`; no simplification or duplicate filtering (`utils/polyline.go:8-12`,
  `shapes_handler.go:45-54`).
- `stop_times.shape_dist_traveled` from the publisher is deliberately ignored in favour of projection,
  mirroring Java's `StopTimeEntriesFactory`; the projection is acknowledged to drift ~90–250 m from
  Java's UTM-based algorithm (`scheduled_block_helper.go:643-659`).
- Stop-direction contract: supplied distances are used only when both the shape-point sequence and the
  trip's stop-time sequence are usable (finite, non-negative, strictly increasing, ≥2 values);
  `stops.direction` survives an upgrade when the feed is unchanged, and recalculating requires a fresh
  import into an empty database (`README.markdown`, "Stop direction calculation and upgrades").
- `minutesBefore`/`minutesAfter` are capped at 24 h — explicitly a Maglev addition, since "Java has no
  cap" (`arrival_and_departure_for_stop_handler.go:41-45`).
- Java's ±1 h `blockNotActive` guard is applied to block schedule deviation but deliberately **not** to
  per-stop predictions, with the reasoning spelled out at
  `arrival_and_departure_for_stop_handler.go:747-756`.
- Situation IDs are prefixed idempotently, because an OBA-exported RT feed already carries the agency
  prefix (`reference_utils.go:861-872`).
- Test-database rule: in Test env the DB path must be `:memory:` (`gtfsdb/helpers.go:135-137`).
- `gtfsdb/` is generated and must not be hand-edited; `fts_queries.go` is the one sanctioned exception
  (`CONTRIBUTING.md`, "Database Access").

### K.3 Developer/agent process contracts

`CONTRIBUTING.md` requires: a triaged issue per PR; ~200-line PRs; tightly scoped commits with
50-char imperative subjects; **no `Co-Authored-By` lines for coding agents**; `go vet` in both modes +
`make test` + `go fmt` before committing; new commits rather than rewritten history after review;
`r.Context()` (never `context.Background()`) inside handlers; immediate `defer Unlock()` and respect
for the documented lock ordering; `errors.Is(err, sql.ErrNoRows)` to separate 404 from 500 (with
`route_handler.go` named as the reference); reuse of the catalogued helper files before writing new
logic; and a Java-response comparison in the PR description for any endpoint the Java server also
serves.

### K.4 Documentation ↔ implementation observations

Recorded neutrally, as observations for a later reviewer — not as defects, and with no judgement about
which side should change.

| # | Documentation says | Code does | Evidence |
|---|---|---|---|
| 1 | Rate limiting is "Per-API-key … Auto-cleanup of idle limiters" (`CLAUDE.md`); "Requests per second **per API key**" (`README.markdown` config table) | A single process-wide `rate.Limiter`; exempt keys bypass it; no cleanup goroutine | `rate_limit_middleware.go:18-56`; `appconf/config.go:15` ("global shared bucket") and `main.go:71` agree with the code |
| 2 | `CONTRIBUTING.md:78`'s Code Reuse list names helper files `block_distance_helper.go` and `block_sequence_helper.go` | No such files; the logic is in `scheduled_block_helper.go` | `git ls-files internal/restapi`; the two names appear only in `CONTRIBUTING.md`, not in `CLAUDE.md` |
| 3 | `CLAUDE.md`'s endpoint table lists 25 endpoints | 27 `/api` routes are registered; the table omits `search/stop.json`, `search/route.json`, `arrivals-and-departures-for-location.json`, `config.json`, `metrics.json`, `route/{id}`, `problem-reports-for-{trip,stop}`, `/api/v2/metadata.json`, and maps both arrivals endpoints to one file | `routes.go:76-121` |
| 4 | `CLAUDE.md`: `api.Shutdown()` "stops background goroutines for request handling" | `RestAPI.Shutdown()` is an empty method | `rest_api.go:22-23` |
| 5 | `CLAUDE.md` describes in-memory static data `manager.gtfsData` with `Agencies`/`Routes`/`Stops`/`Trips` and `GetStaticData()` | `gtfs.Manager` has no such field or method; all static reads go to SQLite | `gtfs_manager.go:42-85` |
| 6 | `CLAUDE.md` / `README.markdown`: Go 1.24.2 or later | `go.mod` declares `go 1.26.0`; Dockerfiles use `golang:1.27-alpine` | `go.mod:3`, `Dockerfile:2` |
| 7 | `CLAUDE.md` test-data list includes `testdata/raba.db` ("pre-built SQLite database") | Not tracked in git; the suite builds `testdata/raba-test.db` at run time | `git ls-files testdata`, `http_test.go:34` |
| 8 | `README.markdown` env-var table documents `HEALTH_CHECK_KEY` | No reference anywhere in `.go`, `.yml` or `.json`; `/healthz` needs no key | repo-wide grep; `health_handler.go:28` |
| 9 | `README.markdown` directory list describes `migrations` ("SQL migration files") and `remote` ("production server configuration and setup scripts") | Both contain only `.gitkeep`; migration DDL lives in `gtfsdb/schema.sql` | `ls -a migrations remote` |
| 10 | `docs/horizontal-scalability-spec.md` and `docs/multi-agency-static-feeds-spec.md` describe multi-feed static ingestion, a `role` split and PostgreSQL | None of it is implemented; both documents are explicitly forward-looking specs verified "against `main`" | `docs/*-spec.md` §2 |
| 11 | `docs/mutex_contention_analysis.md` recommends copy-on-write via `atomic.Value` | Implemented with `atomic.Pointer`; the doc's own Status paragraph records that the reader half is done and writer hold time is unchanged | `realtime.go:45-48`; the doc's "Status" note |

### K.5 Unresolved specification questions (raised by the code itself)

- `distanceAlongTrip` is a two-tier best effort; a correct implementation needs block-cumulative shape
  projection, tracked as `summary/distance-along-shape-library-port.md`
  (`trips_helper.go:261-288`). That file is not in this repository.
- `runningLate`/`runningEarly` are configurable in Java but hard-coded here, tracked as
  OneBusAway/maglev issue #800 (`trips_helper.go:1340-1351`).
- `occupancyCapacity` / `occupancyCount` have no source in either SQLite or GTFS-RT and are left at
  their defaults (`trips_helper.go:92-96`, `vehicles_for_agency_handler.go:207-209`).
- `vehicles-for-agency` reports `activeTripId` for the interlined trip while the stop fields describe
  the nominal trip; reconciling them is explicitly deferred
  (`vehicles_for_agency_handler.go:261-274`).
- `metricsWrapper.QueryRowContext` always records `status="ok"` because `database/sql` defers the error
  to `Scan`; a follow-up is noted in the PR description (`gtfsdb/helpers.go:86-92`).
- `GetAgenciesForStops` has no `ORDER BY`, so for a stop served by several agencies the chosen prefix
  is whatever SQLite returns first — noted in `docs/multi-agency-static-feeds-spec.md` §2.1, which also
  observes that the ordered `GetAgencyForStop` exists but no handler calls it.

---

## Section L: Tooling and Environment Assessment

### L.1 Available

| Tool | Path / version | Notes |
|---|---|---|
| Go | `/home/aditya4044/.local/go/bin/go`, **go1.26.0 linux/amd64** | Matches `go.mod`'s `go 1.26.0` |
| Go module cache | `/home/aditya4044/go/pkg/mod`, **5.7 GB, populated** (includes `modernc.org`, `github.com/!one!bus!away`) | Builds and vets ran offline without downloading |
| git | `/usr/bin/git` | |
| docker | `/usr/bin/docker` | Present; not exercised during this scan |
| curl, python3, node | `/usr/bin/curl`, `/usr/bin/python3`, node v24.21.0 | |

`go env`: `GOVERSION=go1.26.0`, `GOOS=linux`, `GOARCH=amd64`, **`CGO_ENABLED=0`**.

### L.2 Missing or unavailable

| Tool | Consequence |
|---|---|
| **gcc / cc / clang** | **The project's default mode cannot run here.** `make build`, `make test`, `make lint`, `make coverage`, `make test-latency` and all `bench-sqlite-*` targets are blocked. Verified: `cgo: C compiler "gcc" not found` |
| `make` | Every Makefile target must be expanded by hand |
| `jq` | `make coverage-report` blocked (the Makefile's own `check-jq` would fail) |
| `k6` | `make load-test`, `smoketest`, `stresstest` blocked (`check-k6` would fail) |
| `sqlite3` CLI | No out-of-process database inspection |
| `air`, `dlv` | No live reload, no debugger |

### L.3 Safe commands for this environment

Read-only with respect to the working tree (they write only to Go's build/test cache):

```bash
CGO_ENABLED=0 go vet  -mod=readonly -tags purego ./...
CGO_ENABLED=0 go test -mod=readonly -tags purego -count=1 ./<pkg>/...
CGO_ENABLED=0 go build -mod=readonly -tags purego -o /dev/null ./cmd/api
go list -m all ; go env ; go doc <pkg>.<Sym>
git status --porcelain ; git ls-files ; git log
```

Commands that **create or modify files** and were therefore avoided: `make build` / `build-pure`
(writes `bin/maglev`, `bin/gtfstidy`), `make coverage` (writes `coverage.out`), `make models`
(rewrites `gtfsdb/*.go`), `make update-openapi` (rewrites `testdata/openapi.yml`), `make load-test`
(writes `config.ci.json`, `ci-gtfs.db`, `maglev.log`), `scripts/download-perf-data.sh`,
`scripts/hotswap-memory-test.sh`, and every `docker-*` target. Running the binary would create a
SQLite database at `data-path` and perform outbound fetches.

### L.4 Operational limitations recorded

1. **No CGO** — the authoritative test matrix cannot be reproduced here. A pure-Go pass does not
   establish CGO-specific coverage; `CLAUDE.md` says so explicitly.
2. The checkout is on `/mnt/c` (Windows filesystem via WSL2 + OneDrive). Go commands here are
   noticeably slow (a 4-package vet took ~20 s); the file-backed test fixture DB will be slower still.
3. The full test suite was not executed (see I.4).
4. Network-dependent paths (static feed download, RT polling, `check-openapi.sh`) were not exercised:
   no outbound calls were made.
5. `perftest`-tagged tests need `testdata/perf/trimet.zip`, which is gitignored and absent.

---

## Section M: Scan Coverage Ledger

### M.1 Priority matrix — final status

| Priority | Area | Status | Remaining work |
|---|---|---|---|
| P0 | Repository baseline | **Complete** | — |
| P1 | Core application architecture | **Complete** | — |
| P1 | Data flow and domain models | **Complete** | — |
| P1 | Module and call relationships | **Complete** | — |
| P2 | APIs and integrations | **Complete** | — |
| P2 | Configuration and state | **Complete** | — |
| P2 | Processing lifecycle | **Complete** | — |
| P3 | Tests and validation infrastructure | **Partially inspected** | Harness, fixtures, mocks, organisation and tagging mapped; the bodies of the 1,378 individual test functions were not read. Full suite not executed (CGO blocked; pure-Go subset passed) |
| P3 | Dependencies and deployment | **Complete** | — |
| P4 | Supporting source and utilities | **Complete** | — |
| P4 | Documentation and contracts | **Partially inspected** | The two 800–900-line forward-looking specs in `docs/` were read at heading + "current behaviour" depth, not end to end. `.claude/skills/*` bodies and `.github/SECURITY.md` / `pull_request_template.md` inventoried but not read |
| P4 | Final coverage reconciliation | **Complete** | — |

### M.2 Per-area detail

**Inspected (implementation read):**
`cmd/api/{main,app}.go` · `internal/app/*` · `internal/appconf/{config,json_config}.go` ·
`internal/gtfs/{gtfs_manager,realtime,static,config,shapes,location_params,route_search}.go` ·
`gtfsdb/{client,config,helpers,fts_queries,stops_rtree,debugging,models,driver_cgo,driver_pure,constraint_cgo,constraint_pure}.go`
· `gtfsdb/{schema.sql,sqlc.yml}` · all 27 `internal/models/*.go` · all 12 `internal/utils/*.go` ·
`internal/{nulls,clock,servicedate,logging,metrics,buildinfo,webui}/*` ·
all 13 `internal/restapi/*_middleware.go` + `caching_middleware.go` ·
`internal/restapi/{routes,rest_api,errors,responses,response_types,db_errors,id_helpers,location_params,arrival_params,timezone_helper,shape_distance_helpers,status_capturing_writer,test_helper}.go`
· `internal/restapi/{arrivals_core,trips_helper,scheduled_block_helper,trip_updates_helper,vehicles_helper,reference_utils}.go`
· every small/medium handler in full, and the primary handler function of every large handler ·
`Makefile` · all 5 CI workflows · `Dockerfile`, `Dockerfile.dev`, both compose files · `.air*.toml` ·
`.gitignore`, `.dockerignore`, `.gitattributes`, `dependabot.yml`, `sonar-project.properties` ·
`CLAUDE.md`, `CONTRIBUTING.md`, `README.markdown` (structure + config/driver/direction sections) ·
`docs/{README,AGENTS,mutex_contention_analysis}.md` · `config*.json` + `config.schema.json` ·
`scripts/check-openapi.sh` · `loadtest/k6/thresholds.js` · `internal/restapi/http_test.go` ·
`internal/restapi/testdata/models.go` · `internal/restapi/openapi_conformance_test.go` (harness).

**Partially inspected — and precisely what remains:**

| Area | Inspected | Not inspected |
|---|---|---|
| `internal/restapi/trips_for_route_handler.go` (1,523 L) | Handler body `:31-456`, all 30 declarations with doc comments | Bodies of `resolveTripsForRouteBlocks`, `tripsForRouteBlockSpans`, `resolveInterlinedEntryTripID`, `routeZoneServiceDates`, `activeTripsInBlocks`, `resolveBlockInZone`, `zoneBlockCoversTime`, `routeBlocksAndTripsInService`, `buildTripReferences`, `tripReferenceSets.*`, `resolveDuplicatedBaseTrip` |
| `internal/restapi/trips_for_location_handler.go` (1,402 L) | Handler body `:22-176`, all 40 declarations | Bodies of `scheduledTripIDsInBounds`, `blocklessScheduledTripIDsInBounds`, `blockedScheduledTripIDsInBounds`, `selectBlockAnchors`, `buildTripsForLocationEntries` (230 L), `referenceBuilder.*` |
| `internal/restapi/arrivals_and_departures_for_location_handler.go` (814 L) | Handler body `:37-136`, all 41 declarations | Bodies of `nearbyStopsForLocation`, `nearbyCandidateIDs`, `nearbyStopsActiveOnDate`, `stopsServingRouteTypes`, `retainedAccumulator`, `parseArrivalsForLocationParams` |
| `internal/restapi/schedule_for_stop_handler.go` (686 L) | Handler body `:24-269`, all 19 declarations | Bodies of `groupScheduleRowsByRouteAndDirection`, `expandExactTimesStopTimes`, `blockBoundaries`, `pluralityHeadsign` |
| `internal/restapi/stops_for_route_handler.go` (592 L) | `:19-139`, all 22 declarations | Bodies of `processTripGroups`, `buildStopGroup`, `disambiguateGroupNames`, `polylineMerger.*` |
| `internal/restapi/{block,schedule_for_route,trip_details,trip_for_vehicle,vehicles_for_agency}_handler.go` | Each primary handler in full | `transformBlockToEntry` tail, `calculateBlockSlackTimes`, `getReferences`, `orderGroupStops`, `buildReferencedTrips`, `buildTripForVehicleReferences`, `fillTripStatusFromSharedBuilder` body |
| `internal/gtfs/advanced_direction_calculator.go` (570 L) | All 20 declarations with their substantial doc comments | Bodies of `CalculateStopDirection`, `computeFromShapesWithCache`, `calculateOrientationAtStop`, `directionFromOrientations` |
| `internal/gtfs/metrics.go` (711 L) | `MetricsSnapshot`, all declarations and their doc comments | Bodies of `GetMetrics`, `populateRealtimeMetrics`, `activeTripsByAgency`, `countActiveBlocksAt` |
| `internal/gtfs/{direction_precomputer,tidy,gtfs_manager_mock}.go` | Declarations + doc comments | Bodies |
| `gtfsdb/query.sql` (1,546 L) | All 128 query names with line numbers; ~20 queries read in full where a handler flow depended on them | The remaining query bodies |
| `docs/*-spec.md` (1,690 L) | Headings, §1 goals, §2 "how it works today" | Design sections §3–§10 of both |
| `loadtest/k6/{scenarios,smoke,hotswap_scenario}.js` | `thresholds.js` in full; `scenarios.js` traffic mix and stages | `smoke.js`, `hotswap_scenario.js` bodies |
| `scripts/{update-openapi,download-perf-data,hotswap-memory-test}.sh` | Purpose and size | Bodies |
| `.claude/skills/*` | File list and sizes | Bodies |
| `internal/restapi` test bodies (813 funcs), `internal/gtfs` (201), `gtfsdb` (91) | Names, file organisation, harness, fixtures, build tags | Individual assertion bodies |

**Excluded** — see Section B.2.

**Blocked:** CGO build/vet/test (no C compiler, verified); `make`/`jq`/`k6`/`sqlite3`-dependent
commands (not installed); `perftest` tests (fixture absent); any network-dependent verification (no
outbound calls made).

### M.3 Scan completeness statement

The scan is **complete for P0, P1, P2, P4-supporting-source and P4-reconciliation**, and
**partially complete for P3-tests and P4-documentation**, exactly as itemised in M.1 and M.2.

Every directory and every tracked file has a recorded status. A full **directory and file inventory**
was performed (all 411 tracked files). A full **source-content inspection** was *not* performed and is
not claimed: 100 % of non-test source files were opened, and roughly 85 % of non-test source lines were
read line-by-line, with the unread remainder being the private sub-helpers of five large handler files,
four large `internal/gtfs` files, and generated sqlc output — all enumerated above. No test function
body was read beyond the shared harness.

---

## Section N: Technical Reference Index

### N.1 Files to read first, by concern

| Concern | File:line |
|---|---|
| Process entry | `cmd/api/main.go:18` |
| DI + middleware composition + shutdown order | `cmd/api/app.go:113`, `:169`, `:233` |
| Dependency container | `internal/app/application.go:12` |
| Config wire format, defaults, validation, env overrides | `internal/appconf/json_config.go:35`, `:76`, `:118`, `:370` |
| Route table (the authoritative endpoint list) | `internal/restapi/routes.go:71` |
| GTFS manager state + locking policy | `internal/gtfs/gtfs_manager.go:36`, `:42`, `:117` |
| Realtime snapshot type + rebuild | `internal/gtfs/realtime.go:39`, `:832` |
| Realtime poll loop + backoff | `internal/gtfs/realtime.go:981`, `:958` |
| Static reload | `internal/gtfs/static.go:196` |
| DB client construction + backfills | `gtfsdb/client.go:20`, `:66`, `:109` |
| Import transaction | `gtfsdb/helpers.go:205` |
| Pre-import validation | `gtfsdb/helpers.go:1441` |
| Schema | `gtfsdb/schema.sql` (whole file) |
| Query catalogue | `gtfsdb/query.sql` (128 `-- name:` headers) |
| Arrivals engine (single + multi stop) | `internal/restapi/arrivals_core.go:92`, `:439` |
| Trip status engine | `internal/restapi/trips_helper.go:50` |
| Block snapshot + request cache | `internal/restapi/scheduled_block_helper.go:144`, `:122` |
| GTFS-RT delay resolution | `internal/restapi/trip_updates_helper.go:40` |
| References + situations | `internal/restapi/reference_utils.go` (whole file) |
| Response envelopes | `internal/models/response.go`, `internal/restapi/response_types.go` |
| Error envelopes | `internal/restapi/errors.go:39`, `:76`, `:121` |
| ID conventions | `internal/utils/api.go:83-115` |
| Time conventions | `internal/utils/api.go:18-80`, `internal/servicedate/servicedate.go`, `internal/models/times.go` |
| Geometry | `internal/utils/geometry.go` |
| Test harness | `internal/restapi/http_test.go:44-122` |
| Spec conformance | `internal/restapi/openapi_conformance_test.go` + `testdata/openapi.yml` |

### N.2 Key symbols

`main` · `BuildApplication` · `CreateServer` · `Run` · `gtfsConfigFromData` · `ParseAPIKeys` ·
`createClock` · `dumpConfigJSON` · `app.Application` · `appconf.{Config,JSONConfig,GtfsConfigData}` ·
`JSONConfig.{setDefaults,Validate,ToAppConfig,ToGtfsConfigData}` · `LoadFromFile` ·
`gtfs.{Manager,Config,RTFeedConfig,LocationParams,RegionBounds}` · `InitGTFSManager` ·
`Manager.{ReloadStatic,Shutdown,IsReady,GetStopsForLocation,GetRoutesForLocation,VehiclesForAgencyID,GetVehicleForTrip,GetAlertsByIDs,GetMetrics,GetRegionBounds,CheckIfOutOfBounds}`
· `mergedRealtime` · `alertIndex` · `vehicleKey` · `rebuildMergedRealtimeLocked` · `pollFeed` ·
`updateFeedRealtime` · `calculateBackoff` · `AdvancedDirectionCalculator` · `DirectionPrecomputer` ·
`gtfsdb.{Client,Queries,DBTX,Config,GtfsData}` · `NewClient` · `StoreGtfsData` ·
`ValidateAndFilterGTFSData` · `SearchRoutesByFullText` · `SearchStopsByName` ·
`GetActiveStopsWithinBounds` · `GetActiveRoutesWithinBounds` · `restapi.RestAPI` · `SetRoutes` ·
`rateLimitAndValidateAPIKey` · `rateLimitAndValidateProtectedAPIKey` · `etagStatic` ·
`BuildTripStatus` · `BuildTripSchedule` · `BuildVehicleStatus` · `GetVehicleStatusAndPhase` ·
`StaleDetector` · `arrivalsForStop` · `arrivalsForStops` · `buildArrival` ·
`activeStopTimesForWindow` · `batchArrivalEntities` · `buildArrivalsReferences` ·
`scheduledBlockSnapshot` · `computeScheduledBlockSnapshot` · `metricsForStop` ·
`keepShiftContainingTrip` · `projectStopsInSequence` · `GetScheduleDeviationForBlock` ·
`getPredictedTimes` · `situationRef` · `situationCollector` · `models.{ResponseModel,ReferencesModel,ArrivalAndDeparture,TripStatus,TripDetails,Stop,Route,Trip,Situation,ModelTime,ModelDuration}`
· `utils.{FormCombinedID,ExtractAgencyIDAndCodeID,ParseTimeParameter,ParseMaxCount,QueryInBatches,Distance,CalculateBounds,OrderStopsAlongRoute,NaturalCompare,EncodePolyline}`
· `servicedate.Date` · `clock.Clock` · `metrics.Metrics`.

### N.3 Schemas and contracts

`gtfsdb/schema.sql` (17 base + 3 virtual + 3 shadow tables) · `gtfsdb/models.go` (sqlc row structs) ·
`internal/models/*.go` (API models) · `testdata/openapi.yml` (2,776 lines, the binding external
contract) · `config.schema.json` (`additionalProperties: false`) · `models.ReferencesModel` (the
six-array references envelope).

### N.4 Configuration quick list

`port` · `host` · `env` · `api-keys` · `protected-api-keys` · `exempt-api-keys` · `rate-limit` ·
`log-level` · `log-format` · `tls-cert-path` · `tls-key-path` · `data-path` ·
`gtfs-static-feed.{url,auth-header-name,auth-header-value,enable-gtfs-tidy}` ·
`gtfs-rt-feeds[].{id,agency-ids,trip-updates-url,vehicle-positions-url,service-alerts-url,realtime-auth-header-name,realtime-auth-header-value,headers,refresh-interval,enabled}`
· env: `MAGLEV_ENABLE_PPROF` `MAGLEV_PROFILE_MUTEX` `GTFS_API_KEYS` `GTFS_PROTECTED_API_KEYS`
`MAGLEV_LOG_LEVEL` `MAGLEV_LOG_FORMAT` `GTFS_STATIC_AUTH_NAME` `GTFS_STATIC_AUTH_VALUE`
`GTFS_REALTIME_AUTH_NAME` `GTFS_REALTIME_AUTH_VALUE` `FAKETIME` `TZ`.

### N.5 Execution paths, one line each

1. startup → config → `InitGTFSManager` → static import → warm RT fetch → `MarkReady` → serve
2. 24 h ticker → `ReloadStatic` → hash gate → import → direction precompute → region bounds
3. per-feed timer → parallel sub-feed fetch → agency filter → per-feed maps → snapshot publish
4. request → 10 global middleware → mux → cache/auth/rate-limit/etag → handler → envelope
5. arrivals → window stop_times → batched entities → per-row `BuildTripStatus` → references
6. `BuildTripStatus` → vehicle → deviation → stop resolution → block snapshot → distances → position
7. problem report → param parse → single INSERT → `{}` OK envelope
8. SIGTERM → `srv.Shutdown` → `api.Shutdown` → `Metrics.Shutdown` → `GtfsManager.Shutdown` → DB close

---

## Section O: Next-Stage Context for a Separate Bug-Hunting Agent

Navigation only. No suspected defects, no hypotheses, no priorities, no suspicious-code list.

### O.1 Established architecture, in one paragraph

A single Go binary (`cmd/api`) builds one `app.Application` holding a `gtfs.Manager`, a
`gtfsdb.Client` over SQLite, a `clock.Clock` and a Prometheus registry. The manager owns all
background work: one 24-hour static-GTFS reload loop and one poll goroutine per GTFS-RT feed. Static
data lives **only** in SQLite (17 STRICT tables plus two FTS5 indexes and an R-Tree); real-time data
lives **only** in memory, as per-feed maps guarded by `realTimeMutex` plus one immutable merged
snapshot published through `atomic.Pointer` and read without any lock. `internal/restapi` registers 27
`GET` routes behind ten global middleware and a per-route cache/auth/rate-limit/ETag chain. Handlers
are thin; almost all behaviour lives in six shared engines — `arrivals_core.go`, `trips_helper.go`,
`scheduled_block_helper.go`, `trip_updates_helper.go`, `vehicles_helper.go`, `reference_utils.go` —
and every response is an OBA envelope from `internal/models` whose shape is contractually pinned to
`testdata/openapi.yml`.

### O.2 Main execution paths

See Section D (six flows, with per-step symbols and line numbers) and N.5 (one-line summaries).

### O.3 Data-flow paths that cross the most module boundaries

1. **GTFS zip → SQLite → API response.** `static.go:18` → `helpers.go:38` → `helpers.go:205` →
   `query.sql.go` → `internal/models` → JSON. Unit changes at three points: HH:MM:SS → `time.Duration`
   → int64 nanoseconds in SQLite → Unix millis in JSON.
2. **GTFS-RT protobuf → merged snapshot → arrival prediction.** `realtime.go:306` → `:345` → `:832` →
   `trip_updates_helper.go:40` / `arrival_and_departure_for_stop_handler.go:627` → `ArrivalAndDeparture`.
3. **Block geometry → distances.** `helpers.go:1242`/`:1352` (import) →
   `scheduled_block_helper.go:144` (snapshot) → `metricsForStop` → `distanceFromStop`,
   `numberOfStopsAway`, `scheduledDistanceAlongTrip`.
4. **Agency identity → ID prefixes.** `BuildStopAgencies` (import) → `GetAgenciesForStops` /
   `SearchStopsByName`'s `MIN(agency_id)` → `FormCombinedID` → every `id` in every response.
5. **Timezone → service date → stop offsets.** `agencies.timezone` → `loadAgencyLocation` →
   `servicedate.Date.Start` → `CalculateSecondsSinceServiceDate` → window and offset arithmetic.

### O.4 Contracts and schemas to hold the implementation against

`testdata/openapi.yml` (binding; `CLAUDE.md` states the spec wins on divergence) ·
`gtfsdb/schema.sql` (CHECK constraints encode many invariants) · `config.schema.json` ·
`models.ReferencesModel` (every emitted ID must resolve inside it) · the unit table in Section E.1 ·
the documented behavioural guarantees in Section K.2 · the Java-parity notes densely embedded as
comments throughout `trips_helper.go`, `trip_updates_helper.go`, `scheduled_block_helper.go`,
`vehicles_helper.go` and `reference_utils.go`, most of which cite the specific Java class and line
range they mirror.

### O.5 High-value source files and symbols (by fan-in and by concentration of logic)

| File | Why it matters |
|---|---|
| `internal/restapi/trips_helper.go` (1,513 L) | `BuildTripStatus` — 8 call sites; the single densest piece of behaviour in the repo |
| `internal/restapi/arrivals_core.go` (1,225 L) | Both arrivals pipelines and the shared reference accumulator |
| `internal/restapi/scheduled_block_helper.go` (895 L) | Block snapshot, shift splitting, stop projection, the `metricsForStop` gate |
| `internal/restapi/trip_updates_helper.go` (614 L) | All GTFS-RT delay interpretation and the ±1 h guard |
| `internal/restapi/reference_utils.go` (1,032 L) | References and situation-ID derivation; shared by nearly every handler |
| `internal/gtfs/realtime.go` (1,080 L) | Snapshot publication, the concurrency contract, agency filtering, backoff |
| `internal/gtfs/gtfs_manager.go` (917 L) | All shared mutable state and the lock-ordering policy |
| `gtfsdb/helpers.go` (1,566 L) | The whole import transaction, pooling, PRAGMAs, pre-import validation |
| `internal/restapi/trips_for_route_handler.go` (1,523 L) | The most branching handler: service days, blocks, interlining, DUPLICATED trips |
| `internal/restapi/trips_for_location_handler.go` (1,402 L) | Realtime ∪ scheduled candidate selection |
| `internal/utils/api.go` (443 L) | ID, time and parameter conventions every handler depends on |

### O.6 Available tests and verification tools

- Harness: `createTestApi(t)` / `createTestApiWithClock(t, clock.NewMockClock(…))` +
  `serveApiAndRetrieveEndpoint` / `callAPIHandler` (`internal/restapi/http_test.go`). `CONTRIBUTING.md`
  requires new handler tests to use these rather than hand-rolled `httptest` servers.
- Fixtures: `testdata/raba.zip` (RABA static), `raba-{trip-updates,vehicle-positions}.pb`,
  `unitrans-*.pb` (deliberate agency mismatch), `config_*.json`,
  `internal/restapi/testdata/models.go` (golden models).
- Mocks: `internal/gtfs/gtfs_manager_mock.go` (`Mock*`) and the `*ForTest` seams on `gtfs.Manager`
  (all writing under feed ID `"_test"`).
- Determinism: `clock.MockClock`; `clock.EnvironmentClock` + `FAKETIME` in Test env.
- Conformance: `internal/restapi/openapi_conformance_test.go`; `scripts/check-openapi.sh`.
- **In this environment** use the pure-Go mode only:
  `CGO_ENABLED=0 go test -tags purego -count=1 ./internal/restapi/ -run TestX` — and state which mode
  ran, because a pure-Go pass does not establish CGO coverage (`CLAUDE.md` is explicit about this).
  CGO mode is verified blocked here: no C compiler.
- CI gate to be aware of: coverage must stay ≥75 % after excluding `query.sql.go`, `db.go`,
  `models.go` and `*_mock.go`.

### O.7 Incompletely inspected areas the next agent should open directly

Listed with exactly what was not read — full detail in M.2.

| To understand… | Read |
|---|---|
| Block/service-day/interlining selection for trips-for-route | `internal/restapi/trips_for_route_handler.go:464-1523` |
| Candidate trip selection and reference building for trips-for-location | `internal/restapi/trips_for_location_handler.go:180-1402` |
| Nearby-stop expansion and reference retention for arrivals-for-location | `internal/restapi/arrivals_and_departures_for_location_handler.go:147-814` |
| Schedule grouping, headsign voting, `exact_times` expansion | `internal/restapi/schedule_for_stop_handler.go:275-686` |
| Direction-group stop ordering and polyline merging | `internal/restapi/stops_for_route_handler.go:143-592` |
| Stop-direction computation | `internal/gtfs/advanced_direction_calculator.go:148-570` and `direction_precomputer.go` |
| The `metrics.json` aggregation | `internal/gtfs/metrics.go:56-711` |
| Any specific SQL | `gtfsdb/query.sql` — 128 `-- name:` headers, line numbers listed in this report's scan log |
| Block slack times and block references | `internal/restapi/block_handler.go:130-287` |
| Where `vehicles-for-agency` deliberately diverges from the shared builder | `internal/restapi/vehicles_for_agency_handler.go:339-397` |
| Planned-but-unimplemented architecture (useful for telling "missing" from "not yet built") | `docs/horizontal-scalability-spec.md` §3–§10, `docs/multi-agency-static-feeds-spec.md` §3–§5 |

### O.8 Conventions worth internalising before reading any handler

1. Public IDs are `{agencyID}_{rawID}`; `FormCombinedID` returns `""` when either side is empty, and
   `ExtractAgencyIDAndCodeID` splits on the **first** `_` only.
2. A stop's agency prefix is not a column — it is derived per response from `stop_agencies`, and a stop
   served by several agencies resolves via `MIN(agency_id)` or first-row-wins depending on the query.
3. `stop_times` times are **nanoseconds since service-day midnight** and may exceed 24 h; the service
   day's base is `servicedate.Date.Start` (noon − 12 h), never a naive midnight.
4. `references` must resolve every ID the entry emits — this is why reference builders are so large.
5. `includeReferences=false` means "present but empty", not absent.
6. `errors.Is(err, sql.ErrNoRows)` ⇒ 404; anything else ⇒ 500. Context cancellation is neither.
7. Realtime reads must go through `manager.mergedRealtime()` and must never mutate what they get back.
8. Handlers must use `r.Context()`; `context.Background()` is reserved for genuinely async work.
9. `BuildTripStatus` returns `tripStatusExtras{snapshot, situations}` specifically so callers reuse
   them instead of recomputing — the per-row amplification on the plural arrivals endpoints is the
   reason `WithSnapshotCache` exists.
10. `gtfsdb/*.go` is generated except `client.go`, `config.go`, `helpers.go`, `debugging.go`,
    `fts_queries.go`, `stops_rtree.go` and the four build-tag files. Query changes go in `query.sql`
    followed by `make models`.

---

*End of report.*
