# OBA API Verify

Assess whether a change fully accomplishes its stated goal, including adequate test coverage for every sub-gap.

Typically invoked by `oba-api-review`. Can be called directly when you have a specific gap description and a diff to check against it. Run with the current working directory set to the root of your `maglev` checkout.

## Arguments

1. The stated goal or gap description — from a spec-gap issue, a PR body, or a user-supplied description of intent.
2. The diff source — PR number, branch name, or working tree (empty).

## Workspace layout

- **Spec**: `<endpoint>.md` in the `maglev.wiki` repo — resolve the repo's local path with `.claude/skills/lib/resolve-oba-repo.sh maglev.wiki` (clones it to a local cache on first use if it isn't already checked out as a sibling of `maglev`).
- **Maglev handler**: `internal/restapi/<endpoint>_handler.go` (relative to the `maglev` repo root) — handler filenames use underscores throughout, e.g. `stops-for-route` → `stops_for_route_handler.go`, so convert every hyphen in the endpoint name to an underscore before looking for the file.
- **Maglev tests**: `internal/restapi/<endpoint>_handler_test.go` (relative to the `maglev` repo root, same hyphen-to-underscore conversion).

## Steps

### 1. Parse the stated goal

Extract the endpoint name and the specific behaviours, fields, parameters, or error cases the change is meant to address. If the goal lists sub-gaps (e.g. a numbered list in a spec-gap issue), enumerate them — each will need individual coverage.

### 2. Read the relevant spec section

Run `.claude/skills/lib/resolve-oba-repo.sh maglev.wiki` to get the wiki repo's local path. If it was found as a sibling checkout or via `OBA_WORKSPACE` rather than cloned into the cache, the resolver doesn't update it automatically and will print a `Warning: ... commit(s) behind ...` to stderr if it's stale — if that happens, note it as a caveat in the final report, since the spec being read may be out of date. Then read the section(s) of `<path>/<endpoint>.md` that correspond to the stated goal. Confirm what correct behaviour looks like according to the spec.

### 3. Get the diff

- **PR**: `gh pr diff <PR> --repo OneBusAway/maglev`
- **Branch**: `git diff main...<branch>`
- **Working tree**: `git diff HEAD`

Read the handler file and test file in context if the diff alone is insufficient.

### 4. Assess production changes against the goal

For each sub-gap or behaviour named in the stated goal:
- Is it addressed by a production change in the diff?
- Does the production change correctly implement what the spec requires?
- Are any adjacent behaviours accidentally affected?

Flag anything the diff claims to address but does not, and anything the diff changes that was not part of the stated goal.

### 5. Assess test coverage

For each sub-gap or behaviour named in the stated goal:
- Is there at least one test or assertion that would catch a regression?
- Are assertions specific enough — not just `assert.NotNil` or a shape check with the wrong expected value?
- Is the test scenario correctly structured: right endpoint called, test data valid for the scenario, proper isolation and cleanup?
- Does any existing assertion get weakened or removed by the diff?

### 6. Report

**Goal check: `<endpoint>`**

Stated goal: [summary]

For each sub-gap: ✓ addressed / ✗ missing / ⚠ partial — with a brief note.

Test coverage: [adequate / gaps noted — list any uncovered sub-gaps or weak assertions]

Overall: **fully closed** / **partially closed** / **not closed**
