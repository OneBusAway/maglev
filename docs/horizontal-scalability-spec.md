# Horizontal Scalability: Split Ingestion, PostgreSQL, and Shared Storage

## 1. Goal, in one paragraph

Maglev today is a single process that ingests GTFS data, holds it in a local
SQLite file plus in-memory structures, and serves the REST API. That ceiling
is one machine. To serve deployments at Puget Sound or NYC scale, Maglev
needs to run as **N identical, stateless API servers behind a load balancer,
all reading a shared database, with exactly one ingestion process writing to
it**. This spec defines that architecture in three connected pieces: (a) a
`role` concept that splits ingestion out of the API server while keeping the
single-binary, single-process deployment as the unchanged default; (b)
PostgreSQL as an opt-in storage engine alongside SQLite, so the database can
scale reads via replicas; and (c) an ingester-hosted GTFS-RT relay so a fleet
of servers generates one vendor fetch per feed instead of N.

This work composes with the planned ingestion overhaul in
`docs/multi-agency-static-feeds-spec.md` (multi-feed merge + stop
consolidation): that pipeline lives in `internal/gtfs`'s load path, and the
ingester role is simply its new caller. Neither spec blocks the other; §10
covers sequencing.

---

## 2. How Maglev works today (verified against `main`)

What has to change is easiest to see as a list of the ways the current
process is *not* horizontally scalable:

- **Ingestion runs in-process.** `InitGTFSManager` calls `ReloadStatic` at
  startup (`internal/gtfs/gtfs_manager.go:113,165`), which downloads, parses,
  and imports static GTFS inside the API server
  (`internal/gtfs/static.go:196-249`), then re-runs every 24h
  (`updateStaticGTFS`, `static.go:162`). Two servers would each download and
  import independently — and with a shared DB they'd race.
- **Storage is a local SQLite file.** `gtfsdb.Client` opens `DBPath` via
  either the CGO driver (`gtfsdb/driver_cgo.go`, mattn/go-sqlite3) or the
  pure-Go driver (`gtfsdb/driver_pure.go`, modernc.org/sqlite). WAL mode is
  already enabled (`gtfsdb/helpers.go:1202`), so *on one host* SQLite
  supports one writer + N readers — but it cannot span machines.
- **sqlc is SQLite-only.** `gtfsdb/sqlc.yml` declares `engine: "sqlite"`;
  `query.sql` is 1,442 lines. Two files are hand-written *because* they
  depend on SQLite virtual tables: FTS5 full-text search
  (`gtfsdb/fts_queries.go`; `routes_fts` at `schema.sql:49`, `stops_fts` at
  `schema.sql:222`) and R-Tree spatial lookup (`gtfsdb/stops_rtree.go`;
  `stops_rtree` + maintenance triggers at `schema.sql:167-218`). Batch
  sizing is derived from `SQLITE_MAX_VARIABLE_NUMBER`
  (`gtfsdb/config.go:9-13`).
- **GTFS-RT never touches the DB.** Each process polls every configured RT
  feed itself (`pollFeed`, `internal/gtfs/realtime.go:731`; fetch+parse in
  `loadRealtimeData`, `realtime.go:205`) and keeps parsed trips/vehicles/
  alerts in memory behind `realTimeMutex`, merged per feed by
  `rebuildMergedRealtimeLocked` (`realtime.go:589`). N servers means N×
  polling of vendor endpoints — a problem for rate-limited or metered feeds.
- **Derived state is computed at import time or cached per process.**
  Stop directions are precomputed *into the DB* so they travel with the
  data — but the precompute runs in separate writes *after* the import
  transaction commits (`static.go:98-116`), an ordering gap §5.2 fixes.
  Region bounds are recomputed after each reload (`static.go:223`), and
  the `DirectionCalculator` keeps a per-process cache cleared on change
  (`static.go:232-234`).
- **Change detection already lives in the DB.** One `import_metadata` row
  (`schema.sql:367`) stores the dataset hash; `GetSystemETag`
  (`gtfs_manager.go:739`) and `GetStaticLastUpdated` (`gtfs_manager.go:779`)
  read it. This is the hook §5.3's watcher builds on — nothing new needs to
  be persisted.
- **Rate limiting is per-process.** The rate-limit middleware holds its
  buckets in memory, so N servers multiply every configured limit by N.
  Documented as a known limitation in §9; a shared limiter is out of scope.

Everything else — handlers, models, response building — is already
stateless per request and needs no change.

---

## 3. Topology: one binary, three roles

New config key / CLI flag `role`, default `all`:

| Role | Static ingestion | RT vendor polling | RT relay endpoints | REST API | DB access |
|---|---|---|---|---|---|
| `all` (default) | yes (in-proc, as today) | yes (direct) | no | yes | read-write |
| `ingester` | yes | yes (fetch + cache bytes) | yes | no (healthz/status only) | read-write |
| `server` | **no** | no (polls ingester) | no | yes | **read-only** |

- **`role=all` is today's behavior, observably.** No config file changes,
  no new processes, SQLite default; API behavior is identical. (Not quite
  byte-for-byte on disk: PR 2's cross-cutting fixes — `busy_timeout`, the
  hash-write reorder, the post-import WAL checkpoint — apply to all roles,
  so the DB *file* can differ from `main` while its logical content is
  identical. §11 tests accordingly.) The web UI (`internal/webui`) follows
  the REST API column above: servers and `role=all` serve it; the ingester
  does not register it.
- **`role=ingester` is a singleton.** It owns all writes: static
  fetch → (multi-feed merge → consolidation, per the multi-agency spec) →
  import → direction precompute, on the existing 24h loop, plus RT vendor
  polling (§6). It serves only internal endpoints (§6.2) — no OBA REST API.
  Running two ingesters against one DB is an operator error; v1 does not
  arbitrate it (§9). One existing constant must scale with the role: the
  periodic reload wraps each `ReloadStatic` in a **5-minute context
  timeout** (`static.go:176`) — fine for RABA, impossible for an NYC-scale
  delete-and-reinsert, and the context is threaded through the import, so
  the transaction would abort mid-flight every cycle. PR 2 makes it
  configurable, defaulting to no timeout (SIGTERM-driven cancellation
  covers shutdown — §9 already declares mid-import cancellation safe).
- **`role=server` never writes.** At startup it opens the DB and waits for
  `import_metadata` to exist — **indefinitely** (configurable timeout),
  reporting "starting" via healthz. The existing startup backoff schedule
  (`gtfs_manager.go:117-120`) totals ~110 seconds, far shorter than a first
  NYC-scale import, so reusing it would crash-loop the fleet during initial
  bring-up; the server role gets its own patient wait loop. The loop's
  error taxonomy matters, and it has three classes, not two:
  - **Not-ready** (keep waiting): "row not found"; "relation/table does
    not exist" (fresh Postgres before the ingester has applied DDL); and,
    in the same-host SQLite topology, the DB file not existing yet —
    `mode=ro` cannot create a missing file, so the read-only *open
    itself* sits inside the wait loop rather than before it (today
    `NewClient` opens eagerly and would exit instead of waiting).
  - **Retryable** (keep waiting, log at warning): connection refused,
    timeouts, and server-unavailable errors — a server starting while a
    replica or pooler reboots must not crash-loop when an import already
    exists.
  - **Fatal**: invalid configuration, authentication failures, and
    `insufficient_privilege` — a misconfigured DB role (§4.1), not a
    not-ready dataset.

  Once data exists, it computes region bounds and starts serving, learning
  about new datasets by watching `import_metadata` (§5.3).
- **Ingester HA is explicitly not v1.** If the ingester dies, servers keep
  serving the last imported static data indefinitely. RT degrades the same
  way a vendor outage does today: after `staleFeedThreshold` (5 minutes,
  `realtime.go:34`) of failed relay fetches, each server's circuit breaker
  clears that feed's RT data (`realtime.go:802-807` → `clearFeedData`) —
  deliberate, since serving hour-old "real-time" predictions is worse than
  serving none. Restart the ingester; it is stateless apart from the DB.
  (One runbook caveat for the same-host SQLite topology: see the read-only
  WAL note in §3.1.)

### 3.1 Deployment shapes this enables

1. **Today (unchanged):** one process, `role=all`, SQLite file.
2. **Single host, split:** ingester + N servers on one machine sharing the
   SQLite file via WAL (one writer + N readers — already enabled,
   `helpers.go:1202`). Useful as a migration stepping-stone and for testing
   the role split without standing up Postgres. Servers open SQLite with
   `mode=ro` so the read-only contract is enforced, not just promised —
   against *bugs*, that is: a compromised process running as the same OS
   user can reopen the file read-write, so filesystem permissions (or the
   Postgres GRANTs of §4.1) are the actual security boundary; don't cite
   `mode=ro` as one. Three operational realities to build in rather than
   discover:
   - Read-only open is a real `gtfsdb` change, not a DSN tweak: `createDB`
     unconditionally applies write PRAGMAs and runs DDL on every open
     (`helpers.go:135-161`), both of which fail on a read-only connection,
     and `mode=ro` needs `file:` URI plumbing that differs between the
     mattn and modernc drivers. PR 2 owns this.
   - A read-only process cannot recover a WAL. A server (re)started while
     the ingester is down and the WAL needs recovery may fail to open —
     running servers survive an ingester outage, restarting ones might
     not. Documented limitation of this topology; Postgres has no
     equivalent.
   - No `busy_timeout` is set today (`helpers.go:1192-1203`), and the
     daily whole-dataset import writes a WAL roughly the size of the
     dataset while N reader processes can starve checkpoints. Add
     `busy_timeout` for all roles — **per connection, not per pool**:
     `database/sql` is a connection pool, so a post-open `PRAGMA` only
     configures whichever connection executed it; the setting must go
     through the DSN/connector path both drivers support (`_busy_timeout`
     / `_pragma=busy_timeout(...)` URI parameters) so every pooled
     connection gets it. Have the ingester run `wal_checkpoint(TRUNCATE)`
     after each import, and define its failure mode: TRUNCATE returns
     `SQLITE_BUSY` whenever reader snapshots are active, which with N
     polling servers will happen routinely — the ingester logs the
     outcome and tolerates a non-truncated WAL (the passive-degraded
     checkpoint still moved frames; the next quiet moment or import
     truncates it), rather than retry-looping against its own readers.
3. **Multi-host:** ingester writes to a Postgres primary; each server points
   at the primary, a read replica, or a pooler (§4). This is the Puget
   Sound / NYC shape.

---

## 4. Storage: dual engine, dual generation, one query source

### 4.1 Config

A new `database` block replaces (and back-compatibly wraps) the current
`GTFSDataPath` / `-data-path`:

```json
{
  "role": "server",
  "database": {
    "engine": "postgres",
    "dsn": "postgres://maglev:…@db-replica-2:5432/maglev"
  }
}
```

- `engine`: `"sqlite"` (default) or `"postgres"`.
- SQLite form: `{ "engine": "sqlite", "path": "./gtfs.db" }`. When the block
  is absent entirely, the legacy `-data-path` / existing config behavior is
  used unchanged.
- Validation: `postgres` requires `dsn` and forbids `path`; `sqlite` the
  reverse. `role=server` + `engine=sqlite` warns unless the path is
  readable (same-host topology, §3.1). DSNs never appear in logs or
  `--dump-config` output (redact like the existing auth-header handling).

**Read replicas require zero in-app routing logic.** Because servers never
write, "support read replicas" collapses to: each server's `dsn` may point
at any replica or pooler; only the ingester is configured with the primary.
No read/write splitting, no replica lag bookkeeping in code — the only
lag-visible artifact is a server briefly serving the previous dataset, which
the §5 cutover design already makes safe. Replica lag SLOs are an ops
concern, documented, not coded.

**"Servers never write" is enforced with GRANTs, not promised in app
logic.** It is the load-bearing premise of both the replica story above
and §5.2's consistency argument, so it gets a real boundary: the server
role's DSN uses a dedicated database role with `SELECT`-only grants on the
schema — no INSERT/UPDATE/DELETE, no DDL — with
`default_transaction_read_only=on` in the DSN as belt-and-suspenders.
Only the ingester's role holds write and DDL privileges.
`docker-compose.scaled.yml` (§7) demonstrates the two-role GRANT setup so
the copied-from example is the secure one, and §11 verifies that a write
attempted through a server connection fails with a permission error.

Two DSN hygiene rules, stated once here: multi-host deployments should set
`sslmode=verify-full` — pgx follows the libpq default of `prefer`, which
silently downgrades to plaintext — and raw DSNs never appear in
app-generated log or error strings (pgx redacts passwords in its own
errors; keep that property at wrap sites, especially the §3 wait loop,
which logs connection failures repeatedly during bring-up).

### 4.2 Dual sqlc generation from one shared query source

Two viable designs exist here, and **v1 chooses dual generation: two sqlc
engine blocks — the existing SQLite package plus a generated `gtfsdb/pg`
package — fed from one shared query source.** The instinctive objection
to this ("duplicated row types across packages") turns out not to be a
crisis: with the right `sql_package` choice the two packages' row structs
are field-identical, identical structs convert with a plain Go
conversion, and the adapter that results is generatable one-line
boilerplate whose *compilation* doubles as the cross-engine parity check.
What dual generation buys in exchange is the thing a runtime approach
can never have: **compile-time validation of the Postgres SQL against
the Postgres schema, at `make models`.** The alternative — executing the
SQLite-generated SQL against Postgres through a placeholder-rewriting
driver — is documented at the end of this section as the fallback, with
its requirements recorded so nothing is lost if the spike (below) sends
us there.

The pieces:

- **One shared query source.** sqlc supports named parameters (`@name` /
  `sqlc.arg()`) on both the sqlite and postgresql engines, so a single
  `query.sql` feeds both engine blocks after a one-time mechanical
  `?`→named-param conversion (a standalone no-behavior-change prep PR —
  and, doubling as the decision-gate spike, the moment we learn how many
  queries the PG engine actually rejects). Only genuine dialect escapes
  fork into per-engine query files — the same FTS/spatial/`strftime` set
  any two-engine design forks.
- **A `Store` interface over the existing types, satisfied natively by
  SQLite and by a generated adapter for PG.** The PG engine block uses
  `sql_package: "database/sql"`, so `pg.Stop` is field-identical to
  `gtfsdb.Stop` (`sql.NullString`, `sql.NullInt64`, …) and Go converts
  identical structs directly. The interface is defined over the
  `gtfsdb` (SQLite) types every handler already uses. Be precise about
  the churn: `Manager.GtfsDB` is a `*gtfsdb.Client` whose `Queries`
  field is the concrete generated type, and handlers and tests
  dereference `GtfsDB.Queries` directly — so the `Queries` field's
  *type* becomes the interface. Because the SQLite `Queries` satisfies
  it as-is and the method set is identical, existing call sites keep
  compiling unchanged; the churn concentrates in the `gtfsdb.Client` /
  `Manager` type declarations and any test that constructs a concrete
  `Queries`, not in per-call-site edits. The PG adapter is one
  generatable line per method
  (`return gtfsdb.Stop(row), err`), with element-wise loops for
  slice-returning methods. If the two schemas drift, the conversions stop
  compiling — the parity check is the type system, not a CI diff.
  `helpers.go`'s import pipeline routes through the interface too, so
  `StoreGtfsData` runs unchanged on either engine.
- **No custom driver.** Servers and ingester open PG through
  `github.com/jackc/pgx/v5/stdlib` directly — no wrapping driver, no
  placeholder tokenizer, none of the optional-interface forwarding a
  wrapper would have to get right. The hand-built multi-row batch INSERTs
  (`helpers.go:942,1107`) assemble their SQL in Go loops, so emitting
  `$n` versus `?` is a one-branch change in the builders, not a rewrite
  problem.

- **Keep the shared `query.sql` in the portable subset — now enforced at
  generation time.** Most of it already is: standard joins, quoted
  `"desc"` (valid in both). Anything non-portable fails `make models`
  under the PG engine block instead of surfacing at runtime — including
  PG-strictness traps like `could not determine data type of parameter`
  in `COALESCE`-style expressions, which a runtime-rewrite design would
  only meet in the equivalence suite. The known dialect escapes get
  moved or rewritten:
  - `INSERT OR REPLACE` (`query.sql:582`) and `INSERT OR IGNORE`
    (`query.sql:130`) → `INSERT … ON CONFLICT …` forms (valid on SQLite
    ≥3.24 too, so the portable form replaces both). One semantic trap to
    handle deliberately: `OR IGNORE` also suppresses `NOT NULL`
    violations, which `ON CONFLICT DO NOTHING` does not — it only covers
    uniqueness conflicts. The `CreateFrequency` rewrite therefore either
    adds explicit validation for rows the old form silently dropped, or
    documents the stricter portable semantics as intended.
  - Datetime and scalar functions: `strftime`/`date(…, '+2 years')`
    (`query.sql:1306`), a second `STRFTIME` in the hot
    `GetActiveServiceIDsForDate` (`query.sql:429`), and the two-argument
    scalar `MAX(iso, date('now'))` (`query.sql:1306`) — Postgres' `MAX` is
    aggregate-only; the portable spelling is `GREATEST`. Each gets computed
    in Go or moved to a per-engine query file.
  - `sqlc.slice()` / `IN (/*SLICE:*/)` — 42 call sites in `query.sql`.
    sqlc.slice() is MySQL and SQLite only; PostgreSQL requires
    `= ANY($1::type[])` (or the equivalent named-param form). Method
    signatures stay slice-typed on both engines, but the SQL cannot be
    shared: SQLite still needs the runtime placeholder expansion,
    Postgres binds one array parameter. These therefore fork into
    per-engine query files (or a mechanical rewrite of each site into a
    dual-engine pair), not the portable subset.
  - These are the escapes found by inspection, not a complete inventory:
    PR 5 includes a full audit of `query.sql` and `helpers.go`, and the
    cross-engine equivalence suite (§8) is the backstop for anything the
    audit misses.
- **Per-engine hand-written files, mirroring the existing sanctioned
  exceptions.** FTS5 and R-Tree queries are already hand-written because
  sqlc can't express them; they gain Postgres twins:
  - `fts_queries_postgres.go`: `tsvector` columns + GIN index, `ts_rank`
    replacing `bm25()`. Same Go signatures, same result types. The sync
    mechanism is explicit, since SQLite keeps its FTS tables current via
    schema machinery the PG side won't have: the `tsvector` columns are
    `GENERATED ALWAYS AS (to_tsvector(…)) STORED`, so population is
    automatic on every insert — no triggers, nothing for `StoreGtfsData`
    to know about — with a clear-and-reinsert search test proving it.
  - `stops_spatial_postgres.go`: plain bounding-box `WHERE lat BETWEEN …`
    over a composite `(lat, lon)` B-tree index. **No PostGIS.** Even
    NYC-scale stop counts (tens of thousands of rows) make an R-Tree
    unnecessary on Postgres; a btree bbox scan is microseconds. This deletes
    the entire trigger apparatus (`schema.sql:189-218`) from the PG schema.
- **Two DDL files.** `schema.sql` stays the sqlc source of truth and the
  SQLite runtime DDL. A new `schema_postgres.sql` is the PG translation:
  same tables/columns/names, `BIGINT`/`DOUBLE PRECISION`/`TEXT` types chosen
  so rows scan into the *same* generated Go structs (SQLite `INTEGER` → Go
  `int64` → PG `BIGINT`; boolean-ish columns stay integer-typed for
  identical scanning), virtual tables replaced per above, applied
  idempotently at ingester startup exactly like SQLite's `IF NOT EXISTS`
  DDL is today. Drift between the two files cannot go silent: each engine
  block generates its models from its own schema, and the adapter's
  struct conversions stop compiling the moment the shapes diverge — no
  separate CI inventory diff needed.
- **Batch sizing becomes engine-aware.** `Config.SafeBatchSize`
  (`gtfsdb/config.go:37-47`) keys off `SQLITE_MAX_VARIABLE_NUMBER` (32,766);
  Postgres' limit is 65,535 bind parameters. The constant moves behind the
  engine config. (Switching bulk inserts to pgx `COPY` is a later
  optimization, noted in §9 — the multi-row INSERT path works on both.)

**Costs, stated honestly.** The one-time named-param conversion churns
`query.sql` and some generated param-struct names; the adapter package
exists and must be regenerated with the queries; every slice-returning
query on PG pays an O(n) element-wise copy (invisible next to scan and
network cost at expected result sizes, but nonzero — profile before
optimizing); and the import pipeline gains an interface indirection. All
of it is boring, reviewable code with compile-time failure modes —
which, per CONTRIBUTING's readability priorities, is the point.

**The fallback, recorded so nothing is lost — a placeholder-rewriting
driver.** Keep the single SQLite-generated package and execute its SQL
against Postgres. Requirements established during review, for whoever
picks this up: interception must live in a wrapping `database/sql`
*driver* around `pgx/v5/stdlib`, not a `gtfsdb.DBTX` wrapper — generated
`WithTx` rebinds `Queries` to the bare `*sql.Tx`
(`gtfsdb/db.go:1189-1193`) and the batch INSERTs execute directly on the
Tx, so a DBTX wrapper never sees the import path's SQL. The rewriter is
a tokenizer implementing SQLite's parameter-numbering semantics (bare
`?` and `?NNN` coexist in the generated SQL, including runtime
`/*SLICE:*/` expansions), with a corpus spanning the SQL constants,
SLICE expansions at representative lengths, and the batch builders, plus
an argument-arity property test — a mis-rewrite binds arguments to the
wrong predicates. The wrapper must also forward every optional
`database/sql` interface pgx implements (`NamedValueChecker` especially
— losing it silently changes type binding) with an interface-parity
test, and register behind `sync.Once`. Zero query churn and no adapter,
at the price of subtle runtime machinery whose correctness is only ever
exercised at runtime.

**Decision gate:** the named-param prep PR *is* the spike — it reveals
how many queries the PG engine block rejects before anything depends on
the answer. The 42 `sqlc.slice()` sites will all reject; that is a
known, mechanical rewrite, not a surprise, and does not by itself
invalidate dual generation. What the spike is looking for is *other*
rejects: a small count of distinct dialect problems beyond the escapes
list above still confirms dual generation; a large, diverse remainder
says the shared-source premise is wrong and the fallback driver is the
cheaper path. Either way the equivalence suite (§8) pins behavior, and
handlers never observe which design sits underneath.

### 4.3 What "identical behavior" means

The OpenAPI spec is the contract, and the engine must be invisible through
it: **every handler test must produce byte-identical JSON (modulo
`currentTime`) on SQLite and Postgres.** Two corners are intentionally
inexact — the only sanctioned divergences, flagged per CONTRIBUTING's
discrepancy rule:

- **FTS ranking**: `bm25()` and `ts_rank` may order fuzzy search results
  differently. Search-endpoint tests assert set-equality plus "exact
  matches rank first," not total order.
- **Truncation shuffling**: `GetStopsForLocation` randomly shuffles stops
  before truncating an over-limit result (`gtfs_manager.go:316-317`), so
  no two runs are byte-identical even on a single engine. Affected tests
  assert set-membership and count, not order.

One adjacent hazard that is *not* a sanctioned divergence: queries whose
`ORDER BY` keys admit ties may break those ties differently on the two
engines. When the equivalence suite catches one, the fix is to make the
ordering fully deterministic (append a unique key to the `ORDER BY`) —
on both engines, in the shared `query.sql` — not to add a third
exception.

---

## 5. Static data flow in split mode

### 5.1 Ingester import pipeline

Unchanged in substance — `ReloadStatic`'s pipeline (download → parse →
merge/consolidate per the multi-agency spec → hash-check → import →
direction precompute) simply runs in the ingester process. The hash gate
(`import_metadata`, `gtfsdb/helpers.go:218-259`) already makes re-imports
idempotent and cheap to skip.

### 5.2 Atomic cutover via MVCC

The import already runs clear-all-tables + reinsert inside **one
transaction**. On Postgres, MVCC gives every concurrent *statement* a
consistent snapshot — no query ever observes a half-imported table, with
zero new machinery. Be precise about the guarantee's edge, though:
consistency is per statement. Handlers issue several sequential queries per
request with no enclosing transaction, so a commit landing mid-request can
yield a response mixing old- and new-dataset rows. That race exists
in-process today, and one import per day makes its window negligible — §11
asserts statement-level consistency, not request-level. (Request-level
snapshots — repeatable-read handler transactions or versioned-schema
cutover — are the v2 answer if it ever matters.)

One real ordering bug gets fixed as part of this work: the direction
precompute runs in separate writes *after* the import transaction commits
(`static.go:98-116`), but the `import_metadata` hash — the signal §5.3's
watcher and the fleet's ETags key off — is written *inside* it. A server
could observe the new hash before directions exist. PR 2 reorders this:
the hash row is written, in its own small transaction, only after the
precompute completes — so "hash changed" means "dataset fully ready." A
crash between import and hash-write merely causes a redundant reimport on
restart, which the hash gate already handles.

The reorder must also define the *non-crash* failure:
`PrecomputeAllDirections` errors are deliberately non-fatal today
(`static.go:110-113` — the API falls back to on-demand calculation). On
precompute **error, the hash row is still written** after logging.
Otherwise the import transaction has committed (new data visible
everywhere via MVCC) while the hash never arrives: the fleet ETag stays
*old* over *new* data — an HTTP-caching correctness bug worse than the one
being fixed — the hash gate mismatches forever so the ingester
re-downloads and re-imports the full dataset every cycle, and the watcher
never fires. "Hash absent" means exactly one thing: crashed between import
and hash write.

Two Postgres footnotes, addressed rather than discovered later:

- `DELETE`-then-reinsert of NYC-scale `stop_times` (tens of millions of
  rows) in one transaction produces dead-tuple bloat. Mitigation is
  operational (autovacuum will churn; schedule imports off-peak) and cheap
  (this happens once per day). If it hurts, the v2 escape hatch is
  **versioned schemas**: import into `gtfs_import` schema, then swap names
  in a metadata transaction — a design explicitly deferred (§9), not
  precluded; nothing in v1 bakes in an assumption against it.
- The import transaction holds no locks that block readers (plain
  row-version writes). A *second concurrent import*, however, does **not**
  reliably fail loudly on its own: under READ COMMITTED, two
  delete-then-reinsert transactions serialize on row locks and the loser
  silently overwrites the winner — no duplicate-key error, no deadlock,
  just last-commit-wins. So the import transaction opens with
  `pg_advisory_xact_lock` on a fixed key (SQLite needs nothing — its
  single-writer lock already serializes), with a short `lock_timeout` so
  a second ingester fails fast with an unambiguous error instead of
  quietly interleaving. The §3 singleton contract remains the deployment
  rule; the advisory lock is what makes violating it observable.

On same-host SQLite (§3.1), WAL provides the same guarantee: readers hold
their snapshot for the duration of each read transaction while the writer
commits.

### 5.3 How servers notice a new dataset

First, be precise about what a server "noticing" even means, because it
bounds every consistency worry below: in split mode servers do not *hold*
the dataset — every handler query runs against the shared DB per request,
so when the import commits, **all servers on a given DB endpoint see the
new rows on their next statement, simultaneously**, watcher or no
watcher; servers on a lagging replica converge when their replica does
(§4.1). `GetSystemETag`
likewise reads `import_metadata` per request, so HTTP caching flips
fleet-wide at commit. The watcher exists only to refresh **per-process
derived caches**: region bounds (`computeRegionBounds`, `static.go:223`)
and the `DirectionCalculator` cache (`static.go:232-234`). The cross-node
skew a round-robin load balancer can expose is therefore "server A shows
new coverage bounds seconds before server B" — cosmetic, once a day — not
divergent datasets. (The one genuine cross-node *data* skew is replica
lag, which exists independently of the watcher and is §4.1's documented
artifact.)

The watcher itself is a trimmed `ReloadStatic` that skips load/import:

1. Read the stored hash (a single-row primary-key read). If unchanged from
   the last observed value, done.
2. If changed: recompute region bounds, clear the `DirectionCalculator`
   cache, log the new system ETag — i.e., exactly the post-import tail of
   `ReloadStatic` (`static.go:223-247`), refactored so both paths share it.

The interval is **configurable, not hard-coded** — `dataset-watch-interval`
(§7), default 30s, floor 5s — and each server offsets its poll loop by a
random phase within one interval. The jitter isn't for the poll itself (N
single-row PK reads per interval is noise at any realistic N); it
decorrelates the only synchronized work in the design, N servers running
`computeRegionBounds` in the same instant after a flip.

Two push-style alternatives were considered and rejected:

- **LISTEN/NOTIFY** — a Postgres-only code path plus reconnect handling,
  to shave seconds off reacting to a once-a-day event whose staleness
  surface is two cosmetic caches; SQLite mode would still need the poll
  as fallback.
- **Piggybacking a dataset-hash header on relay RT responses** (servers
  already poll the ingester) — same objection, plus it does nothing for
  RT-less deployments (the poll survives as fallback anyway) and couples
  static change detection to the RT path.

---

## 6. GTFS-RT: ingester as relay

### 6.1 Design

The ingester is the only process that talks to RT vendors. It fetches each
configured feed's protobuf on the feed's `refresh-interval` (reusing the
existing HTTP client, auth-header, and error-backoff machinery —
`newRealtimeHTTPClient` at `realtime.go:43`, `calculateBackoff` at
`realtime.go:709`) and caches **the raw response bytes** per
`(feed-id, kind)` in memory, with the fetch timestamp and a strong ETag
(sha256 of the bytes). It does not parse them — parsing, filtering, and the
merged-view bookkeeping remain in the API servers, untouched.

Servers keep their RT **merge/rebuild pipeline untouched** (parse →
`rebuildMergedRealtimeLocked`). The fetch-and-bookkeeping layer changes in
three ways — stated honestly, since "only the URL changes" would undersell
it:

1. URLs are rewritten to relay URLs and vendor auth headers are replaced
   by the internal key, so servers never *send* vendor credentials.
2. `loadRealtimeData` (`realtime.go:205-239`) learns conditional requests
   — today it sends no caching headers and treats any non-200 (including
   304) as an error. Each (feed, kind) fetch now has **three outcomes**:
   new data / not-modified / error, backed by a small per-(feed, kind)
   ETag + last-body cache.
3. **`pollFeed`'s success bookkeeping changes — this is load-bearing, not
   optional.** Today "success" means "new data applied": `pollFeed` resets
   `lastSuccessfulFetch` and `consecutiveErrors` only when
   `updateFeedRealtime` reports new data (`realtime.go:783-793`);
   otherwise backoff engages and the 5-minute breaker eventually clears
   the feed. Against a relay whose steady state is mostly 304s (§6.2), a
   feed whose *vendor data* is merely quiet for five minutes would be
   indistinguishable from a dead feed — the breaker would clear perfectly
   healthy RT data fleet-wide. So `updateFeedRealtime`'s return taxonomy
   and `pollFeed`'s clock handling change: not-modified resets the
   staleness clock and error counter *without* re-applying data or
   rebuilding the merged view. And the staleness clock is set from the
   relay's `X-Maglev-Fetched-At` — the time of the last successful
   *vendor* fetch — not local receive time. Otherwise the relay's
   5-minute cache window and the server's 5-minute breaker compose
   serially, and a vendor outage takes ~10 minutes to clear instead of
   today's ~5; anchoring the clock to vendor-fetch time keeps the
   end-to-end timing identical to direct polling, with the relay's 503
   rule (§6.2) as defense-in-depth rather than the primary signal.

Fleet-wide effect: one vendor fetch per feed per interval, regardless of N.

Freshness cost: a server's copy can lag the ingester's by up to one server
poll interval (default 30s, same as today's vendor polling) plus one relay
hop. Nodes may briefly disagree with each other by seconds — acceptable for
arrival predictions, and no worse than today's N independent vendor polls
disagreeing with each other.

### 6.2 Relay endpoints (ingester)

```text
GET /internal/rt/{feed-id}/trip-updates.pb
GET /internal/rt/{feed-id}/vehicle-positions.pb
GET /internal/rt/{feed-id}/service-alerts.pb
GET /internal/healthz
GET /internal/status.json      ← feed fetch times, last import hash, errors
GET /metrics                   ← ingester keeps Prometheus metrics
```

- Responses carry `ETag`, `Last-Modified`, and `X-Maglev-Fetched-At`;
  servers send `If-None-Match`, so steady-state fleet traffic is mostly
  304s.
- `404` for a feed-id/kind with no configured URL; `503` with `Retry-After`
  when the ingester has not yet completed a first successful fetch for that
  feed (distinguishing "no data yet" from "no such feed" keeps server-side
  logging honest).
- **`503` also when the cache has gone stale**: once the last successful
  vendor fetch for a (feed, kind) is older than the staleness threshold
  (aligned with the servers' `staleFeedThreshold`, 5 minutes,
  `realtime.go:34`), the relay stops serving the bytes as fresh. This rule
  is load-bearing, not defensive garnish: without it, a vendor outage is
  invisible to servers — they keep receiving 200/304, `lastSuccessfulFetch`
  keeps resetting (`realtime.go:783-793`), the 5-minute circuit breaker
  (`realtime.go:802-807`) never fires, and stale trip updates and alerts —
  which, unlike vehicles, have no per-entity expiry (`realtime.go:317-319`)
  — would serve indefinitely. With §6.1's `X-Maglev-Fetched-At` clock
  anchoring as the primary staleness signal, this 503 rule is the
  defense-in-depth layer that keeps even a misbehaving server honest.
- Observability splits with the roles: vendor-fetch metrics and pprof
  (`MAGLEV_ENABLE_PPROF`, unchanged) live on the ingester; relay-fetch and
  API metrics live on the servers. Dashboards and alerts need both scrape
  targets — worth one line in the deployment docs.
- Auth, with the threat model stated instead of implied: the relay serves
  public transit data over side-effect-free GETs, so replay is a non-issue;
  the realistic loss from a leaked key is (a) a DoS path against the
  ingester and (b) read access to feeds whose *vendor* access is
  credentialed or metered. **Network isolation of the relay port is the
  primary control; the key is defense-in-depth**, and plaintext HTTP is
  acceptable only on an isolated segment. Mechanics: `internal-api-key` is
  a **list** (accept-any-of), so rotation is add-new → roll servers →
  remove-old rather than a flag-day fleet restart. Comparison is
  constant-time over hashes (SHA-256 both sides, then
  `subtle.ConstantTimeCompare` — removes even the length leak; precedent
  at `internal/app/api_keys.go:21`). The ingester's listener honors the
  existing `tls-cert-path`/`tls-key-path` config, and the server-side
  relay client accepts `https` ingester URLs (system trust store; a
  private-CA bundle option can follow if a deployment needs one).
  Unauthenticated requests get a cheap, uniform 401 *before any routing or
  per-feed work* — uniform so feed IDs can't be enumerated via 401-vs-404
  differences — with rate-limited logging so a scanner can't flood the
  ingester's logs. And the no-key case **fails closed**: a role that
  exposes `/internal/*` (or, as a server, consumes it) with an empty
  `internal-api-key` list is a startup validation error, not a silently
  unauthenticated relay. There is no insecure-mode escape hatch in v1 —
  the key costs one config line.
- The internal listener is a hardened `http.Server`, not a bare mux: the
  same timeout/limit settings as the API server (`ReadTimeout`,
  `WriteTimeout`, `IdleTimeout`, `MaxHeaderBytes` —
  `cmd/api/app.go:199-207`) plus, at minimum, the recovery,
  request-logging, and size-limit middleware. An unhardened internal port
  would be the softest DoS target in the deployment, and a panic here
  crashes the singleton writer.
- Relay responses set `Content-Type: application/x-protobuf` and
  `X-Content-Type-Options: nosniff` — the relay re-serves vendor bytes
  verbatim, and content-sniffing is the failure mode if a relay URL ever
  lands in a browser.
- The relay's raw-byte fetch restates the 25MB response cap. The existing
  guard lives inside `loadRealtimeData` (`realtime.go:228-236`), which
  parses — the relay doesn't call it, so the new fetch function applies
  the same `io.LimitReader` bound, post-gzip-decompression as today.
  Cache memory is thereby bounded at 25MB × 3 kinds × feed count.
- `/internal/status.json` has a **defined schema** precisely because raw
  error strings leak: fetch errors in this codebase embed the full source
  URL (`realtime.go:225`), and URL-embedded keys are real (§6.3). Per
  (feed, kind): feed ID, last-success timestamp, consecutive-error count,
  and an error *class* (HTTP status / timeout / parse) — never raw error
  strings, URLs, credentials, or DSNs.
- `/metrics` is unauthenticated — Prometheus scrape configs can't send
  arbitrary custom headers, and today's API-server `/metrics` is likewise
  open (`cmd/api/app.go:165-167`) — relying on the same network isolation
  as the rest of the port. Metric labels are feed IDs, never URLs (true of
  the existing metrics, `realtime.go:780`; keep it that way).
- The relay listens on the ingester's single configured port; there is no
  second listener. `role=all` does not register these routes at all.

### 6.3 Server-side configuration

One new key for `role=server`: `ingester-url` (plus `internal-api-key`).
The same `gtfs-rt-feeds` list is shared verbatim across the whole fleet's
config — the ingester reads the vendor URLs/headers from it; servers read
the feed IDs, refresh intervals, and `agency-ids` filters from it and derive
relay URLs mechanically (`{ingester-url}/internal/rt/{feed-id}/{kind}.pb`
for each kind whose vendor URL is non-empty). One config file, two roles,
no duplication to drift.

One caveat, stated honestly: a verbatim-shared file puts vendor credentials
on every server host — present, though never sent. Servers only read `id`,
`refresh-interval`, `agency-ids`, and which URL kinds are non-empty, so
security-sensitive deployments can strip `headers` from the copy
distributed to servers, and replace any URL that embeds a key in its query
string with a non-empty placeholder (servers use it purely as a presence
flag). Behavior is identical either way.

---

## 7. Configuration summary

New keys (all optional; omitting all of them = today's behavior):

| Key | Roles | Meaning |
|---|---|---|
| `role` | all | `"all"` (default) / `"ingester"` / `"server"` |
| `database.engine` | all | `"sqlite"` (default) / `"postgres"` |
| `database.path` | sqlite | DB file path (replaces `-data-path`, which remains as a CLI alias) |
| `database.dsn` | postgres | Connection string; primary for ingester, any replica/pooler for servers |
| `ingester-url` | server | Base URL of the ingester's relay (`http` or `https`) |
| `dataset-watch-interval` | server | Seconds between `import_metadata` checks (default 30, floor 5; jittered per server — §5.3) |
| `internal-api-key` | ingester, server | Shared secret **list** for `/internal/*` (accept-any-of, enabling rotation — §6.2) |

Validation rules: `role=server` requires `ingester-url` iff any RT feeds
are configured. Beyond that, **roles silently ignore keys that don't apply
to them** — the intended pattern is one shared config file for the whole
fleet (the server role reads but doesn't act on `gtfs-static-feed(s)`; the
ingester ignores `api-keys`/`rate-limit`), so warning about "irrelevant"
keys would just generate noise on every correctly configured deployment.
A `--dump-config` run shows what the process actually uses.

`role` itself is validated: any value other than the three names fails
config load, mirroring the `Env` validation pattern
(`json_config.go:99-106`). A typo must not silently default to `all` —
that would put a second *writer* in the fleet, the exact operator error
§3 declares unarbitratable. Cheap to validate, expensive to discover.

Secrets follow the existing env-override pattern
(`json_config.go:382-442`, which already does this for API keys and feed
auth values): `MAGLEV_INTERNAL_API_KEY` and `MAGLEV_DATABASE_DSN` override
the file values, so the fleet-shared config file can omit secrets
entirely. Because `internal-api-key` is a list, its env form is defined
explicitly: comma-separated, entries whitespace-trimmed, empty entries a
validation error — the same convention the API-keys env override uses —
with a rotation test covering mixed file + env configuration. Config files that do carry secrets should be mode `0600`, and
`docker-compose.scaled.yml` demonstrates env/secret injection rather than
baked-in values. One inherited gap gets fixed on the way: `--dump-config`
redacts header values but prints RT feed URLs verbatim
(`cmd/api/app.go:323-328`), including any embedded `?key=` credentials —
PR 1 extends redaction to URL query strings, and §11 tests it.

`config.schema.json`, `config.example.json`, and a new
`docker-compose.scaled.yml` (Postgres + 1 ingester + 2 servers) document the
split topology.

---

## 8. Exact changes, by PR

Per CONTRIBUTING: each PR independently reviewable, ideally ≤200 lines of
non-test code. Phases 1–4 are pure Go/architecture with SQLite only —
Postgres does not appear until PR 5, so the riskiest work rides on an
already-proven process split.

| PR | Scope | Key files |
|---|---|---|
| **1. Role + database config plumbing** (no behavior change) | Parse/validate `role` (strict three-value check), `database.*`, `ingester-url`, `internal-api-key` (list); env overrides `MAGLEV_INTERNAL_API_KEY`/`MAGLEV_DATABASE_DSN`; extend `--dump-config` redaction to URL query strings (§7). `role` defaults to `all`; everything behaves identically. | `internal/appconf/json_config.go`, `internal/gtfs/config.go`, `cmd/api/main.go`, `cmd/api/app.go`, `config.schema.json` |
| **2. Ingester role** | `role=ingester`: run static load/refresh loops + hardened healthz/status/metrics listener (§6.2); skip REST API and webui registration. `role=server`: skip `ReloadStatic`-driven import; patient wait-for-`import_metadata` startup (§3); read-only DB open — a real `gtfsdb` change, since `createDB` applies write PRAGMAs and DDL on every open (`helpers.go:135-161`). Extract the post-import tail of `ReloadStatic` into a shared `refreshDerivedState`; move the `import_metadata` hash write to after direction precompute, still-written-on-precompute-error (§5.2); make the periodic-reload timeout configurable, default none (§3); add `busy_timeout` + post-import `wal_checkpoint(TRUNCATE)` (§3.1). New goroutines follow the existing `shutdownChan` + `WaitGroup` pattern; ingester shutdown drains the listener before stopping fetch loops, mirroring `Run`'s ordering (`cmd/api/app.go:254-273`). Per CONTRIBUTING's size guidance this lands as 2–3 stacked PRs: the `refreshDerivedState` + hash-reorder refactor first (self-contained, independently testable), then the role split, then read-only open. | `cmd/api/main.go`, `internal/gtfs/gtfs_manager.go`, `internal/gtfs/static.go`, `gtfsdb/helpers.go` |
| **3. Server dataset watcher** | Jittered, configurable `import_metadata` poll (`dataset-watch-interval`, §5.3) → `refreshDerivedState` on hash change; runs on the `shutdownChan` + `WaitGroup` pattern with a per-iteration context timeout, like `updateStaticGTFS` (`context.Background()` is legitimate here per CONTRIBUTING — no request behind it). Test: two clients on one WAL SQLite file; writer imports a second fixture; reader converges without restart. That test conflicts with today's test-env guard — `createDB` rejects every non-`:memory:` path when `Env == Test` (`helpers.go:136-138`) — so this PR relaxes the guard to also accept paths under the test's temp directory (`t.TempDir()`), keeping the original protection against tests touching real DB files. | `internal/gtfs/static.go`, `gtfsdb/helpers.go`, tests |
| **4. RT relay** | Ingester: byte-cache fetch loop + `/internal/rt/*` endpoints (ETag/304, 503 before first fetch and on stale cache, hash-then-compare key auth, nosniff/content-type, 25MB fetch cap — §6.2). The byte cache is its own type with its own mutex — not new fields on `Manager`, which already carries a documented two-lock ordering policy (`gtfs_manager.go:35-40`); the cache type is the seam between the `internal/gtfs` fetch loop and the HTTP handlers. Server: relay URL derivation, three-outcome conditional fetch, and the `updateFeedRealtime`/`pollFeed` success-bookkeeping change with `X-Maglev-Fetched-At` clock anchoring (§6.1); merge/rebuild untouched. Test: a feed returning only 304s for longer than `staleFeedThreshold` retains its data and stays at base poll interval. | new `internal/gtfs/rt_relay.go`, `internal/restapi/` internal routes, `internal/gtfs/config.go`, `internal/gtfs/realtime.go` (fetch + poll bookkeeping) |
| **5. Postgres engine** | Prep PR: mechanical `?`→named-param conversion of `query.sql` (no behavior change; doubles as the §4.2 decision-gate spike). Then: PG engine block generating `gtfsdb/pg` (`sql_package: "database/sql"`) via `pgx/v5/stdlib`; `Store` interface over the existing `gtfsdb` types + generated conversion adapter (compile-enforced schema parity); `schema_postgres.sql` + startup DDL + two-role GRANT setup (§4.1); per-engine forks for the dialect escapes (`INSERT OR REPLACE`/`OR IGNORE`, `strftime`, scalar `MAX`→`GREATEST`, full audit of `query.sql` + `helpers.go`); `fts_queries_postgres.go`, `stops_spatial_postgres.go`; engine-aware batch size + placeholder emission in the batch builders; engine-aware test-env guard (`helpers.go:136-138` rejects non-`:memory:` paths when `Env == Test`). Lands as 3–4 stacked PRs (named-param prep / engine block + adapter / schema + DDL / FTS + spatial). | `gtfsdb/*` |
| **6. Equivalence CI + load tests** | CI job: full `internal/restapi` handler suite against dockerized Postgres (same fixtures, byte-identical JSON assertions per §4.3). k6 scenario for the split topology; document results at Puget Sound-scale data. | `.github/workflows/*`, `loadtest/` |

Makefile additions: `make test-postgres` (spins up a disposable Postgres via
Docker, runs the suite with `MAGLEV_TEST_DATABASE_DSN` set), used by the CI
job and available locally.

---

## 9. Known limitations and deferred work (deliberately)

- **Ingester HA / leader election.** v1: singleton by deployment contract;
  crash = static keeps serving, RT clears after 5 minutes (§3). A lease row
  in the DB (or relying on the orchestrator's "exactly one replica") is
  future work.
- **Postgres schema migrations.** v1 DDL is create-only (`IF NOT EXISTS`,
  applied idempotently at ingester startup, mirroring
  `performDatabaseMigration`, `helpers.go:163-176`). SQLite sidesteps
  schema evolution because the daily import rewrites *data*, not schema; a
  future `ALTER TABLE` on a live Postgres deployment needs a real
  (additive, ingester-applied) migration step. Deferred until the first
  post-launch schema change forces the issue — but the deferral is named
  here so it isn't mistaken for a solved problem. One cheap guard lands
  with v1 rather than deferring: `CREATE … IF NOT EXISTS` validates
  nothing about tables that already exist, so the PG DDL stamps a schema
  version row and both roles refuse to start against a version they don't
  recognize — drift becomes a loud startup error instead of a subtle
  scan mismatch.
- **Ingester deploys vs. the 5-minute RT circuit breaker.** Servers
  tolerate a relay outage shorter than `staleFeedThreshold`; an ingester
  restart exceeding ~5 minutes clears RT fleet-wide until the next
  successful fetch. Keep deploys fast; a config knob to raise the threshold
  can be added if real deploys need it. SIGTERM mid-import is safe on both
  engines — the transaction rolls back and the hash gate retries next
  cycle.
- **Rate limiting is per-server.** N servers ⇒ N× the configured global
  rate. Documented; a shared limiter (or LB-level limiting) is out of scope.
- **Versioned-schema cutover** (§5.2) if single-transaction imports bloat at
  NYC scale.
- **pgx `COPY` bulk import** as an optimization over multi-row INSERTs.
- **Shared RT store (Redis/parsed-state-in-PG).** The relay keeps vendor
  load at 1× and required no changes to the most concurrency-sensitive code
  in the repo (`realtime.go`); a shared parsed store buys cross-node RT
  consistency and RT history, and can replace the relay later without
  config-visible breakage (servers would just stop polling).
- **Per-server memory for RT parsing** stays O(fleet size) in aggregate;
  fine at current feed sizes, revisit only if profiling says otherwise.
- **LISTEN/NOTIFY dataset push** — rejected for v1 (§5.3).
- **Cross-region / multi-primary anything.** Read replicas only.

---

## 10. Interaction with the multi-agency static feeds spec

- Its PRs 1–3 (multi-feed config, merge, consolidation) and this spec's PRs
  1–4 mostly touch different code (`internal/gtfs/merge.go`/
  `consolidation.go` vs. role plumbing / relay), though both modify
  `internal/appconf/json_config.go` and `internal/gtfs/static.go` —
  rebase-grade overlap, not a design conflict — and can land in any
  interleaving. The merge
  pipeline runs wherever `loadGTFSData` runs — which after this spec's PR 2
  is the ingester. No coordination needed beyond normal rebases.
- Its **v2 proposal (§9 there: namespaced IDs stored in the DB)** changes
  the *contents* of every ID column. That is data, not schema — both
  engines are unaffected structurally. The only ordering constraint worth
  honoring: if both efforts are in flight simultaneously, land the
  namespaced-ID import rewrite before writing Postgres-specific fixtures or
  golden files, so the equivalence suite (PR 6) is built against the final
  ID spelling and isn't churned twice.
- The multi-agency spec's combined-hash change detection (§4.4 there) is
  exactly what §5.3's watcher observes — the two designs meet at
  `import_metadata` and agree.

---

## 11. Acceptance checklist

Run before calling the feature done:

- [ ] `role=all` + no new config keys: byte-identical API responses versus
      `main`; logically identical DB (schema + row-level dump diff empty —
      the file itself differs: WAL checkpoint, hash-row transaction; §3);
      log content equivalent (goroutine interleaving makes byte-identical
      logs unenforceable).
- [ ] Single host, SQLite WAL: 1 ingester + 2 servers; kill and restart the
      ingester mid-day — running servers never error, keep serving; next
      import is picked up by both servers within one watcher interval of
      commit. Also
      restart a *server* while the ingester is down (read-only WAL open
      caveat, §3.1) and record the observed behavior.
- [ ] Postgres: full handler test suite green with byte-identical JSON
      (§4.3's two sanctioned divergences only).
- [ ] Import a new dataset while servers sustain load (k6): zero 5xx;
      statement-level consistency holds (no query observes a half-imported
      table); ETag and dataset flip fleet-wide within one watcher interval
      of the post-precompute hash write (§5.2).
- [ ] RT relay: vendor endpoints see exactly one fetch per feed per
      interval regardless of server count; server RT responses match a
      direct-poll control run; 304 ratio on the relay is >90% in steady
      state.
- [ ] Vendor outage with the ingester up: servers' staleness clock —
      anchored to `X-Maglev-Fetched-At` (§6.1) — clears the feed on the
      same ~5-minute schedule as a direct-poll outage; the relay's 503
      kicks in as backstop (§6.2).
- [ ] Quiet feed: a vendor feed whose data is unchanged for well over
      `staleFeedThreshold` (relay serving continuous 304s) retains its RT
      data fleet-wide and stays at base poll interval — the breaker does
      not fire (§6.1).
- [ ] Scheduled (not just startup) reimport of the largest target dataset
      completes on the ingester — the periodic-reload timeout no longer
      caps it at 5 minutes (§3).
- [ ] Server-side write protection is verified **twice, independently**:
      once through a connection *without* `default_transaction_read_only`
      so the `SELECT`-only GRANTs themselves reject the write
      (`insufficient_privilege`), and once with the read-only DSN setting
      so the belt-and-suspenders layer is exercised too — the DSN setting
      fails writes before the ACL check, so a single combined test would
      leave the GRANTs untested (§4.1). The server wait loop treats
      `insufficient_privilege` as fatal, not not-ready (§3).
- [ ] Ingester down 1 hour: static serving unaffected; each RT feed clears
      after ~5 minutes via the existing circuit breaker
      (`staleFeedThreshold`) rather than serving hour-old predictions;
      `/internal/status.json` and server logs make the failure obvious.
- [ ] Read replica: point one server at a replica with induced lag;
      verify the only observable artifact is delayed dataset cutover on
      that node.
- [ ] `--dump-config` redacts DSNs, internal keys, and URL query strings
      (§7); `/internal/status.json` contains no URLs, credentials, or raw
      error strings (§6.2).

---

## 12. Out of scope (deliberately)

- Kubernetes manifests / Helm charts — `docker-compose.scaled.yml` is the
  reference topology; orchestration is the operator's domain.
- Autoscaling policies, load balancer configuration, TLS termination.
- Multi-tenant (multiple regions in one DB) — one deployment per region,
  as today.
- Any change to the OBA REST API surface. This is infrastructure only; the
  OpenAPI spec remains the contract and no response changes shape.
