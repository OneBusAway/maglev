# Contributing to Maglev

## Pull Request Guidelines

### Size

Keep PRs as short as possible, ideally no more than 200 lines. Large or multi-issue PRs should usually be split into one PR per issue — reviewers will ask for this if scope creeps, so it's cheaper to split upfront. Tightly coupled fixes that can't be reviewed independently are a reasonable exception.

### Scope

Keep scope tight. Related fixes and cleanup are fine to include, but use commits to delineate concerns clearly.

### Commits

Commits must be tightly scoped and well described. Each commit should represent a single logical change with a message that explains what and why. This really helps navigate longer PRs or PRs where more than one issue is being addressed. There can be some drift from the PR/issue description, but commits should stay tight and accurate.

- Limit the subject line to 50 characters
- Capitalize the subject line
- Do not end the subject line with a period
- Separate subject from body with a blank line
- Use the imperative mood in the subject line
- Wrap the body at 72 characters
- Use the body to explain what and why

Do not write commit messages that refer indirectly to PR comments — e.g. "apply review comments," "address PR feedback,". It should be possible to delete the PR and have all necessary context for each change live in the commit itself.

Do not attribute commits to a coding agent (e.g. no `Co-Authored-By` lines for Claude or similar tools). The human contributor authored and is responsible for the code, regardless of what tooling assisted in writing it.

### Before Committing

Before committing any code, always run these steps and have them all succeed:

1. Run `go vet -tags "sqlite_fts5 sqlite_math_functions" ./...` and fix any issues identified
2. Run `make test` and fix any failing tests
3. Run `go fmt ./...` and commit all of the formatting changes

### Responding to Review Feedback

Add new commits when addressing requested changes — don't rewrite already-reviewed history. Rebasing or force-pushing after review has started means the whole PR has to be re-reviewed. Prioritize finishing reviewed PRs over starting new ones.

### Testing

Every new branch or condition introduced by a PR needs test coverage — untested code paths and leftover dead code are common review findings. That said, match the codebase's existing test-coverage conventions rather than maximizing coverage for its own sake: e.g. if no handler currently has a test for simple 500-on-DB-failure propagation, a new handler doesn't need one either just because the code is new.

Use `createTestApi(t)` together with `callAPIHandler`/`serveApiAndRetrieveEndpoint` (`internal/restapi/http_test.go`) for handler tests rather than hand-rolling `httptest.NewServer` — it's the dominant pattern across the handler test suite and keeps RABA fixture setup and request/response plumbing consistent. Prefer table-driven tests (`tests := []struct{...}{}` + `t.Run`) when covering multiple cases of the same handler.

### Code Reuse

Before writing new logic, check whether it already exists — this is the single most common category of review comment. The codebase organizes shared helpers by category rather than by handler:

- **Parameter parsing** (`internal/utils/api.go`) — functions named `Parse*`, returning a parsed value plus field errors (e.g. `ParseFloatParam`, `ParseTimeParameter`, `ParseDate`).
- **Validation** (`internal/utils/validation.go`) — functions named `Validate*`, returning just an error (e.g. `ValidateLatitude`, `ValidateDate`). Something that parses *and* returns a value belongs in `api.go`; something that only checks validity belongs in `validation.go`.
- **Handler-level ID/location extraction** (`internal/restapi/id_helpers.go`, `location_params.go`) — pulling and validating IDs or lat/lon/radius straight off `*http.Request`.
- **Reference building** (`internal/restapi/reference_utils.go`) — building the `Agency`/`Route`/`Situation` reference blocks used in list/entry responses, plus `ShouldIncludeReferences`.
- **Sorting and comparison** (`internal/utils/sort.go`, `string_utils.go`) — e.g. `NaturalCompare`.
- **Geometry and spatial math** (`internal/utils/geometry.go`, `direction.go`, `polyline.go`) — distance, bearing, bounds, polyline encoding.
- **Real-time position/status helpers** (`internal/restapi/trips_helper.go`, `vehicles_helper.go`, `block_distance_helper.go`, `block_sequence_helper.go`, `shape_distance_helpers.go`, `trip_updates_helper.go`) — trip/vehicle status, schedule deviation, and distance-along-shape/block calculations shared across the real-time endpoints.
- **Response/error envelopes** (`internal/restapi/responses.go`, `errors.go`) — standard success/error response helpers (`sendNotFound`, `sendError`, `validationErrorResponse`, `serverErrorResponse`). When a DB lookup fails, distinguish "not found" from "the query itself failed": check `errors.Is(err, sql.ErrNoRows)` and return 404 via `sendNotFound` only for that case; any other error should go through `serverErrorResponse` (500). Collapsing every error into a 404 hides real outages behind a "not found" response — `internal/restapi/route_handler.go` is a good reference for this pattern.
- **Nullable fields** (`internal/nulls`) — read `sql.NullString`/`sql.NullInt64` (and similar) fields from sqlc rows through this package (`StringOrEmpty`, `StringOrDefault`, `Int64OrDefault`, etc.) rather than hand-rolling `.Valid && .String != ""` checks, or worse, reading `.String`/`.Int64` directly without checking `.Valid` at all.
- **Generic collection helpers** (`internal/utils/maps.go`, `filters.go`) — small generics like `MapValues`, plus reference-filtering helpers like `FilterAgencies`/`FilterRoutes`.

If you're about to write a parser, validator, sort, reference builder, or status/distance calculation, search the relevant file above first — reviewers will, and will ask you to reuse or relocate rather than duplicate. If what you write is reusable and doesn't fit any existing file, add it to the file matching its category rather than leaving it local to your handler; if none fits, that's a signal to add a new shared file rather than letting a handler file become a dumping ground.

This list will drift as the code evolves — the habit that matters is checking the relevant category's file before writing new logic, not memorizing this exact inventory.

### Complexity

Keep cognitive complexity low — check the SonarCloud analysis posted on your PR, not just by eye. Break long functions into smaller ones, reduce branching paths, and introduce value objects to cut parameter counts where it helps. Use shared domain/value objects rather than maintaining multiple representations of the same concept.

### Handler Consistency and Spec Discrepancies

Flag it in the PR description if your handler's behavior differs from other handlers' — an unusual response envelope, a deviation from a commonly adopted semantic, etc. These are often spec ambiguities that only become visible when comparing handlers side by side. Resolving the discrepancy and updating the `maglev.wiki` spec is the reviewer's job, not the contributor's — don't block your PR on getting the wiki updated yourself, just flag it clearly enough that the reviewer can act on it.

## Code Conventions

### Context Propagation

Pass the request's `r.Context()` through to DB and service calls inside HTTP handlers, rather than `context.Background()`/`context.TODO()`. A handler using a background context won't cancel its downstream work when the client disconnects. `context.Background()` is legitimate for genuinely async work with no request behind it (static data reload, GTFS-RT polling loops), but not inside a live handler.

### Concurrency

In `internal/gtfs/gtfs_manager.go`, acquire a mutex and `defer Unlock()`/`defer RUnlock()` immediately — don't call it manually on multiple return branches. A `defer` guarantees the unlock happens no matter which branch returns; manual unlocking on each branch is one missed early-return away from a deadlock. Respect the lock ordering documented at the top of the file (`staticMutex` before `realTimeMutex`, never the reverse).

### Database Access

Never hand-edit anything under `gtfsdb/` — those files carry a `// Code generated by sqlc. DO NOT EDIT.` header. Add or change queries in `gtfsdb/query.sql` and run `make models` to regenerate. `gtfsdb/fts_queries.go` is a deliberate, documented exception (sqlc can't generate FTS5-specific syntax) — that's a sanctioned exception, not a precedent for adding more hand-written query files.
